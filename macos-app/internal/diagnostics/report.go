package diagnostics

import (
	"archive/zip"
	"encoding/json"
	"io"
	"runtime"
	"time"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/openconnect"
	"openconnect.local/desktop/internal/profiles"
)

type Report struct {
	Version      string             `json:"version"`
	GeneratedAt  time.Time          `json:"generatedAt"`
	OS           string             `json:"os"`
	Architecture string             `json:"architecture"`
	VPN          openconnect.Status `json:"vpn"`
}

func Write(w io.Writer, version string, ps *profiles.Store, ns *network.Store, logs *logging.Buffer, status openconnect.Status) error {
	z := zip.NewWriter(w)
	defer z.Close()
	files := map[string]any{
		"report.json":   Report{Version: version, GeneratedAt: time.Now().UTC(), OS: runtime.GOOS, Architecture: runtime.GOARCH, VPN: status},
		"profiles.json": map[string]any{"version": 1, "profiles": ps.List()}, "routes.json": ns.List(), "logs.json": logs.Entries(),
	}
	for name, value := range files {
		entry, e := z.Create(name)
		if e != nil {
			return e
		}
		enc := json.NewEncoder(entry)
		enc.SetIndent("", "  ")
		if e = enc.Encode(value); e != nil {
			return e
		}
	}
	return nil
}
