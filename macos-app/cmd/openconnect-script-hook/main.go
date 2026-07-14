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
	Reason       string       `json:"reason"`
	TunnelDevice string       `json:"tunnelDevice"`
	Gateway      string       `json:"gateway"`
	ProxyPAC     string       `json:"proxyPAC,omitempty"`
	Routes       []Route      `json:"routes"`
	Proxies      []ProxyState `json:"proxies,omitempty"`
}
type ProxyState struct {
	Service string `json:"service"`
	URL     string `json:"url,omitempty"`
	Enabled bool   `json:"enabled"`
}

func main() {
	script := os.Getenv("OPENCONNECT_REAL_VPNC_SCRIPT")
	statePath := os.Getenv("OPENCONNECT_ROUTE_STATE")
	if !filepath.IsAbs(script) || !filepath.IsAbs(statePath) {
		fatal("hook paths must be absolute")
	}
	cmd := exec.Command(script)
	reason := os.Getenv("reason")
	var previous State
	if reason == "disconnect" {
		if b, readErr := os.ReadFile(statePath); readErr == nil {
			_ = json.Unmarshal(b, &previous)
		}
	}
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal("vpnc-script failed: " + err.Error())
	}
	if reason == "disconnect" || reason == "pre-init" {
		if reason == "disconnect" {
			restoreProxies(previous.Proxies)
		}
		_ = os.Remove(statePath)
		return
	}
	state := State{AppliedAt: time.Now().UTC(), Reason: reason, TunnelDevice: os.Getenv("TUNDEV"), Gateway: os.Getenv("VPNGATEWAY"), ProxyPAC: os.Getenv("CISCO_PROXY_PAC"), Routes: routes(), Proxies: snapshotProxies()}
	if !strings.HasPrefix(state.TunnelDevice, "utun") {
		fatal("vpnc-script did not provide a valid utun device")
	}
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
	if state.ProxyPAC != "" {
		applyProxyPAC(state.Proxies, state.ProxyPAC)
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
func fatal(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
