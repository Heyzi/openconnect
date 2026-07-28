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
	"time"
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
		previous = State{AppliedAt: time.Now().UTC(), TunnelDevice: os.Getenv("TUNDEV"), DNS: snapshotDNS(), Proxies: snapshotProxies()}
		writeState(statePath, previous)
	}
	cmd := exec.Command(script)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
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
	if !strings.HasPrefix(state.TunnelDevice, "utun") {
		fatal("vpnc-script did not provide a valid utun device")
	}
	writeState(statePath, state)
	if state.ProxyPAC != "" {
		applyProxyPAC(state.Proxies, state.ProxyPAC)
	}
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
	if err = os.WriteFile(tmp, append(b, '\n'), 0600); err != nil {
		fatal(err.Error())
	}
	if err = os.Rename(tmp, statePath); err != nil {
		fatal(err.Error())
	}
	if uid, conversionErr := strconv.Atoi(os.Getenv("OPENCONNECT_OWNER_UID")); conversionErr == nil {
		_ = os.Chown(statePath, uid, -1)
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
func snapshotProxies() []ProxyState {
	var out []ProxyState
	for _, service := range services() {
		b, e := exec.Command("/usr/sbin/networksetup", "-getautoproxyurl", service).Output()
		if e != nil {
			continue
		}
		state := ProxyState{Service: service, Enabled: enabledPattern.Match(b)}
		if match := urlPattern.FindSubmatch(b); len(match) == 2 && string(match[1]) != "(null)" {
			state.URL = strings.TrimSpace(string(match[1]))
		}
		out = append(out, state)
	}
	return out
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
func snapshotDNS() []DNSState {
	var out []DNSState
	for _, service := range services() {
		b, e := exec.Command("/usr/sbin/networksetup", "-getdnsservers", service).Output()
		if e != nil {
			continue
		}
		state := DNSState{Service: service, Servers: parseDNSServers(string(b))}
		out = append(out, state)
	}
	return out
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
	if !strings.HasPrefix(device, "utun") {
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
