package profiles

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyStoreReturnsJSONArray(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s.List())
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "[]" {
		t.Fatalf("empty list encoded as %s", b)
	}
}

func TestPasswordIsNeverPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Save(Profile{Name: "Work", Server: "https://vpn.example", Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatal("password was written to profiles.json")
	}
}

func TestStoreRoundTripAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "profiles.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(Profile{Name: "Work", Server: "https://vpn.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Protocol != "anyconnect" {
		t.Fatalf("defaults missing: %#v", p)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(p.ID)
	if !ok || got.Name != "Work" {
		t.Fatalf("round trip failed: %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions = %o", info.Mode().Perm())
	}
}

func TestProfileValidation(t *testing.T) {
	for _, server := range []string{"", "vpn.example.com", "ftp://vpn.example.com"} {
		p := Profile{Name: "x", Server: server}
		if p.Validate() == nil {
			t.Errorf("accepted %q", server)
		}
	}
}

func TestPersistentRouteRulesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(Profile{Name: "VPN", Server: "https://vpn.example", RouteAdditions: []string{"192.168.0.0/16"}, RouteDeletions: []string{"10.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(saved.ID)
	if !ok || got.RouteAdditions[0] != "192.168.0.0/16" || got.RouteDeletions[0] != "10.0.0.0/8" {
		t.Fatalf("route rules were not persisted: %#v", got)
	}
}
