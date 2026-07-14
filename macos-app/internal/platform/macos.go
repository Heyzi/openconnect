package platform

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func OpenBrowser(url string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return exec.Command("open", url).Start()
}

func EnsureHelper(socket string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if helperCompatible(socket) {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	resources := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "Resources"))
	bin := filepath.Join(resources, "bin")
	paths := []string{filepath.Join(bin, "openconnect-helper"), filepath.Join(bin, "openconnect"), filepath.Join(bin, "openconnect-script-hook"), filepath.Join(resources, "vpnc-script")}
	for _, path := range paths {
		if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
			return fmt.Errorf("required bundled component is missing: %s", path)
		}
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	command := fmt.Sprintf("/bin/launchctl remove org.openconnect.desktop.helper >/dev/null 2>&1; /bin/launchctl submit -l org.openconnect.desktop.helper -o /var/log/openconnect-desktop-helper.log -e /var/log/openconnect-desktop-helper.log -- %s --socket %s --owner-uid %d --openconnect %s --hook %s --vpnc-script %s --log /var/run/openconnect-desktop-openconnect.log", quote(paths[0]), quote(socket), os.Getuid(), quote(paths[1]), quote(paths[2]), quote(paths[3]))
	appleScript := "do shell script " + strconv.Quote(command) + " with administrator privileges"
	if output, runErr := exec.Command("/usr/bin/osascript", "-e", appleScript).CombinedOutput(); runErr != nil {
		return fmt.Errorf("administrator authorization failed: %s", strings.TrimSpace(string(output)))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if conn, dialErr := net.DialTimeout("unix", socket, 150*time.Millisecond); dialErr == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("privileged helper did not start")
}

func helperCompatible(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
	if err != nil {
		return false
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	if json.NewEncoder(conn).Encode(map[string]any{"operation": "version", "protocolVersion": 2}) != nil {
		return false
	}
	var response struct {
		OK bool `json:"ok"`
	}
	return json.NewDecoder(conn).Decode(&response) == nil && response.OK
}

func RunTray(url, statusPath string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	tray := filepath.Clean(filepath.Join(filepath.Dir(executable), "..", "Resources", "OpenConnect Tray.app", "Contents", "MacOS", "openconnect-tray"))
	if _, err := os.Stat(tray); err != nil {
		return fmt.Errorf("tray component is missing: %w", err)
	}
	cmd := exec.Command(tray, url, fmt.Sprint(os.Getpid()), statusPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tray exited: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return errors.New("tray exited unexpectedly")
}
