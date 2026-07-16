package openconnect

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/privileged"
	"openconnect.local/desktop/internal/profiles"
)

func TestUnexpectedOpenConnectExitChangesConnectedStatusToError(t *testing.T) {
	running := false
	client := privileged.Client{QueryFunc: func(request privileged.Request) (privileged.Response, error) {
		if request.Operation != "status" {
			t.Fatalf("operation = %q, want status", request.Operation)
		}
		return privileged.Response{OK: true, Running: &running, LastExit: "exit status 1"}, nil
	}}
	manager := NewWithClient(client, "", logging.New(20), network.NewStore())
	start := time.Now().UTC()
	manager.status = Status{State: "connected", StartedAt: &start}
	go manager.watchProcess(start)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.Status()
		if status.State == "error" {
			if want := "OpenConnect exited unexpectedly: exit status 1"; status.LastError != want {
				t.Fatalf("LastError = %q, want %q", status.LastError, want)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("status did not change to error: %#v", manager.Status())
}

func TestLogWatcherFlushesFinalLineAfterProcessExit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "openconnect.log")
	if err := os.WriteFile(logPath, []byte("first line\nfinal error without newline"), 0600); err != nil {
		t.Fatal(err)
	}
	logs := logging.New(20)
	manager := NewWithClient(privileged.Client{}, "", logs, network.NewStore())
	manager.logPath = logPath
	start := time.Now().UTC()
	manager.status = Status{State: "error", StartedAt: &start}

	manager.watchLog(start)

	entries := logs.Entries()
	if len(entries) != 2 || entries[0].Message != "first line" || entries[1].Message != "final error without newline" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestProcessWatcherIgnoresTransientHelperFailure(t *testing.T) {
	calls := 0
	running := true
	client := privileged.Client{QueryFunc: func(privileged.Request) (privileged.Response, error) {
		calls++
		if calls == 1 {
			return privileged.Response{}, errors.New("temporary socket failure")
		}
		return privileged.Response{OK: true, Running: &running}, nil
	}}
	manager := NewWithClient(client, "", logging.New(20), network.NewStore())
	start := time.Now().UTC()
	manager.status = Status{State: "connected", StartedAt: &start}
	go manager.watchProcess(start)
	t.Cleanup(func() {
		manager.mu.Lock()
		manager.status = Status{State: "disconnected"}
		manager.mu.Unlock()
	})
	time.Sleep(1100 * time.Millisecond)
	if status := manager.Status(); status.State != "connected" {
		t.Fatalf("status = %#v, want connected", status)
	}
}

func TestShutdownWaitsForProcessAndNetworkCleanup(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	statusCalls := 0
	running := true
	client := privileged.Client{
		DoFunc: func(request privileged.Request) error {
			if request.Operation != "disconnect" {
				t.Fatalf("operation = %q, want disconnect", request.Operation)
			}
			return nil
		},
		QueryFunc: func(request privileged.Request) (privileged.Response, error) {
			if request.Operation != "status" {
				t.Fatalf("operation = %q, want status", request.Operation)
			}
			statusCalls++
			if statusCalls == 2 {
				running = false
				if err := os.Remove(statePath); err != nil {
					t.Fatal(err)
				}
			}
			return privileged.Response{OK: true, Running: &running}, nil
		},
	}
	manager := NewWithClient(client, statePath, logging.New(20), network.NewStore())
	if err := manager.Shutdown(time.Second); err != nil {
		t.Fatal(err)
	}
	if statusCalls < 2 {
		t.Fatalf("status calls = %d, want at least 2", statusCalls)
	}
}

func TestSavedRouteOverridesApplyAfterServerRoutes(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"reason":"connect","tunnelDevice":"utun9","routes":[{"cidr":"10.0.0.0/8","source":"server-include"},{"cidr":"172.16.0.0/12","source":"server-include"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var operations []string
	client := privileged.Client{DoFunc: func(request privileged.Request) error {
		operations = append(operations, request.Operation+":"+request.Route.CIDR)
		return nil
	}}
	routes := network.NewStore()
	manager := NewWithClient(client, statePath, logging.New(20), routes)
	start := time.Now().UTC()
	manager.status = Status{State: "connecting", StartedAt: &start}
	profile := profiles.Profile{RouteDeletions: []string{"10.0.0.0/8"}, RouteAdditions: []string{"192.168.0.0/16"}}
	manager.waitForApplied(start, profile)
	got := routes.List()
	if len(got) != 2 || got[0].CIDR != "172.16.0.0/12" || got[1].CIDR != "192.168.0.0/16" {
		t.Fatalf("routes = %#v", got)
	}
	wantOperations := []string{"route.delete:10.0.0.0/8", "route.add:192.168.0.0/16"}
	if !reflect.DeepEqual(operations, wantOperations) {
		t.Fatalf("operations = %#v, want %#v", operations, wantOperations)
	}
}

func TestWaitForAppliedIgnoresBaselineState(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, []byte(`{"tunnelDevice":"utun9","routes":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	routes := network.NewStore()
	manager := NewWithClient(privileged.Client{DoFunc: func(privileged.Request) error { return nil }}, statePath, logging.New(20), routes)
	start := time.Now().UTC()
	manager.status = Status{State: "connecting", StartedAt: &start}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = os.WriteFile(statePath, []byte(`{"reason":"connect","tunnelDevice":"utun9","routes":[{"cidr":"10.0.0.0/8","source":"server-include"}]}`), 0600)
	}()
	manager.waitForApplied(start, profiles.Profile{})
	got := routes.List()
	if len(got) != 1 || got[0].CIDR != "10.0.0.0/8" {
		t.Fatalf("routes = %#v", got)
	}
}
