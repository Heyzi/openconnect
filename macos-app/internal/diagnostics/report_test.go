package diagnostics

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/openconnect"
	"openconnect.local/desktop/internal/profiles"
)

func TestReportRedactsKnownSecrets(t *testing.T) {
	dir := t.TempDir()
	ps, _ := profiles.NewStore(filepath.Join(dir, "p.json"))
	_, _ = ps.Save(profiles.Profile{Name: "Work", Server: "https://vpn.example"})
	ns := network.NewStore()
	logs := logging.New(10)
	logs.Add("Info", "test", "password=topsecret token=verysecret")
	var out bytes.Buffer
	if e := Write(&out, "test", ps, ns, logs, openconnect.Status{}); e != nil {
		t.Fatal(e)
	}
	raw := out.String()
	if strings.Contains(raw, "topsecret") || strings.Contains(raw, "verysecret") {
		t.Fatal("diagnostic archive contains a secret")
	}
}
