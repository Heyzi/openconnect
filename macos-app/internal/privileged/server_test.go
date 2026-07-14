package privileged

import (
	"reflect"
	"testing"
)

func TestVerboseOpenConnectArguments(t *testing.T) {
	in := &ConnectRequest{Protocol: "anyconnect", Verbose: true, MACAddress: "02:00:00:00:00:01", Server: "https://vpn.example"}
	hook := "/Applications/OpenConnect Desktop.app/Contents/Resources/bin/openconnect-script-hook"
	args := (&Server{Hook: hook}).openConnectArgs(in)
	want := []string{"--protocol", "anyconnect", "--passwd-on-stdin", "--script", "'/Applications/OpenConnect Desktop.app/Contents/Resources/bin/openconnect-script-hook'", "--dump-http-traffic", "-vvv", "--mac-address", "02:00:00:00:00:01", "https://vpn.example"}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("arguments = %#v, want %#v", args, want)
	}
}

func TestShellQuoteEscapesApostrophe(t *testing.T) {
	if got, want := shellQuote("/tmp/user's hook"), "'/tmp/user'\\''s hook'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}
