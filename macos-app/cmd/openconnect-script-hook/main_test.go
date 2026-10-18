package main

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestReconnectClearsStaleNetworkBeforeRetry(t *testing.T) {
	configured, staleGateway := true, true
	teardowns, setups, attempts := 0, 0, 0
	run := func(reason string) error {
		switch reason {
		case "disconnect":
			if !configured {
				t.Fatal("repeated teardown during retries")
			}
			configured, staleGateway = false, false
			teardowns++
		case "attempt-reconnect":
			if staleGateway {
				t.Fatal("TLS retry would use the stale gateway route")
			}
			attempts++
		case "connect":
			if configured {
				t.Fatal("connect would overwrite the original network backup")
			}
			configured = true
			setups++
		}
		return nil
	}
	previous := "connect"
	for _, reason := range []string{"attempt-reconnect", "attempt-reconnect", "reconnect", "reconnect"} {
		if err := runNetworkTransition(reason, previous, run); err != nil {
			t.Fatal(err)
		}
		previous = reason
	}
	if !configured || teardowns != 1 || setups != 1 || attempts != 2 {
		t.Fatalf("configured=%v teardown=%d setup=%d attempts=%d", configured, teardowns, setups, attempts)
	}
}

func TestReconnectStopsOnTeardownFailure(t *testing.T) {
	want := errors.New("network cleanup failed")
	calls := 0
	err := runNetworkTransition("attempt-reconnect", "connect", func(reason string) error {
		calls++
		if reason != "disconnect" {
			t.Fatalf("ran %s despite failed cleanup", reason)
		}
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
}

func TestParseDNSServers(t *testing.T) {
	got := parseDNSServers("10.20.30.40\n2001:db8::53\n")
	want := []string{"10.20.30.40", "2001:db8::53"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDNSServers() = %#v, want %#v", got, want)
	}
}

func TestReconnectPreservesOriginalDNSBaseline(t *testing.T) {
	captured := time.Now().UTC()
	baseline := State{
		AppliedAt: captured,
		DNS:       []DNSState{{Service: "Wi-Fi", Servers: []string{"1.1.1.1"}}},
		Proxies:   []ProxyState{{Service: "Wi-Fi", URL: "http://proxy.example/pac", Enabled: true}},
	}
	got := runtimeState(baseline, "reconnect", "utun7", "vpn.example", "", nil)
	if !reflect.DeepEqual(got.DNS, baseline.DNS) || !reflect.DeepEqual(got.Proxies, baseline.Proxies) {
		t.Fatalf("reconnect replaced immutable baseline: %#v", got)
	}
	if !got.AppliedAt.Equal(captured) {
		t.Fatalf("reconnect changed capture time: %s", got.AppliedAt)
	}
}

func TestParseDNSServersRecognizesAutomaticDNS(t *testing.T) {
	got := parseDNSServers("There aren't any DNS Servers set on Wi-Fi.\n")
	if len(got) != 0 {
		t.Fatalf("automatic DNS parsed as %#v", got)
	}
}
