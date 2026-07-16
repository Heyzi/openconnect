package main

import (
	"flag"
	"fmt"
	"os"

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
	flag.StringVar(&server.ConfigID, "config-id", "", "bundled component identity")
	flag.IntVar(&server.OwnerUID, "owner-uid", -1, "UID allowed to connect")
	flag.Parse()
	if server.OwnerUID < 0 || server.OpenConnect == "" || server.Hook == "" || server.VPNCScript == "" || server.ConfigID == "" {
		fmt.Fprintln(os.Stderr, "missing required helper arguments")
		os.Exit(64)
	}
	if err := server.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
