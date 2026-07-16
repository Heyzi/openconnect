package main

import (
	"reflect"
	"testing"
	"time"
)

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
