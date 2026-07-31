package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"openconnect.local/desktop/internal/privileged"
)

func main() {
	var server privileged.Server
	flag.StringVar(&server.Socket, "socket", privileged.DefaultSocket, "Unix socket path")
	flag.StringVar(&server.OpenConnect, "openconnect", "", "OpenConnect binary")
	flag.StringVar(&server.Hook, "hook", "", "post-vpnc-script hook")
	flag.StringVar(&server.VPNCScript, "vpnc-script", "", "real vpnc-script")
	flag.StringVar(&server.StatePath, "state", "/var/run/openconnect-desktop-routes.json", "route state path")
	flag.StringVar(&server.LogPath, "log", "/var/run/openconnect-desktop-openconnect.log", "OpenConnect runtime log")
	flag.StringVar(&server.CaptureDir, "capture-dir", "", "directory for captured server payloads")
	flag.StringVar(&server.ConfigID, "config-id", "", "bundled component identity")
	flag.IntVar(&server.OwnerUID, "owner-uid", -1, "UID allowed to connect")
	flag.Parse()
	if server.OpenConnect == "" {
		executable, err := os.Executable()
		if err != nil {
			fatal(err)
		}
		contents := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", ".."))
		resources := filepath.Join(contents, "Resources")
		server.OpenConnect = filepath.Join(resources, "bin", "openconnect")
		server.Hook = filepath.Join(resources, "bin", "openconnect-script-hook")
		server.VPNCScript = filepath.Join(resources, "vpnc-script")
		server.OwnerUID = consoleUID()
		server.CaptureDir = filepath.Join(os.TempDir(), fmt.Sprintf("openconnect-desktop-csd-%d", server.OwnerUID))
		server.ConfigID, err = privileged.ComponentID(executable, server.OpenConnect, server.Hook, server.VPNCScript)
		if err != nil {
			fatal(err)
		}
	}
	if server.OwnerUID < 0 || server.OpenConnect == "" || server.Hook == "" || server.VPNCScript == "" || server.ConfigID == "" || server.CaptureDir == "" {
		fmt.Fprintln(os.Stderr, "missing required helper arguments")
		os.Exit(64)
	}
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func consoleUID() int {
	var stat syscall.Stat_t
	if err := syscall.Stat("/dev/console", &stat); err != nil {
		return -1
	}
	return int(stat.Uid)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
