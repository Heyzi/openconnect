package privileged

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Server struct {
	mu                                                                  sync.Mutex // guards cmd and lastExit; never held across slow work
	opMu                                                                sync.Mutex // serializes connect, recovery and route changes
	Socket, OpenConnect, Hook, VPNCScript, StatePath, LogPath, ConfigID string
	OwnerUID                                                            int
	CaptureDir                                                          string
	Components                                                          []string // files hashed into ConfigID
	cmd                                                                 *exec.Cmd
	lastExit                                                            string
}

func (s *Server) Serve() error {
	if os.Geteuid() != 0 {
		return errors.New("helper must run as root")
	}
	if _, e := s.recoverSystemState(); e != nil {
		return e
	}
	_ = os.Remove(s.Socket)
	listener, e := net.Listen("unix", s.Socket)
	if e != nil {
		return e
	}
	defer listener.Close()
	if e = os.Chown(s.Socket, s.OwnerUID, -1); e != nil {
		return e
	}
	if e = os.Chmod(s.Socket, 0600); e != nil {
		return e
	}
	for {
		conn, e := listener.Accept()
		if e != nil {
			return e
		}
		go s.handle(conn)
	}
}

// verifyComponents re-hashes the bundle right before root executes from it:
// the bundle can be user-writable, so the hash from helper startup goes stale.
func (s *Server) verifyComponents() error {
	if len(s.Components) == 0 {
		return nil
	}
	id, e := ComponentID(s.Components...)
	if e != nil {
		return e
	}
	if id != s.ConfigID {
		return errors.New("bundled components changed since the helper started")
	}
	return nil
}
func (s *Server) running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cmd != nil
}
func (s *Server) recoverSystemState() (bool, error) {
	if _, e := os.Stat(s.StatePath); errors.Is(e, os.ErrNotExist) {
		return false, nil
	} else if e != nil {
		return false, e
	}
	if e := s.verifyComponents(); e != nil {
		return false, e
	}
	cmd := exec.Command(s.Hook)
	cmd.Env = append(os.Environ(), "OPENCONNECT_REAL_VPNC_SCRIPT="+s.VPNCScript, "OPENCONNECT_ROUTE_STATE="+s.StatePath, "OPENCONNECT_RECOVER_ONLY=1")
	if output, e := cmd.CombinedOutput(); e != nil {
		return false, fmt.Errorf("stale network state recovery failed: %s: %w", strings.TrimSpace(string(output)), e)
	}
	return true, nil
}
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(35 * time.Second))
	var req Request
	response := Response{OK: true}
	decoder := json.NewDecoder(io.LimitReader(conn, 1<<20))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&req); e != nil {
		response = Response{Error: e.Error()}
	} else if e := s.execute(req, &response); e != nil {
		response = Response{Error: e.Error()}
	}
	_ = json.NewEncoder(conn).Encode(response)
}
func (s *Server) execute(req Request, response *Response) error {
	// Fast operations must stay answerable (Disconnect above all) while a slow
	// connect, recovery or route change is in progress.
	switch req.Operation {
	case "version":
		if req.ProtocolVersion != ProtocolVersion {
			return errors.New("incompatible helper protocol")
		}
		response.ConfigID = s.ConfigID
		return nil
	case "disconnect":
		return s.disconnect()
	case "reconnect":
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.cmd == nil {
			return errors.New("VPN is not running")
		}
		return s.cmd.Process.Signal(syscall.SIGUSR2)
	case "status":
		s.mu.Lock()
		defer s.mu.Unlock()
		running := s.cmd != nil
		response.Running = &running
		response.LastExit = s.lastExit
		return nil
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	switch req.Operation {
	case "connect":
		return s.connect(req.Connect)
	case "recover":
		// An agent can restart while this helper and its OpenConnect child are
		// still alive. Never restore the pre-VPN network state in that case.
		recovered := false
		if !s.running() {
			var err error
			recovered, err = s.recoverSystemState()
			if err != nil {
				return err
			}
		}
		response.Recovered = &recovered
		return nil
	case "route.add":
		return s.routeAdd(req.Route)
	case "route.delete":
		return s.routeDelete(req.Route)
	case "route.replace":
		return s.routeReplace(req.Route)
	default:
		return errors.New("operation is not allowed")
	}
}

var macAddress = regexp.MustCompile(`(?i)^[0-9a-f]{2}([-:][0-9a-f]{2}){5}$`)

func (s *Server) connect(in *ConnectRequest) error {
	if in == nil {
		return errors.New("invalid connect request")
	}
	parsed, e := url.Parse(in.Server)
	if e != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errors.New("server must be an HTTPS URL")
	}
	allowed := map[string]bool{"anyconnect": true, "gp": true, "pulse": true, "fortinet": true}
	if !allowed[in.Protocol] {
		return errors.New("protocol is not allowed")
	}
	if s.running() {
		return errors.New("VPN is already running")
	}
	if _, e = s.recoverSystemState(); e != nil {
		return e
	}
	if e = s.verifyComponents(); e != nil {
		return e
	}
	// A captured payload belongs to exactly one connection attempt.
	if e = os.RemoveAll(s.CaptureDir); e != nil {
		return e
	}
	if in.SaveServerScripts {
		if e = os.Mkdir(s.CaptureDir, 0755); e != nil {
			return e
		}
	}
	if in.MACAddress != "" && !macAddress.MatchString(in.MACAddress) {
		return errors.New("invalid MAC address")
	}
	args := s.openConnectArgs(in)
	cmd := exec.Command(s.OpenConnect, args...)
	cmd.Env = append(os.Environ(), "OPENCONNECT_REAL_VPNC_SCRIPT="+s.VPNCScript, "OPENCONNECT_ROUTE_STATE="+s.StatePath)
	stdin, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	logFile, e := os.OpenFile(s.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	if e = os.Chown(s.LogPath, s.OwnerUID, -1); e != nil {
		_ = logFile.Close()
		return e
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if e = cmd.Start(); e != nil {
		_ = logFile.Close()
		return e
	}
	s.mu.Lock()
	s.lastExit = ""
	s.cmd = cmd
	s.mu.Unlock()
	go func(password, otp string) {
		defer stdin.Close()
		_, _ = io.WriteString(stdin, password+"\n")
		if otp != "" {
			_, _ = io.WriteString(stdin, otp+"\n")
		}
	}(in.Password, in.OTP)
	go func() {
		waitErr := cmd.Wait()
		_ = logFile.Close()
		s.mu.Lock()
		if s.cmd == cmd {
			s.cmd = nil
			if waitErr != nil {
				s.lastExit = waitErr.Error()
			}
		}
		s.mu.Unlock()
	}()
	return nil
}

// reconnectTimeout keeps OpenConnect retrying with its existing session
// cookie for an hour instead of the 300s default. Reconnects reuse the
// gateway token, so surviving a long sleep or a network change costs nothing,
// while a process exit forces a fresh password and OTP login.
const reconnectTimeout = "3600"

func (s *Server) openConnectArgs(in *ConnectRequest) []string {
	args := []string{"--protocol", in.Protocol, "--passwd-on-stdin", "--script", shellQuote(s.Hook), "--useragent", "Open AnyConnect VPN Agent", "--compression", "none", "--reconnect-timeout", reconnectTimeout, "--send-device-id"}
	if in.Verbose {
		args = append(args, "--dump-http-traffic", "-vvv")
	}
	if in.SaveServerScripts {
		args = append(args, "--csd-save", filepath.Join(s.CaptureDir, "csd-payload.bin"))
	}
	if in.MACAddress != "" {
		args = append(args, "--mac-address", in.MACAddress)
	}
	if in.Username != "" {
		args = append(args, "--user", in.Username)
	}
	if in.Group != "" {
		args = append(args, "--authgroup", in.Group)
	}
	args = append(args, in.Server)
	return args
}

// OpenConnect passes --script to /bin/sh -c rather than executing the value as
// an argv path. Quote the bundled hook so an application name containing spaces
// remains one shell word.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *Server) disconnect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil {
		return nil
	}
	return s.cmd.Process.Signal(syscall.SIGTERM)
}
func validCIDR(cidr string) error {
	if cidr == "" || strings.ContainsAny(cidr, " \t\r\n") {
		return errors.New("invalid CIDR")
	}
	_, _, e := net.ParseCIDR(cidr)
	return e
}

// routeScope reports the BSD route(8) scope flag ("-host" or "-net") for cidr.
// A stale /32 or /128 route left in the table by a previous VPN session is
// recorded as a host route, and "route delete -net" silently fails to match
// it ("not in table"), leaving the dead route in place.
func routeScope(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "-net"
	}
	ones, bits := ipnet.Mask.Size()
	if ones == bits {
		return "-host"
	}
	return "-net"
}
func (s *Server) tunnelDevice() (string, error) {
	b, e := os.ReadFile(s.StatePath)
	if e != nil {
		return "", e
	}
	var state struct {
		TunnelDevice string `json:"tunnelDevice"`
	}
	if e = json.Unmarshal(b, &state); e != nil {
		return "", e
	}
	if !ValidTunnelDevice(state.TunnelDevice) {
		return "", errors.New("invalid tunnel device")
	}
	return state.TunnelDevice, nil
}
func (s *Server) routeAdd(in *RouteRequest) error {
	if in == nil {
		return errors.New("missing route")
	}
	if e := validCIDR(in.CIDR); e != nil {
		return e
	}
	device, e := s.tunnelDevice()
	if e != nil {
		return e
	}
	return runRoute("add", in.CIDR, device)
}
func (s *Server) routeDelete(in *RouteRequest) error {
	if in == nil {
		return errors.New("missing route")
	}
	if e := validCIDR(in.CIDR); e != nil {
		return e
	}
	return runRouteDelete(in.CIDR)
}
func (s *Server) routeReplace(in *RouteRequest) error {
	if in == nil {
		return errors.New("missing route")
	}
	if e := validCIDR(in.CIDR); e != nil {
		return e
	}
	if e := validCIDR(in.PreviousCIDR); e != nil {
		return e
	}
	device, e := s.tunnelDevice()
	if e != nil {
		return e
	}
	if e = runRouteDelete(in.PreviousCIDR); e != nil {
		return e
	}
	if e = runRoute("add", in.CIDR, device); e != nil {
		_ = runRoute("add", in.PreviousCIDR, device)
		return fmt.Errorf("replace failed and previous route was restored: %w", e)
	}
	return nil
}

// routeArgs builds route(8) arguments; without -inet6 it rejects IPv6 addresses
// with "bad address".
func routeArgs(action, cidr string, rest ...string) []string {
	args := []string{"-n", action}
	if strings.Contains(cidr, ":") {
		args = append(args, "-inet6")
	}
	return append(args, rest...)
}
func runRoute(action, cidr, device string) error {
	return exec.Command("/sbin/route", routeArgs(action, cidr, "-net", cidr, "-interface", device)...).Run()
}
func runRouteDelete(cidr string) error {
	return exec.Command("/sbin/route", routeArgs("delete", cidr, routeScope(cidr), cidr)...).Run()
}
