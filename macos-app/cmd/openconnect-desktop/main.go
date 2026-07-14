package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"openconnect.local/desktop/internal/api"
	"openconnect.local/desktop/internal/keychain"
	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/openconnect"
	"openconnect.local/desktop/internal/platform"
	"openconnect.local/desktop/internal/profiles"
)

func main() {
	var helperSocket string
	var statePath string
	var logPath string
	var noBrowser bool
	flag.StringVar(&helperSocket, "helper-socket", "/var/run/openconnect-desktop.sock", "privileged helper socket")
	flag.StringVar(&statePath, "route-state", "/var/run/openconnect-desktop-routes.json", "post-vpnc-script state")
	flag.StringVar(&logPath, "openconnect-log", "/var/run/openconnect-desktop-openconnect.log", "OpenConnect runtime log")
	flag.BoolVar(&noBrowser, "no-browser", false, "do not open the portal automatically")
	flag.Parse()
	home, err := os.UserHomeDir()
	fatal(err)
	data := filepath.Join(home, "Library", "Application Support", "OpenConnect Desktop")
	if err = os.MkdirAll(data, 0700); err != nil {
		fatal(err)
	}
	instanceFile, primary, err := acquireInstance(filepath.Join(data, "instance.lock"))
	fatal(err)
	if !primary {
		if existingURL, readErr := os.ReadFile(filepath.Join(data, "instance.lock")); readErr == nil && strings.TrimSpace(string(existingURL)) != "" {
			_ = platform.OpenBrowser(strings.TrimSpace(string(existingURL)))
		}
		return
	}
	defer instanceFile.Close()
	ps, err := profiles.NewStore(filepath.Join(data, "profiles.json"))
	fatal(err)
	ns := network.NewStore()
	logs := logging.New(2000)
	if helperErr := platform.EnsureHelper(helperSocket); helperErr != nil {
		logs.Add("Error", "Privileged Helper", helperErr.Error())
	}
	vpn := openconnect.New(helperSocket, statePath, logPath, logs, ns)
	executable, _ := os.Executable()
	keys := keychain.Store{Binary: filepath.Join(filepath.Dir(executable), "openconnect-keychain")}
	server := api.New(ps, ns, keys, vpn, logs)
	listener, err := server.Listen()
	fatal(err)
	bootstrapURL := server.BootstrapURL()
	portalURL := server.PortalURL()
	_ = instanceFile.Truncate(0)
	_, _ = instanceFile.Seek(0, 0)
	_, _ = instanceFile.WriteString(portalURL)
	_ = instanceFile.Sync()
	statusPath := filepath.Join(data, "status.json")
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if encoded, encodeErr := json.Marshal(vpn.Status()); encodeErr == nil {
				_ = os.WriteFile(statusPath, encoded, 0600)
			}
		}
	}()
	logs.Add("Info", "Desktop Agent", "portal listening on loopback")
	fmt.Println(bootstrapURL)
	go func() {
		for {
			if trayErr := platform.RunTray(portalURL, statusPath); trayErr != nil {
				logs.Add("Warning", "Tray", trayErr.Error()+"; restarting")
			}
			time.Sleep(2 * time.Second)
		}
	}()
	if !noBrowser {
		if err := platform.OpenBrowser(bootstrapURL); err != nil {
			logs.Add("Warning", "Desktop Agent", err.Error())
		}
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fatal(err)
		}
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = vpn.Disconnect()
}
func acquireInstance(path string) (*os.File, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return file, true, nil
}
func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
