package observability

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/openconnect"
)

func TestBuildConnectionStages(t *testing.T) {
	start := time.Now().UTC()
	entries := []logging.Entry{
		{Time: start.Add(time.Second), Message: "Connected to 192.0.2.1:443"},
		{Time: start.Add(2 * time.Second), Message: "Connected to HTTPS with ciphersuite TLS"},
		{Time: start.Add(3 * time.Second), Message: "Got CONNECT response: HTTP/1.1 200 CONNECTED"},
		{Time: start.Add(4 * time.Second), Message: "CSTP connected"},
		{Time: start.Add(5 * time.Second), Message: "Established DTLS connection"},
		{Time: start.Add(6 * time.Second), Message: "server routes applied"},
	}
	report := Build(openconnect.Status{State: "connected", StartedAt: &start}, entries, filepath.Join(t.TempDir(), "missing"))
	for _, id := range []string{"network", "tls", "auth", "tunnel", "udp", "config"} {
		for _, stage := range report.Stages {
			if stage.ID == id && stage.Status != "complete" {
				t.Fatalf("stage %s = %#v", id, stage)
			}
		}
	}
	if report.Posture.Status != "not-requested" {
		t.Fatalf("posture = %#v", report.Posture)
	}
}

func TestPosturePayloadMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "csd-payload.bin")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	report := Build(openconnect.Status{State: "connected"}, nil, path)
	if !report.Posture.PayloadReceived || report.Posture.Size != 7 || len(report.Posture.SHA256) != 64 {
		t.Fatalf("posture = %#v", report.Posture)
	}
}
