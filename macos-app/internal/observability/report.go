package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"time"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/openconnect"
)

type Stage struct {
	ID     string     `json:"id"`
	Name   string     `json:"name"`
	Status string     `json:"status"`
	Detail string     `json:"detail"`
	At     *time.Time `json:"at,omitempty"`
}

type Posture struct {
	Status          string     `json:"status"`
	Detail          string     `json:"detail"`
	FileName        string     `json:"fileName,omitempty"`
	SHA256          string     `json:"sha256,omitempty"`
	Requested       bool       `json:"requested"`
	PayloadReceived bool       `json:"payloadReceived"`
	Size            int64      `json:"size,omitempty"`
	ModifiedAt      *time.Time `json:"modifiedAt,omitempty"`
}

type Report struct {
	State     string     `json:"state"`
	Server    string     `json:"server,omitempty"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Stages    []Stage    `json:"stages"`
	Posture   Posture    `json:"posture"`
}

func Build(status openconnect.Status, entries []logging.Entry, capturePath string) Report {
	stages := []Stage{
		{ID: "process", Name: "Client", Status: "pending", Detail: "Waiting to start OpenConnect"},
		{ID: "network", Name: "Network", Status: "pending", Detail: "Waiting for DNS and TCP"},
		{ID: "tls", Name: "TLS", Status: "pending", Detail: "Waiting for secure transport"},
		{ID: "auth", Name: "Authentication", Status: "pending", Detail: "Waiting for credentials"},
		{ID: "posture", Name: "Posture / HostScan", Status: "pending", Detail: "No posture decision observed yet"},
		{ID: "tunnel", Name: "VPN tunnel", Status: "pending", Detail: "Waiting for CSTP tunnel"},
		{ID: "udp", Name: "UDP transport", Status: "pending", Detail: "Waiting for DTLS or ESP"},
		{ID: "config", Name: "Network configuration", Status: "pending", Detail: "Waiting for address and routes"},
	}
	if status.State == "disconnected" && status.StartedAt == nil {
		for i := range stages {
			stages[i].Status = "idle"
		}
	}
	set := func(id, state, detail string, at time.Time) {
		for i := range stages {
			if stages[i].ID == id {
				stages[i].Status, stages[i].Detail, stages[i].At = state, detail, &at
			}
		}
	}
	for _, entry := range entries {
		if status.StartedAt != nil && entry.Time.Before(*status.StartedAt) {
			continue
		}
		message := entry.Message
		switch {
		case strings.Contains(message, "root helper started connection"):
			set("process", "complete", "OpenConnect started through the privileged helper", entry.Time)
		case strings.Contains(message, "Connected to HTTPS") || strings.Contains(message, "SSL negotiation"):
			set("tls", "complete", message, entry.Time)
		case strings.Contains(message, "Connected to "):
			set("network", "complete", message, entry.Time)
		case strings.Contains(message, "Please enter") || strings.Contains(message, "One-time password"):
			set("auth", "active", "Server requested interactive authentication", entry.Time)
		case strings.Contains(message, "Got CONNECT response"):
			set("auth", "complete", "Authentication accepted by the VPN gateway", entry.Time)
		case containsPosture(message):
			set("posture", "active", message, entry.Time)
		case strings.Contains(message, "CSTP connected"):
			set("tunnel", "complete", message, entry.Time)
		case strings.Contains(message, "Established DTLS") || strings.Contains(message, "ESP session established"):
			set("udp", "complete", message, entry.Time)
		case strings.Contains(message, "Configured as "):
			set("config", "active", message, entry.Time)
		case strings.Contains(message, "server routes applied"):
			set("config", "complete", message, entry.Time)
		}
	}
	posture := inspectPosture(entries, status.StartedAt, capturePath)
	for i := range stages {
		if stages[i].ID == "posture" {
			stages[i].Status, stages[i].Detail = posture.Status, posture.Detail
		}
		if status.State == "error" && stages[i].Status == "pending" {
			stages[i].Status, stages[i].Detail = "error", status.LastError
		}
	}
	return Report{State: status.State, Server: status.Server, StartedAt: status.StartedAt, Stages: stages, Posture: posture}
}

func containsPosture(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "csd") || strings.Contains(lower, "hostscan") || strings.Contains(lower, "host scan") || strings.Contains(lower, "trojan") || strings.Contains(lower, "tncc") || strings.Contains(lower, "hip report")
}

func inspectPosture(entries []logging.Entry, startedAt *time.Time, capturePath string) Posture {
	p := Posture{Status: "not-requested", Detail: "The gateway has not requested CSD, HostScan, TNCC, or HIP during this session"}
	for _, entry := range entries {
		if startedAt != nil && entry.Time.Before(*startedAt) {
			continue
		}
		if containsPosture(entry.Message) {
			p.Requested, p.Status, p.Detail = true, "requested", entry.Message
		}
	}
	info, err := os.Stat(capturePath)
	if err != nil || info.IsDir() {
		return p
	}
	data, err := os.ReadFile(capturePath)
	if err != nil {
		p.Status, p.Detail = "error", "Captured payload exists but cannot be read: "+err.Error()
		return p
	}
	sum := sha256.Sum256(data)
	modified := info.ModTime().UTC()
	p.Requested, p.PayloadReceived, p.Status = true, true, "captured"
	p.Detail, p.FileName, p.Size, p.ModifiedAt = "Server-provided posture payload captured without additional execution", info.Name(), info.Size(), &modified
	p.SHA256 = hex.EncodeToString(sum[:])
	return p
}
