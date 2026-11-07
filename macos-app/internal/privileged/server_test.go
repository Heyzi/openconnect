package privileged

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestVerboseOpenConnectArguments(t *testing.T) {
	in := &ConnectRequest{Protocol: "anyconnect", Verbose: true, MACAddress: "02:00:00:00:00:01", Server: "https://vpn.example"}
	hook := "/Applications/OpenConnect Desktop.app/Contents/Resources/bin/openconnect-script-hook"
	args := (&Server{Hook: hook}).openConnectArgs(in)
	want := []string{"--protocol", "anyconnect", "--passwd-on-stdin", "--script", "'/Applications/OpenConnect Desktop.app/Contents/Resources/bin/openconnect-script-hook'", "--useragent", "Open AnyConnect VPN Agent", "--compression", "none", "--reconnect-timeout", "3600", "--send-device-id", "--dump-http-traffic", "-vvv", "--mac-address", "02:00:00:00:00:01", "https://vpn.example"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("arguments = %#v, want %#v", args, want)
	}
}

func TestCaptureServerScriptsArgument(t *testing.T) {
	s := Server{CaptureDir: "/tmp/openconnect-desktop-csd-501"}
	in := &ConnectRequest{Protocol: "anyconnect", Server: "https://vpn.example", SaveServerScripts: true}
	want := "--csd-save /tmp/openconnect-desktop-csd-501/csd-payload.bin"
	if got := strings.Join(s.openConnectArgs(in), " "); !strings.Contains(got, want) {
		t.Fatalf("arguments %q do not contain %q", got, want)
	}
}

func TestRecoverDoesNotChangeNetworkWhileOpenConnectIsRunning(t *testing.T) {
	server := &Server{cmd: &exec.Cmd{}}
	response := Response{OK: true}
	if err := server.execute(Request{Operation: "recover"}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Recovered == nil || *response.Recovered {
		t.Fatalf("Recovered = %#v, want false", response.Recovered)
	}
}

func TestRecoverReportsNoStaleState(t *testing.T) {
	server := &Server{StatePath: t.TempDir() + "/missing.json"}
	response := Response{OK: true}
	if err := server.execute(Request{Operation: "recover"}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Recovered == nil || *response.Recovered {
		t.Fatalf("Recovered = %#v, want false", response.Recovered)
	}
}

func TestShellQuoteEscapesApostrophe(t *testing.T) {
	if got, want := shellQuote("/tmp/user's hook"), "'/tmp/user'\\''s hook'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestStatusReportsWhetherOpenConnectIsRunning(t *testing.T) {
	server := &Server{lastExit: "exit status 1"}
	response := Response{OK: true}
	if err := server.execute(Request{Operation: "status"}, &response); err != nil {
		t.Fatal(err)
	}
	if response.Running == nil || *response.Running {
		t.Fatalf("Running = %#v, want false", response.Running)
	}
	if response.LastExit != "exit status 1" {
		t.Fatalf("LastExit = %q", response.LastExit)
	}
}

func TestRouteScope(t *testing.T) {
	cases := map[string]string{
		"176.109.96.27/32":       "-host",
		"64:ff9b::b06d:601b/128": "-host",
		"10.0.0.0/24":            "-net",
		"fd00::/64":              "-net",
		"not-a-cidr":             "-net",
	}
	for cidr, want := range cases {
		if got := routeScope(cidr); got != want {
			t.Errorf("routeScope(%q) = %q, want %q", cidr, got, want)
		}
	}
}

func TestVersionReportsHelperConfiguration(t *testing.T) {
	server := &Server{ConfigID: "bundle-components"}
	response := Response{OK: true}
	if err := server.execute(Request{Operation: "version", ProtocolVersion: ProtocolVersion}, &response); err != nil {
		t.Fatal(err)
	}
	if response.ConfigID != "bundle-components" {
		t.Fatalf("ConfigID = %q", response.ConfigID)
	}
}

func TestRouteArgsSelectAddressFamily(t *testing.T) {
	if got := strings.Join(routeArgs("delete", "2001:db8::/32", "-net", "2001:db8::/32"), " "); got != "-n delete -inet6 -net 2001:db8::/32" {
		t.Fatal(got)
	}
	if got := strings.Join(routeArgs("add", "10.0.0.0/8", "-net", "10.0.0.0/8"), " "); got != "-n add -net 10.0.0.0/8" {
		t.Fatal(got)
	}
}

func TestValidTunnelDeviceRejectsScutilInjection(t *testing.T) {
	if !ValidTunnelDevice("utun12") || ValidTunnelDevice("utun0\nset x") || ValidTunnelDevice("utun") {
		t.Fatal("tunnel device validation is wrong")
	}
}

func TestVerifyComponentsDetectsSwappedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hook")
	_ = os.WriteFile(path, []byte("a"), 0700)
	id, _ := ComponentID(path)
	s := Server{Components: []string{path}, ConfigID: id}
	if e := s.verifyComponents(); e != nil {
		t.Fatal(e)
	}
	_ = os.WriteFile(path, []byte("b"), 0700)
	if s.verifyComponents() == nil {
		t.Fatal("swapped component accepted")
	}
}
