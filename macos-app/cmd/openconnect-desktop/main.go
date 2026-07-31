package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
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

var buildCommit = "unknown"

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
	captureDir := filepath.Join(os.TempDir(), fmt.Sprintf("openconnect-desktop-csd-%d", os.Getuid()))
	_ = os.RemoveAll(captureDir)
	if err = os.MkdirAll(captureDir, 0700); err != nil {
		fatal(err)
	}
	if err = os.MkdirAll(data, 0700); err != nil {
		fatal(err)
	}
	instancePath := filepath.Join(data, "instance.lock")
	executable, err := os.Executable()
	fatal(err)
	executable, err = filepath.Abs(executable)
	fatal(err)
	instanceFile, primary, existingURL, err := acquirePortableInstance(instancePath, executable)
	fatal(err)
	if !primary {
		if existingURL != "" {
			_ = platform.OpenBrowser(existingURL)
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
	if recovered, recoverErr := vpn.RecoverStaleSystemState(); recoverErr != nil {
		logs.Add("Warning", "Network Recovery", recoverErr.Error())
	} else if recovered {
		logs.Add("Info", "Network Recovery", "stale VPN DNS, proxy, and tunnel state restored")
	}
	keys := keychain.Store{Binary: filepath.Join(filepath.Dir(executable), "openconnect-keychain")}
	server := api.New(ps, ns, keys, vpn, logs, buildCommit)
	listener, err := server.Listen()
	fatal(err)
	bootstrapURL := server.BootstrapURL()
	fatal(writeInstanceInfo(instanceFile, instanceInfo{URL: bootstrapURL, Executable: executable, PID: os.Getpid()}))
	statusPath := filepath.Join(data, "status.json")
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var previous []byte
		for range ticker.C {
			if encoded, encodeErr := json.Marshal(vpn.Status()); encodeErr == nil {
				previous, _ = writeStatus(statusPath, previous, encoded)
			}
		}
	}()
	logs.Add("Info", "Desktop Agent", "portal listening on loopback")
	fmt.Println(bootstrapURL)
	go func() {
		for {
			if trayErr := platform.RunTray(bootstrapURL, statusPath, buildCommit); trayErr != nil {
				logs.Add("Warning", "Tray", trayErr.Error()+"; restarting")
			}
			time.Sleep(2 * time.Second)
		}
	}()
	wake := make(chan os.Signal, 1)
	signal.Notify(wake, syscall.SIGUSR1)
	go func() {
		for range wake {
			time.Sleep(2 * time.Second)
			if wakeErr := vpn.ReconnectAfterWake(); wakeErr != nil {
				logs.Add("Warning", "Wake Recovery", wakeErr.Error())
			}
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
	if shutdownErr := vpn.Shutdown(15 * time.Second); shutdownErr != nil {
		logs.Add("Error", "Shutdown", shutdownErr.Error())
		fmt.Fprintln(os.Stderr, shutdownErr)
	}
}

func writeStatus(path string, previous, encoded []byte) ([]byte, error) {
	if bytes.Equal(previous, encoded) {
		return previous, nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, encoded, 0600); err != nil {
		return previous, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return previous, err
	}
	return append(previous[:0], encoded...), nil
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

type instanceInfo struct {
	URL        string `json:"url"`
	Executable string `json:"executable,omitempty"`
	PID        int    `json:"pid,omitempty"`
}

// acquirePortableInstance replaces an instance that is still running from a
// different app bundle. This matters when a portable .app is moved or updated:
// the old helper otherwise retains paths into the previous bundle forever.
func acquirePortableInstance(path, executable string) (*os.File, bool, string, error) {
	file, primary, err := acquireInstance(path)
	if err != nil || primary {
		return file, primary, "", err
	}
	info, err := readInstanceInfo(path)
	if err != nil {
		return nil, false, "", err
	}
	ownerPID := info.PID
	ownerExecutable := info.Executable
	if ownerPID == 0 {
		ownerPID, _ = lockOwnerPID(path)
	}
	if ownerExecutable == "" && ownerPID > 0 {
		ownerExecutable, _ = processExecutable(ownerPID)
	}
	if ownerExecutable == "" || sameExecutable(ownerExecutable, executable) {
		return nil, false, info.URL, nil
	}
	if ownerPID <= 1 {
		return nil, false, "", fmt.Errorf("could not identify previous application instance")
	}
	if err = syscall.Kill(ownerPID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return nil, false, "", fmt.Errorf("could not stop previous application instance: %w", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		file, primary, err = acquireInstance(path)
		if err != nil || primary {
			return file, primary, "", err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, false, "", fmt.Errorf("previous application instance did not stop")
}

func sameExecutable(running, current string) bool {
	running = strings.TrimSpace(running)
	return running == current || strings.HasPrefix(running, current+" ")
}

func lockOwnerPID(path string) (int, error) {
	output, err := exec.Command("/usr/sbin/lsof", "-t", "--", path).Output()
	if err != nil {
		return 0, err
	}
	line := strings.Split(strings.TrimSpace(string(output)), "\n")[0]
	return strconv.Atoi(line)
}

func processExecutable(pid int) (string, error) {
	output, err := exec.Command("/bin/ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	return strings.TrimSpace(string(output)), err
}

func writeInstanceInfo(file *os.File, info instanceInfo) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if _, err = file.Write(encoded); err != nil {
		return err
	}
	return file.Sync()
}
func readInstanceInfo(path string) (instanceInfo, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return instanceInfo{}, err
	}
	var info instanceInfo
	if json.Unmarshal(contents, &info) == nil && info.URL != "" {
		return info, nil
	}
	// Compatibility with versions that stored only the bootstrap URL.
	info.URL = strings.TrimSpace(string(contents))
	if info.URL == "" {
		return instanceInfo{}, errors.New("empty application instance file")
	}
	return info, nil
}
func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
