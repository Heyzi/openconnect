package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"openconnect.local/desktop/internal/observability"
	"openconnect.local/desktop/internal/profiles"
)

type diagnosticCheck struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	DurationMS int64  `json:"durationMs,omitempty"`
}

type diagnosticReport struct {
	RanAt   time.Time         `json:"ranAt"`
	Server  string            `json:"server"`
	Overall string            `json:"overall"`
	Checks  []diagnosticCheck `json:"checks"`
}

func capturePath() string { return filepath.Join(serverScriptsDir(), "csd-payload.bin") }

func (s *Server) inspection(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, observability.Build(s.vpn.Status(), s.logs.Entries(), capturePath()))
}

func (s *Server) runDiagnostics(w http.ResponseWriter, r *http.Request) {
	p, ok := s.diagnosticProfile(r.URL.Query().Get("profileId"))
	if !ok {
		http.Error(w, "select a VPN profile first", http.StatusBadRequest)
		return
	}
	status := s.vpn.Status()
	pinnedIP := ""
	if status.ProfileID == p.ID {
		if status.Server != "" {
			p.Server = status.Server
		}
		if status.State == "connected" {
			pinnedIP = status.ServerIP
		}
	}
	report := diagnose(p, status.State, len(s.network.List()), capturePath(), pinnedIP)
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) diagnosticProfile(id string) (profiles.Profile, bool) {
	if id != "" {
		return s.profiles.Get(id)
	}
	status := s.vpn.Status()
	if status.ProfileID != "" {
		return s.profiles.Get(status.ProfileID)
	}
	return profiles.Profile{}, false
}

func diagnose(profile profiles.Profile, vpnState string, routeCount int, posturePath string, pinnedIP string) diagnosticReport {
	report := diagnosticReport{RanAt: time.Now().UTC(), Server: profile.Server, Overall: "pass"}
	add := func(id, name, status, detail string, started time.Time) {
		report.Checks = append(report.Checks, diagnosticCheck{ID: id, Name: name, Status: status, Detail: detail, DurationMS: time.Since(started).Milliseconds()})
		if status == "fail" {
			report.Overall = "fail"
		} else if status == "warn" && report.Overall == "pass" {
			report.Overall = "warn"
		}
	}
	u, err := url.Parse(profile.Server)
	if err != nil || u.Hostname() == "" {
		report.Overall = "fail"
		report.Checks = append(report.Checks, diagnosticCheck{ID: "server", Name: "Server URL", Status: "fail", Detail: "Invalid VPN server URL"})
		return report
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "443"
	}
	endpoint := net.JoinHostPort(host, port)

	started := time.Now()
	if pinnedIP != "" {
		// While connected, vpnc-script has replaced the system DNS servers with
		// the gateway's, which usually can't resolve the gateway's own public
		// hostname (split-horizon DNS). Reuse the address resolved before the
		// tunnel came up instead of re-resolving through DNS that no longer
		// serves it.
		add("dns", "DNS resolution", "pass", "Using address pinned at connect time: "+pinnedIP, started)
		endpoint = net.JoinHostPort(pinnedIP, port)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		addresses, lookupErr := net.DefaultResolver.LookupHost(ctx, host)
		cancel()
		if lookupErr != nil {
			add("dns", "DNS resolution", "fail", lookupErr.Error(), started)
		} else {
			add("dns", "DNS resolution", "pass", strings.Join(addresses, ", "), started)
		}
	}

	started = time.Now()
	conn, dialErr := net.DialTimeout("tcp", endpoint, 4*time.Second)
	if dialErr != nil {
		add("tcp", "TCP connection", "fail", dialErr.Error(), started)
	} else {
		remote := conn.RemoteAddr().String()
		_ = conn.Close()
		add("tcp", "TCP connection", "pass", "Connected to "+remote, started)
	}

	started = time.Now()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	tlsConn, tlsErr := tls.DialWithDialer(dialer, "tcp", endpoint, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if tlsErr != nil {
		add("tls", "TLS certificate", "fail", tlsErr.Error(), started)
	} else {
		state := tlsConn.ConnectionState()
		detail := fmt.Sprintf("TLS %x; certificate expires %s", state.Version, state.PeerCertificates[0].NotAfter.UTC().Format(time.RFC3339))
		_ = tlsConn.Close()
		add("tls", "TLS certificate", "pass", detail, started)
	}

	started = time.Now()
	if vpnState == "connected" {
		add("tunnel", "VPN tunnel", "pass", "Tunnel is connected", started)
	} else {
		add("tunnel", "VPN tunnel", "warn", "Current state: "+vpnState, started)
	}
	started = time.Now()
	if routeCount > 0 {
		add("routes", "VPN routes", "pass", fmt.Sprintf("%d effective routes", routeCount), started)
	} else {
		add("routes", "VPN routes", "warn", "No active VPN routes", started)
	}
	started = time.Now()
	if info, statErr := os.Stat(posturePath); statErr == nil && !info.IsDir() {
		add("posture", "Posture payload", "pass", fmt.Sprintf("Captured %d bytes", info.Size()), started)
	} else if profile.SaveServerScripts {
		add("posture", "Posture payload", "warn", "Capture enabled, but the gateway has not supplied a payload", started)
	} else {
		add("posture", "Posture payload", "pass", "Capture is disabled; no payload observed", started)
	}
	return report
}
