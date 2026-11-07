package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"openconnect.local/desktop/internal/privileged"
)

type Route struct {
	CIDR   string `json:"cidr"`
	Source string `json:"source"`
}
type State struct {
	AppliedAt    time.Time    `json:"appliedAt"`
	UpdatedAt    time.Time    `json:"updatedAt"`
	Reason       string       `json:"reason"`
	TunnelDevice string       `json:"tunnelDevice"`
	Gateway      string       `json:"gateway"`
	ProxyPAC     string       `json:"proxyPAC,omitempty"`
	Routes       []Route      `json:"routes"`
	Proxies      []ProxyState `json:"proxies,omitempty"`
	DNS          []DNSState   `json:"dns,omitempty"`
}
type ProxyState struct {
	Service string `json:"service"`
	URL     string `json:"url,omitempty"`
	Enabled bool   `json:"enabled"`
}
type DNSState struct {
	Service string   `json:"service"`
	Servers []string `json:"servers,omitempty"`
}

func main() {
	script := os.Getenv("OPENCONNECT_REAL_VPNC_SCRIPT")
	statePath := os.Getenv("OPENCONNECT_ROUTE_STATE")
	if !filepath.IsAbs(script) || !filepath.IsAbs(statePath) {
		fatal("hook paths must be absolute")
	}
	reason := os.Getenv("reason")
	var previous State
	if b, readErr := os.ReadFile(statePath); readErr == nil {
		_ = json.Unmarshal(b, &previous)
	}
	if os.Getenv("OPENCONNECT_RECOVER_ONLY") == "1" {
		restoreSystemState(previous)
		_ = os.Remove(statePath)
		return
	}
	if reason != "disconnect" && reason != "pre-init" && previous.AppliedAt.IsZero() {
		names := services()
		previous = State{AppliedAt: time.Now().UTC(), TunnelDevice: os.Getenv("TUNDEV"), DNS: snapshotDNS(names), Proxies: snapshotProxies(names)}
		writeState(statePath, previous)
	}
	if err := runNetworkTransition(reason, previous.Reason, func(step string) error {
		cmd := exec.Command(script)
		cmd.Env = append(os.Environ(), "reason="+step)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return err
		}
		if step == "disconnect" && reason != "disconnect" {
			restoreSystemState(previous)
		}
		return nil
	}); err != nil {
		if reason != "disconnect" && reason != "pre-init" {
			restoreSystemState(previous)
			_ = os.Remove(statePath)
		}
		fatal("vpnc-script failed: " + err.Error())
	}
	if reason == "disconnect" {
		restoreSystemState(previous)
		_ = os.Remove(statePath)
		return
	}
	if reason == "pre-init" {
		return
	}
	state := runtimeState(previous, reason, os.Getenv("TUNDEV"), os.Getenv("VPNGATEWAY"), os.Getenv("CISCO_PROXY_PAC"), routes())
	if !privileged.ValidTunnelDevice(state.TunnelDevice) {
		fatal("vpnc-script did not provide a valid utun device")
	}
	writeState(statePath, state)
	if (reason == "connect" || reason == "reconnect") && state.ProxyPAC != "" {
		applyProxyPAC(state.Proxies, state.ProxyPAC)
	}
}

func runNetworkTransition(reason, previousReason string, run func(string) error) error {
	// Darwin's attempt-reconnect only adds a gateway route. EEXIST leaves
	// stale routing in place. Tear down once, before TLS, keeping the process
	// and session cookie alive. Repeated retries must not repeat teardown.
	if reason == "attempt-reconnect" && previousReason != "attempt-reconnect" {
		if err := run("disconnect"); err != nil {
			return err
		}
	}
	// Reapply only what we tore down. An unsolicited reconnect must not
	// overwrite vpnc-script's original default-route backup with VPN routes.
	if reason == "reconnect" && previousReason == "attempt-reconnect" {
		return run("connect")
	}
	return run(reason)
}

func runtimeState(baseline State, reason, device, gateway, proxyPAC string, currentRoutes []Route) State {
	return State{AppliedAt: baseline.AppliedAt, UpdatedAt: time.Now().UTC(), Reason: reason, TunnelDevice: device, Gateway: gateway, ProxyPAC: proxyPAC, Routes: currentRoutes, Proxies: baseline.Proxies, DNS: baseline.DNS}
}
func writeState(statePath string, state State) {
	b, err := json.Marshal(state)
	if err != nil {
		fatal(err.Error())
	}
	tmp := statePath + ".tmp"
	// Stays root-owned: root replays this file during recovery. World-readable
	// so the agent can follow it; it holds no secrets.
	if err = os.WriteFile(tmp, append(b, '\n'), 0644); err != nil {
		fatal(err.Error())
	}
	if err = os.Rename(tmp, statePath); err != nil {
		fatal(err.Error())
	}
}
func routes() []Route {
	var out []Route
	for _, kind := range []struct{ count, prefix, source string }{{"CISCO_SPLIT_INC", "CISCO_SPLIT_INC_", "server-include"}, {"CISCO_SPLIT_EXC", "CISCO_SPLIT_EXC_", "server-exclude"}} {
		count, _ := strconv.Atoi(os.Getenv(kind.count))
		for i := 0; i < count; i++ {
			base := kind.prefix + strconv.Itoa(i)
			addr := os.Getenv(base + "_ADDR")
			mask := os.Getenv(base + "_MASKLEN")
			if ip := net.ParseIP(addr); ip != nil && mask != "" {
				out = append(out, Route{CIDR: addr + "/" + mask, Source: kind.source})
			}
		}
	}
	return out
}

var urlPattern = regexp.MustCompile(`(?m)^URL:\s*(.*)$`)
var enabledPattern = regexp.MustCompile(`(?m)^Enabled:\s*Yes$`)

func services() []string {
	b, e := exec.Command("/usr/sbin/networksetup", "-listallnetworkservices").Output()
	if e != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "An asterisk") {
			continue
		}
		out = append(out, strings.TrimPrefix(line, "*"))
	}
	return out
}

// queryServices runs one read-only networksetup query per service concurrently,
// keeping service order and dropping services whose query failed.
func queryServices[T any](services []string, flag string, parse func(string, []byte) T) []T {
	results := make([]*T, len(services))
	var wg sync.WaitGroup
	for i, service := range services {
		wg.Add(1)
		go func(i int, service string) {
			defer wg.Done()
			if b, e := exec.Command("/usr/sbin/networksetup", flag, service).Output(); e == nil {
				value := parse(service, b)
				results[i] = &value
			}
		}(i, service)
	}
	wg.Wait()
	var out []T
	for _, result := range results {
		if result != nil {
			out = append(out, *result)
		}
	}
	return out
}
func snapshotProxies(services []string) []ProxyState {
	return queryServices(services, "-getautoproxyurl", func(service string, b []byte) ProxyState {
		state := ProxyState{Service: service, Enabled: enabledPattern.Match(b)}
		if match := urlPattern.FindSubmatch(b); len(match) == 2 && string(match[1]) != "(null)" {
			state.URL = strings.TrimSpace(string(match[1]))
		}
		return state
	})
}
func applyProxyPAC(states []ProxyState, pac string) {
	for _, state := range states {
		if exec.Command("/usr/sbin/networksetup", "-setautoproxyurl", state.Service, pac).Run() == nil {
			_ = exec.Command("/usr/sbin/networksetup", "-setautoproxystate", state.Service, "on").Run()
		}
	}
}
func restoreProxies(states []ProxyState) {
	for _, state := range states {
		if state.URL != "" {
			_ = exec.Command("/usr/sbin/networksetup", "-setautoproxyurl", state.Service, state.URL).Run()
		}
		value := "off"
		if state.Enabled {
			value = "on"
		}
		_ = exec.Command("/usr/sbin/networksetup", "-setautoproxystate", state.Service, value).Run()
	}
}
func snapshotDNS(services []string) []DNSState {
	return queryServices(services, "-getdnsservers", func(service string, b []byte) DNSState {
		return DNSState{Service: service, Servers: parseDNSServers(string(b))}
	})
}
func parseDNSServers(output string) []string {
	var servers []string
	for _, line := range strings.Split(output, "\n") {
		value := strings.TrimSpace(line)
		if net.ParseIP(value) != nil {
			servers = append(servers, value)
		}
	}
	return servers
}
func restoreDNS(states []DNSState) {
	for _, state := range states {
		args := []string{"-setdnsservers", state.Service}
		if len(state.Servers) == 0 {
			args = append(args, "Empty")
		} else {
			args = append(args, state.Servers...)
		}
		_ = exec.Command("/usr/sbin/networksetup", args...).Run()
	}
}
func removeTunnelDNS(device string) {
	if !privileged.ValidTunnelDevice(device) {
		return
	}
	commands := "open\nremove State:/Network/Service/" + device + "/DNS\nremove State:/Network/Service/" + device + "/IPv4\nclose\n"
	cmd := exec.Command("/usr/sbin/scutil")
	cmd.Stdin = strings.NewReader(commands)
	_ = cmd.Run()
}
func restoreSystemState(state State) {
	restoreDNS(state.DNS)
	restoreProxies(state.Proxies)
	removeTunnelDNS(state.TunnelDevice)
	_ = exec.Command("/usr/bin/dscacheutil", "-flushcache").Run()
	_ = exec.Command("/usr/bin/killall", "-HUP", "mDNSResponder").Run()
}
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
