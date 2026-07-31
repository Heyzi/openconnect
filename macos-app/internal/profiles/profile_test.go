package profiles

import "testing"

func TestValidateCanonicalizesMACWithColons(t *testing.T) {
	p := Profile{Name: "VPN", Server: "https://vpn.example", MACAddress: "02-00-00-00-00-01"}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.MACAddress != "02:00:00:00:00:01" {
		t.Fatalf("MAC address = %q", p.MACAddress)
	}
}

func TestValidateNormalizesFallbackServers(t *testing.T) {
	p := Profile{Name: "VPN", Server: " https://vpn1.example ", FallbackServers: []string{"https://vpn2.example", "https://vpn1.example"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Server != "https://vpn1.example" || len(p.FallbackServers) != 1 || p.FallbackServers[0] != "https://vpn2.example" {
		t.Fatalf("servers = %#v", p.Servers())
	}
}

func TestValidateCanonicalizesAndRejectsConflictingRouteRules(t *testing.T) {
	p := Profile{Name: "VPN", Server: "https://vpn.example", RouteAdditions: []string{"10.1.2.3/8", "10.0.0.0/8"}, RouteDeletions: []string{"192.168.1.4/24"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(p.RouteAdditions) != 1 || p.RouteAdditions[0] != "10.0.0.0/8" || p.RouteDeletions[0] != "192.168.1.0/24" {
		t.Fatalf("route rules were not canonicalized: %#v %#v", p.RouteAdditions, p.RouteDeletions)
	}
	p.RouteDeletions = []string{"10.0.0.0/8"}
	if err := p.Validate(); err == nil {
		t.Fatal("accepted the same route in additions and deletions")
	}
}

func TestValidateRejectsArbitraryProfileID(t *testing.T) {
	p := Profile{ID: `" onmouseover="alert(1)`, Name: "VPN", Server: "https://vpn.example"}
	if err := p.Validate(); err == nil {
		t.Fatal("accepted arbitrary profile ID")
	}
}
