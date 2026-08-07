package api

import (
	"strings"
	"testing"

	"openconnect.local/desktop/internal/profiles"
)

func TestDiagnoseUsesPinnedIPInsteadOfDNSWhileConnected(t *testing.T) {
	profile := profiles.Profile{Server: "https://vpn.invalid.example"}
	report := diagnose(profile, "connected", 1, "/nonexistent", "127.0.0.1")
	var dns diagnosticCheck
	for _, check := range report.Checks {
		if check.ID == "dns" {
			dns = check
		}
	}
	if dns.Status != "pass" {
		t.Fatalf("dns status = %q, want pass", dns.Status)
	}
	if !strings.Contains(dns.Detail, "127.0.0.1") {
		t.Fatalf("dns detail = %q, want pinned IP", dns.Detail)
	}
}
