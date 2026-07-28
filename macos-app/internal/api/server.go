package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"openconnect.local/desktop/internal/diagnostics"
	"openconnect.local/desktop/internal/keychain"
	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/openconnect"
	"openconnect.local/desktop/internal/platform"
	"openconnect.local/desktop/internal/profiles"
)

//go:embed web/*
var assets embed.FS

type Server struct {
	profiles                       *profiles.Store
	network                        *network.Store
	keychain                       keychain.Store
	vpn                            *openconnect.Manager
	logs                           *logging.Buffer
	authMu                         sync.RWMutex
	session, bootstrap, csrf, host string
	buildCommit                    string
	handler                        http.Handler
}

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func New(ps *profiles.Store, ns *network.Store, keys keychain.Store, vpn *openconnect.Manager, logs *logging.Buffer, buildCommit string) *Server {
	s := &Server{profiles: ps, network: ns, keychain: keys, vpn: vpn, logs: logs, session: token(), bootstrap: token(), csrf: token(), buildCommit: buildCommit}
	mux := http.NewServeMux()
	s.routes(mux)
	s.handler = s.secure(mux)
	return s
}
func (s *Server) Listen() (net.Listener, error) {
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err == nil {
		s.host = l.Addr().String()
	}
	return l, err
}
func (s *Server) BootstrapURL() string {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	return "http://" + s.host + "/bootstrap?token=" + s.bootstrap
}
func (s *Server) PortalURL() string { return "http://" + s.host + "/" }
func (s *Server) Serve(l net.Listener) error {
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	return srv.Serve(l)
}
func (s *Server) routes(m *http.ServeMux) {
	m.HandleFunc("/bootstrap", method("GET", s.bootstrapHandler))
	m.HandleFunc("/api/v1/session", method("GET", s.sessionInfo))
	m.HandleFunc("/api/v1/logout", method("POST", s.logout))
	m.HandleFunc("/api/v1/status", method("GET", s.status))
	m.HandleFunc("/api/v1/connect", method("POST", s.connect))
	m.HandleFunc("/api/v1/disconnect", method("POST", s.disconnect))
	m.HandleFunc("/api/v1/profiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			s.listProfiles(w, r)
		} else if r.Method == "POST" {
			s.saveProfile(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	m.HandleFunc("/api/v1/profiles/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			s.getProfile(w, r)
		} else if r.Method == "PUT" {
			s.saveProfile(w, r)
		} else if r.Method == "DELETE" {
			s.deleteProfile(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	m.HandleFunc("/api/v1/logs", method("GET", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.logs.Entries()) }))
	m.HandleFunc("/api/v1/events", method("GET", s.events))
	m.HandleFunc("/api/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			s.listRoutes(w, r)
		} else if r.Method == "POST" {
			s.addRoute(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	m.HandleFunc("/api/v1/routes/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			s.updateRoute(w, r)
		} else if r.Method == "DELETE" {
			s.deleteRoute(w, r)
		} else {
			http.Error(w, "method not allowed", 405)
		}
	})
	m.HandleFunc("/api/v1/routes-export", method("GET", s.exportRoutes))
	m.HandleFunc("/api/v1/diagnostics", method("POST", s.diagnostics))
	m.HandleFunc("/api/v1/diagnostics/run", method("POST", s.runDiagnostics))
	m.HandleFunc("/api/v1/inspection", method("GET", s.inspection))
	m.HandleFunc("/api/v1/server-scripts/open", method("POST", s.openServerScripts))
	web, _ := fs.Sub(assets, "web")
	m.Handle("/", http.FileServer(http.FS(web)))
}

func serverScriptsDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("openconnect-desktop-csd-%d", os.Getuid()))
}
func (s *Server) openServerScripts(w http.ResponseWriter, r *http.Request) {
	if err := platform.OpenFolder(serverScriptsDir()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes := s.network.List()
	profile, ok := s.profiles.Get(r.URL.Query().Get("profileId"))
	if !ok {
		network.MarkOverlaps(routes)
		writeJSON(w, http.StatusOK, routes)
		return
	}
	present := make(map[string]bool, len(routes))
	for _, route := range routes {
		present[route.CIDR] = true
	}
	for _, cidr := range profile.RouteAdditions {
		if !present[cidr] {
			routes = append(routes, network.Route{CIDR: cidr, Source: "saved"})
		}
	}
	network.MarkOverlaps(routes)
	writeJSON(w, http.StatusOK, routes)
}

func routeID(r *http.Request) string { return strings.TrimPrefix(r.URL.Path, "/api/v1/routes/") }
func routeInput(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		CIDR string `json:"cidr"`
	}
	if decode(w, r, &in) != nil {
		return "", false
	}
	return in.CIDR, true
}
func (s *Server) addRoute(w http.ResponseWriter, r *http.Request) {
	cidr, ok := routeInput(w, r)
	if !ok {
		return
	}
	route, err := s.network.Add(cidr)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = s.vpn.AddRoute(route.CIDR); err != nil {
		_ = s.network.Delete(route.ID)
		http.Error(w, err.Error(), 503)
		return
	}
	s.logs.Add("Info", "Routing", "system route added "+route.CIDR)
	if err = s.rememberRouteAddition(route.CIDR); err != nil {
		s.logs.Add("Warning", "Routing", "route was applied but could not be saved: "+err.Error())
	}
	writeJSON(w, 201, route)
}
func (s *Server) updateRoute(w http.ResponseWriter, r *http.Request) {
	cidr, ok := routeInput(w, r)
	if !ok {
		return
	}
	previous, found := s.network.Get(routeID(r))
	if !found {
		http.Error(w, "route not found", 404)
		return
	}
	route, err := s.network.Update(routeID(r), cidr)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if err = s.vpn.ReplaceRoute(previous.CIDR, route.CIDR); err != nil {
		_, _ = s.network.Update(route.ID, previous.CIDR)
		http.Error(w, err.Error(), 503)
		return
	}
	s.logs.Add("Info", "Routing", "system route changed to "+route.CIDR)
	if err = s.rememberRouteReplacement(previous, route); err != nil {
		s.logs.Add("Warning", "Routing", "route change was applied but could not be saved: "+err.Error())
	}
	writeJSON(w, 200, route)
}
func (s *Server) deleteRoute(w http.ResponseWriter, r *http.Request) {
	route, found := s.network.Get(routeID(r))
	if !found {
		http.Error(w, "route not found", 404)
		return
	}
	if err := s.vpn.DeleteRoute(route.CIDR); err != nil {
		http.Error(w, err.Error(), 503)
		return
	}
	if err := s.network.Delete(route.ID); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.logs.Add("Info", "Routing", "system route removed "+route.CIDR)
	if err := s.rememberRouteDeletion(route); err != nil {
		s.logs.Add("Warning", "Routing", "route was removed but could not be saved: "+err.Error())
	}
	w.WriteHeader(204)
}
func (s *Server) persistentProfile() (profiles.Profile, bool) {
	status := s.vpn.Status()
	if status.ProfileID == "" {
		return profiles.Profile{}, false
	}
	p, ok := s.profiles.Get(status.ProfileID)
	return p, ok
}
func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}
func removeValue(values []string, value string) []string {
	out := values[:0]
	for _, current := range values {
		if current != value {
			out = append(out, current)
		}
	}
	return out
}
func (s *Server) rememberRouteAddition(cidr string) error {
	p, ok := s.persistentProfile()
	if !ok {
		return nil
	}
	p.RouteDeletions = removeValue(p.RouteDeletions, cidr)
	p.RouteAdditions = appendUnique(p.RouteAdditions, cidr)
	_, err := s.profiles.Save(p)
	return err
}
func (s *Server) rememberRouteDeletion(route network.Route) error {
	p, ok := s.persistentProfile()
	if !ok {
		return nil
	}
	if strings.HasPrefix(route.Source, "server") {
		p.RouteDeletions = appendUnique(p.RouteDeletions, route.CIDR)
	} else {
		p.RouteAdditions = removeValue(p.RouteAdditions, route.CIDR)
	}
	_, err := s.profiles.Save(p)
	return err
}
func (s *Server) rememberRouteReplacement(previous, current network.Route) error {
	p, ok := s.persistentProfile()
	if !ok {
		return nil
	}
	if strings.HasPrefix(previous.Source, "server") {
		p.RouteDeletions = appendUnique(p.RouteDeletions, previous.CIDR)
	} else {
		p.RouteAdditions = removeValue(p.RouteAdditions, previous.CIDR)
	}
	p.RouteAdditions = appendUnique(p.RouteAdditions, current.CIDR)
	p.RouteDeletions = removeValue(p.RouteDeletions, current.CIDR)
	_, err := s.profiles.Save(p)
	return err
}
func (s *Server) exportRoutes(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("profileId")
	p, ok := s.profiles.Get(id)
	if !ok {
		http.Error(w, "profile not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="openconnect-custom-routes.json"`)
	writeJSON(w, http.StatusOK, map[string]any{"profile": p.Name, "additions": append([]string{}, p.RouteAdditions...), "deletions": append([]string{}, p.RouteDeletions...)})
}
func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="openconnect-diagnostics.zip"`)
	if err := diagnostics.Write(w, "0.1.0", s.profiles, s.network, s.logs, s.vpn.Status()); err != nil {
		s.logs.Add("Error", "Diagnostics", err.Error())
	}
}

func method(want string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != want {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, r)
	}
}
func profileID(r *http.Request) string { return strings.TrimPrefix(r.URL.Path, "/api/v1/profiles/") }
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if r.Host != s.host {
			http.Error(w, "invalid host", 403)
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+s.host {
			http.Error(w, "invalid origin", 403)
			return
		}
		if r.URL.Path != "/bootstrap" {
			s.authMu.RLock()
			session := s.session
			s.authMu.RUnlock()
			c, err := r.Cookie("oc_session")
			if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(session)) != 1 {
				http.Error(w, "unauthorized", 401)
				return
			}
		}
		s.authMu.RLock()
		csrf := s.csrf
		s.authMu.RUnlock()
		if r.Method != "GET" && r.Method != "HEAD" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(csrf)) != 1 {
			http.Error(w, "invalid CSRF token", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) bootstrapHandler(w http.ResponseWriter, r *http.Request) {
	s.authMu.Lock()
	validToken := s.bootstrap != "" && subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.bootstrap)) == 1
	session := s.session
	if validToken {
		s.bootstrap = ""
	}
	s.authMu.Unlock()
	if !validToken {
		http.Error(w, "invalid bootstrap token", 403)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "oc_session", Value: session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Server) sessionInfo(w http.ResponseWriter, r *http.Request) {
	s.authMu.RLock()
	csrf := s.csrf
	s.authMu.RUnlock()
	writeJSON(w, 200, map[string]string{"csrfToken": csrf, "buildCommit": s.buildCommit})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.authMu.Lock()
	s.session, s.csrf = token(), token()
	s.authMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "oc_session", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.vpn.Status()) }
func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProfileID string `json:"profileId"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		OTP       string `json:"otp"`
	}
	if decode(w, r, &in) != nil {
		return
	}
	p, ok := s.profiles.Get(in.ProfileID)
	if !ok {
		http.Error(w, "profile not found", 404)
		return
	}
	if in.Password == "" || in.OTP == "" {
		if in.Password == "" {
			in.Password, _ = s.keychain.Get(p.ID)
		}
		if in.Password == "" || in.OTP == "" {
			http.Error(w, "password and OTP are required", 400)
			return
		}
	}
	if strings.TrimSpace(in.Username) != "" {
		p.Username = strings.TrimSpace(in.Username)
	}
	if err := checkServerReachable(p.Server); err != nil {
		s.logs.Add("Error", "Connectivity", err.Error())
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	s.logs.Add("Info", "Connectivity", "VPN server is reachable")
	// A captured posture payload belongs to exactly one connection attempt.
	// Remove the previous session's file so the inspector cannot attribute stale
	// HostScan data to the new VPN session.
	_ = os.Remove(capturePath())
	if err := s.vpn.Connect(p, openconnect.Credentials{Password: in.Password, OTP: in.OTP}); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	writeJSON(w, 202, s.vpn.Status())
}
func checkServerReachable(server string) error {
	return checkServerReachableWithNetwork(server, net.DefaultResolver.LookupHost, net.DialTimeout)
}
func checkServerReachableWithNetwork(server string, lookup func(context.Context, string) ([]string, error), dial func(string, string, time.Duration) (net.Conn, error)) error {
	u, err := url.Parse(server)
	if err != nil || u.Hostname() == "" {
		return fmt.Errorf("invalid VPN server URL")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	host := u.Hostname()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	addresses, err := lookup(ctx, host)
	cancel()
	if err != nil {
		return fmt.Errorf("DNS lookup for VPN server %s failed: %w", host, err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("DNS lookup for VPN server %s returned no addresses", host)
	}
	var dialErr error
	for _, ip := range addresses {
		conn, connectErr := dial("tcp", net.JoinHostPort(ip, port), 3*time.Second)
		if connectErr == nil {
			_ = conn.Close()
			return nil
		}
		dialErr = connectErr
	}
	endpoint := net.JoinHostPort(host, port)
	if errors.Is(dialErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("TCP connection to VPN server %s was refused: %w", endpoint, dialErr)
	}
	if networkErr, ok := dialErr.(net.Error); ok && networkErr.Timeout() {
		return fmt.Errorf("TCP connection to VPN server %s timed out: %w", endpoint, dialErr)
	}
	return fmt.Errorf("TCP connection to VPN server %s failed: %w", endpoint, dialErr)
}
func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.vpn.Disconnect(); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.profiles.List())
}
func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	p, ok := s.profiles.Get(profileID(r))
	if !ok {
		http.Error(w, "profile not found", 404)
		return
	}
	writeJSON(w, 200, p)
}
func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var p profiles.Profile
	if decode(w, r, &p) != nil {
		return
	}
	if id := profileID(r); r.URL.Path != "/api/v1/profiles" && id != "" {
		p.ID = id
	}
	password := p.Password
	if existing, ok := s.profiles.Get(p.ID); ok {
		p.PasswordSet = existing.PasswordSet
		if p.RouteAdditions == nil {
			p.RouteAdditions = existing.RouteAdditions
		}
		if p.RouteDeletions == nil {
			p.RouteDeletions = existing.RouteDeletions
		}
	}
	if password != "" {
		p.PasswordSet = true
	}
	p.Password = ""
	saved, err := s.profiles.Save(p)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if password != "" {
		if err = s.keychain.Set(saved.ID, password); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	writeJSON(w, 200, saved)
}
func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id := profileID(r)
	status := s.vpn.Status()
	if status.ProfileID == id && status.State != "disconnected" && status.State != "error" {
		http.Error(w, "disconnect the active profile before deleting it", http.StatusConflict)
		return
	}
	if err := s.profiles.Delete(id); err != nil {
		http.Error(w, "profile not found", 404)
		return
	}
	_ = s.keychain.Delete(id)
	w.WriteHeader(204)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "stream unsupported", 500)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	ch, done := s.logs.Subscribe()
	defer done()
	for {
		select {
		case e := <-ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			f.Flush()
		case <-r.Context().Done():
			return
		}
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "content type must be application/json", 415)
		return fmt.Errorf("content type")
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, err.Error(), 400)
		return err
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
