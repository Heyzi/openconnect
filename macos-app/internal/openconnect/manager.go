package openconnect

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/privileged"
	"openconnect.local/desktop/internal/profiles"
)

type Status struct {
	State       string        `json:"state"`
	ProfileID   string        `json:"profileId,omitempty"`
	ProfileName string        `json:"profileName,omitempty"`
	Server      string        `json:"server,omitempty"`
	LastError   string        `json:"lastError,omitempty"`
	StartedAt   *time.Time    `json:"startedAt,omitempty"`
	Traffic     *TrafficStats `json:"traffic,omitempty"`
}
type Credentials struct{ Password, OTP string }
type Manager struct {
	mu        sync.RWMutex
	helper    privileged.Client
	statePath string
	logPath   string
	logs      *logging.Buffer
	routes    *network.Store
	status    Status
	profile   profiles.Profile
}
type scriptState struct {
	TunnelDevice string    `json:"tunnelDevice"`
	Reason       string    `json:"reason"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Routes       []struct {
		CIDR   string `json:"cidr"`
		Source string `json:"source"`
	} `json:"routes"`
}

func New(socket, statePath, logPath string, logs *logging.Buffer, routes *network.Store) *Manager {
	return &Manager{helper: privileged.Client{Socket: socket}, statePath: statePath, logPath: logPath, logs: logs, routes: routes, status: Status{State: "disconnected"}}
}
func NewWithClient(client privileged.Client, statePath string, logs *logging.Buffer, routes *network.Store) *Manager {
	return &Manager{helper: client, statePath: statePath, logs: logs, routes: routes, status: Status{State: "disconnected"}}
}
func (m *Manager) Status() Status { m.mu.RLock(); defer m.mu.RUnlock(); return m.status }
func (m *Manager) RecoverStaleSystemState() (bool, error) {
	response, err := m.helper.Query(privileged.Request{Operation: "recover"})
	if err != nil || response.Recovered == nil {
		return false, err
	}
	return *response.Recovered, nil
}
func (m *Manager) Connect(p profiles.Profile, credentials Credentials) error {
	m.mu.Lock()
	if m.status.State != "disconnected" && m.status.State != "error" {
		m.mu.Unlock()
		return errors.New("a VPN connection is already active")
	}
	m.mu.Unlock()
	request := privileged.Request{Operation: "connect", Connect: &privileged.ConnectRequest{Server: p.Server, Protocol: p.Protocol, Username: p.Username, Group: p.Group, Password: credentials.Password, OTP: credentials.OTP, Verbose: p.Verbose, SaveServerScripts: p.SaveServerScripts, MACAddress: p.MACAddress}}
	if err := m.helper.Do(request); err != nil {
		return err
	}
	now := time.Now().UTC()
	m.mu.Lock()
	m.status = Status{State: "connecting", ProfileID: p.ID, ProfileName: p.Name, Server: p.Server, StartedAt: &now}
	m.profile = p
	m.mu.Unlock()
	m.routes.ClearServer()
	m.logs.Add("Info", "OpenConnect", "root helper started connection")
	go m.waitForApplied(now, p, time.Time{})
	go m.watchLog(now)
	go m.watchProcess(now)
	go m.watchTraffic(now)
	return nil
}

func (m *Manager) watchTraffic(start time.Time) {
	tracker := trafficTracker{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.RLock()
		active := m.status.StartedAt != nil && m.status.StartedAt.Equal(start) &&
			(m.status.State == "connecting" || m.status.State == "connected" || m.status.State == "disconnecting")
		m.mu.RUnlock()
		if !active {
			return
		}
		contents, err := os.ReadFile(m.statePath)
		if err != nil {
			continue
		}
		var state scriptState
		if json.Unmarshal(contents, &state) != nil || state.TunnelDevice == "" {
			continue
		}
		counters, err := interfaceByteCounters(state.TunnelDevice)
		if err != nil {
			continue
		}
		stats := tracker.sample(counters, time.Now())
		m.mu.Lock()
		if m.status.StartedAt != nil && m.status.StartedAt.Equal(start) {
			m.status.Traffic = &stats
		}
		m.mu.Unlock()
	}
}

func (m *Manager) watchProcess(start time.Time) {
	// The privileged helper owns the child process, so the unprivileged agent
	// must explicitly observe it. In particular, OpenConnect can exit while the
	// Mac is asleep after its CSTP reconnect attempts are exhausted.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	helperFailures := 0
	for range ticker.C {
		m.mu.RLock()
		active := m.status.StartedAt != nil && m.status.StartedAt.Equal(start) &&
			(m.status.State == "connecting" || m.status.State == "connected")
		m.mu.RUnlock()
		if !active {
			return
		}
		response, err := m.helper.Query(privileged.Request{Operation: "status"})
		if err != nil || response.Running == nil {
			helperFailures++
			if helperFailures < 6 {
				continue
			}
		} else {
			helperFailures = 0
		}
		if response.Running != nil && *response.Running {
			continue
		}
		reason := "OpenConnect exited unexpectedly"
		if helperFailures > 0 {
			reason = "privileged helper is unavailable"
		} else if response.LastExit != "" {
			reason += ": " + response.LastExit
		}
		changed := false
		m.mu.Lock()
		if m.status.StartedAt != nil && m.status.StartedAt.Equal(start) &&
			(m.status.State == "connecting" || m.status.State == "connected") {
			m.status.State = "error"
			m.status.LastError = reason
			changed = true
		}
		m.mu.Unlock()
		if changed {
			m.routes.Clear()
			m.logs.Add("Error", "OpenConnect", reason)
		}
		return
	}
}
func (m *Manager) watchLog(start time.Time) {
	offset, remainder := 0, ""
	for {
		m.mu.RLock()
		active := m.status.StartedAt != nil && m.status.StartedAt.Equal(start) &&
			(m.status.State == "connecting" || m.status.State == "connected" || m.status.State == "disconnecting")
		m.mu.RUnlock()
		offset, remainder = m.readLog(offset, remainder, !active)
		if !active {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (m *Manager) readLog(offset int, remainder string, flush bool) (int, string) {
	data, err := os.ReadFile(m.logPath)
	if err != nil || len(data) < offset {
		return offset, remainder
	}
	chunk := remainder + string(data[offset:])
	offset = len(data)
	lines := strings.Split(chunk, "\n")
	remainder = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		if strings.TrimSpace(line) != "" {
			m.logs.Add("Info", "OpenConnect", line)
		}
	}
	if flush && strings.TrimSpace(remainder) != "" {
		m.logs.Add("Info", "OpenConnect", remainder)
		remainder = ""
	}
	return offset, remainder
}
func (m *Manager) waitForApplied(start time.Time, profile profiles.Profile, after time.Time) {
	timeout := 60 * time.Second
	if !after.IsZero() {
		timeout = 5 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(m.statePath)
		if err == nil {
			var state scriptState
			if json.Unmarshal(b, &state) == nil && state.TunnelDevice != "" && (state.Reason == "connect" || state.Reason == "reconnect") &&
				(after.IsZero() || state.Reason == "reconnect" && state.UpdatedAt.After(after)) {
				m.mu.RLock()
				active := m.status.StartedAt != nil && m.status.StartedAt.Equal(start) && m.status.State == "connecting"
				m.mu.RUnlock()
				if !active {
					return
				}
				for _, route := range state.Routes {
					m.routes.AddServerWithSource(route.CIDR, route.Source)
				}
				m.applySavedRoutes(profile)
				m.mu.Lock()
				if m.status.StartedAt != nil && m.status.StartedAt.Equal(start) && m.status.State == "connecting" {
					m.status.State = "connected"
				}
				m.mu.Unlock()
				m.logs.Add("Info", "Routing", "server routes applied; user overrides may now be changed")
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	m.mu.Lock()
	if m.status.StartedAt != nil && m.status.StartedAt.Equal(start) && m.status.State == "connecting" {
		m.status.State = "error"
		m.status.LastError = "timed out waiting for vpnc-script to apply network configuration"
		if !after.IsZero() {
			m.status.LastError = "timed out waiting for VPN reconnection"
		}
	}
	m.mu.Unlock()
}
func (m *Manager) ReconnectAfterWake() error {
	m.mu.RLock()
	if m.status.State != "connected" || m.status.StartedAt == nil {
		m.mu.RUnlock()
		return nil
	}
	start, profile := *m.status.StartedAt, m.profile
	m.mu.RUnlock()
	requestedAt := time.Now().UTC()
	if err := m.helper.Do(privileged.Request{Operation: "reconnect"}); err != nil {
		return err
	}
	m.mu.Lock()
	if m.status.StartedAt == nil || !m.status.StartedAt.Equal(start) || m.status.State != "connected" {
		m.mu.Unlock()
		return nil
	}
	m.status.State, m.status.Traffic = "connecting", nil
	m.mu.Unlock()
	m.routes.ClearServer()
	m.logs.Add("Info", "OpenConnect", "Mac woke from sleep; VPN reconnect requested")
	go m.waitForApplied(start, profile, requestedAt)
	return nil
}
func (m *Manager) applySavedRoutes(profile profiles.Profile) {
	for _, cidr := range profile.RouteDeletions {
		if route, found := m.routes.GetByCIDR(cidr); found {
			if err := m.DeleteRoute(route.CIDR); err != nil {
				m.logs.Add("Warning", "Routing", "could not restore route deletion "+route.CIDR+": "+err.Error())
				continue
			}
			_ = m.routes.Delete(route.ID)
			m.logs.Add("Info", "Routing", "restored route deletion "+route.CIDR)
		}
	}
	for _, cidr := range profile.RouteAdditions {
		if _, found := m.routes.GetByCIDR(cidr); found {
			continue
		}
		route, err := m.routes.Add(cidr)
		if err != nil {
			m.logs.Add("Warning", "Routing", "could not restore custom route "+cidr+": "+err.Error())
			continue
		}
		if err = m.AddRoute(route.CIDR); err != nil {
			_ = m.routes.Delete(route.ID)
			m.logs.Add("Warning", "Routing", "could not restore custom route "+route.CIDR+": "+err.Error())
			continue
		}
		m.logs.Add("Info", "Routing", "restored custom route "+route.CIDR)
	}
}
func (m *Manager) Disconnect() error {
	if err := m.helper.Do(privileged.Request{Operation: "disconnect"}); err != nil {
		return err
	}
	m.mu.Lock()
	startedAt := m.status.StartedAt
	m.status.State = "disconnecting"
	m.mu.Unlock()
	m.logs.Add("Info", "OpenConnect", "disconnect requested through root helper")
	go m.finishDisconnect(startedAt)
	return nil
}

func (m *Manager) Shutdown(timeout time.Duration) error {
	if err := m.helper.Do(privileged.Request{Operation: "disconnect"}); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := m.helper.Query(privileged.Request{Operation: "status"})
		if err == nil && response.Running != nil && !*response.Running {
			if m.statePath == "" {
				return nil
			}
			if _, stateErr := os.Stat(m.statePath); errors.Is(stateErr, os.ErrNotExist) {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("timed out waiting for OpenConnect and network cleanup to finish")
}

func (m *Manager) finishDisconnect(startedAt *time.Time) {
	// The hook removes its state file only after the original vpnc-script has
	// finished deleting routes and restoring the network configuration.
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(m.statePath); errors.Is(err, os.ErrNotExist) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Give the log tailer one final cycle to publish vpnc-script's delete lines.
	time.Sleep(400 * time.Millisecond)
	m.mu.Lock()
	if m.status.State != "disconnecting" || !sameStart(m.status.StartedAt, startedAt) {
		m.mu.Unlock()
		return
	}
	m.status = Status{State: "disconnected"}
	m.mu.Unlock()
	m.routes.Clear()
	m.logs.Add("Info", "Routing", "VPN session routes cleared after vpnc-script cleanup")
}

func sameStart(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
func (m *Manager) Helper() privileged.Client { return m.helper }
func (m *Manager) AddRoute(cidr string) error {
	return m.helper.Do(privileged.Request{Operation: "route.add", Route: &privileged.RouteRequest{CIDR: cidr}})
}
func (m *Manager) DeleteRoute(cidr string) error {
	return m.helper.Do(privileged.Request{Operation: "route.delete", Route: &privileged.RouteRequest{CIDR: cidr}})
}
func (m *Manager) ReplaceRoute(previous, cidr string) error {
	return m.helper.Do(privileged.Request{Operation: "route.replace", Route: &privileged.RouteRequest{CIDR: cidr, PreviousCIDR: previous}})
}
