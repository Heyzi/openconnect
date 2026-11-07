package openconnect

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/privileged"
)

// Checks the if_msghdr2 offsets against netstat on a live interface. The kernel
// reports the low 32 bits here, so compare modulo 2^32.
func TestKernelByteCountersMatchNetstat(t *testing.T) {
	got, err := kernelByteCounters("lo0")
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("/usr/sbin/netstat", "-b", "-I", "lo0").Output()
	if err != nil {
		t.Skip(err)
	}
	fields := strings.Fields(strings.Split(string(output), "\n")[1])
	received, _ := strconv.ParseUint(fields[len(fields)-5], 10, 64)
	sent, _ := strconv.ParseUint(fields[len(fields)-2], 10, 64)
	if counterDelta(got.received, received) > 1<<24 || counterDelta(got.sent, sent) > 1<<24 {
		t.Fatalf("kernel = %#v, netstat received = %d sent = %d", got, received, sent)
	}
}

func TestReadLogFollowsAppendsAndTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	logs := logging.New(10)
	m := &Manager{logPath: path, logs: logs}
	_ = os.WriteFile(path, []byte("one\ntw"), 0600)
	offset, remainder := m.readLog(0, "", false)
	file, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = file.WriteString("o\n")
	_ = file.Close()
	offset, remainder = m.readLog(offset, remainder, false)
	_ = os.WriteFile(path, []byte("x\n"), 0600)
	m.readLog(offset, remainder, false)
	entries := logs.Entries()
	if len(entries) != 3 || entries[1].Message != "two" || entries[2].Message != "x" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestHelperFailureDuringCleanupDoesNotBlockReconnect(t *testing.T) {
	m := NewWithClient(privileged.Client{
		DoFunc: func(privileged.Request) error { return errors.New("helper down") },
		QueryFunc: func(privileged.Request) (privileged.Response, error) {
			return privileged.Response{}, errors.New("helper down")
		},
	}, "", logging.New(10), network.NewStore())
	start := time.Now()
	m.status = Status{State: "connected", StartedAt: &start}
	m.failAndCleanup(start, "boom")
	m.finishDisconnect(&start, "boom", false, 0)
	if got := m.Status().State; got != "error" {
		t.Fatalf("state = %q, want error so Connect is allowed again", got)
	}
}

func TestAdoptRunningSessionExposesDisconnect(t *testing.T) {
	running := true
	m := NewWithClient(privileged.Client{QueryFunc: func(privileged.Request) (privileged.Response, error) {
		return privileged.Response{OK: true, Running: &running}, nil
	}}, "", logging.New(10), network.NewStore())
	m.AdoptRunningSession()
	if got := m.Status().State; got != "connected" {
		t.Fatalf("state = %q", got)
	}
	m.mu.Lock()
	m.status.State = "disconnected"
	m.mu.Unlock()
}

func TestCounterDeltaSurvives32BitWrap(t *testing.T) {
	if got := counterDelta(1<<32-10, 5); got != 15 {
		t.Fatalf("delta = %d", got)
	}
}

func TestTrafficTrackerReportsSessionTotalsAndRates(t *testing.T) {
	start := time.Unix(100, 0)
	tracker := trafficTracker{}
	if got := tracker.sample(byteCounters{received: 1000, sent: 500}, start); got != (TrafficStats{}) {
		t.Fatalf("baseline = %#v", got)
	}
	got := tracker.sample(byteCounters{received: 5000, sent: 1500}, start.Add(2*time.Second))
	want := TrafficStats{DownloadBytes: 4000, UploadBytes: 1000, DownloadBytesPerSec: 2000, UploadBytesPerSec: 500}
	if got != want {
		t.Fatalf("stats = %#v, want %#v", got, want)
	}
}
