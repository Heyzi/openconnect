package network

import "testing"

func TestRuntimeRouteCRUD(t *testing.T) {
	s := NewStore()
	s.AddServerWithSource("10.1.2.3/8", "server")
	routes := s.List()
	if len(routes) != 1 || routes[0].CIDR != "10.0.0.0/8" || routes[0].Source != "server" {
		t.Fatalf("server route: %#v", routes)
	}
	updated, e := s.Update(routes[0].ID, "172.16.1.2/16")
	if e != nil || updated.Source != "user" {
		t.Fatalf("update: %#v %v", updated, e)
	}
	added, e := s.Add("192.168.1.1/24")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Delete(added.ID); e != nil {
		t.Fatal(e)
	}
	if len(s.List()) != 1 {
		t.Fatal("delete failed")
	}
}
func TestRejectsUnsafeAndDuplicateRoutes(t *testing.T) {
	s := NewStore()
	if _, e := s.Add("127.0.0.0/8"); e == nil {
		t.Fatal("accepted localhost")
	}
	if _, e := s.Add("bad"); e == nil {
		t.Fatal("accepted invalid CIDR")
	}
	if _, e := s.Add("10.0.0.0/8"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Add("10.1.2.3/8"); e == nil {
		t.Fatal("accepted duplicate")
	}
}

func TestClearRemovesServerRoutesAndUserOverrides(t *testing.T) {
	s := NewStore()
	s.AddServerWithSource("10.0.0.0/8", "server")
	if _, err := s.Add("192.168.0.0/16"); err != nil {
		t.Fatal(err)
	}
	s.Clear()
	if routes := s.List(); len(routes) != 0 {
		t.Fatalf("routes survived disconnect: %#v", routes)
	}
}

func TestMarkOverlaps(t *testing.T) {
	routes := []Route{
		{CIDR: "10.0.0.0/8"},
		{CIDR: "10.20.0.0/16"},
		{CIDR: "192.168.0.0/16"},
		{CIDR: "2001:db8::/32"},
		{CIDR: "2001:db8:1::/48"},
	}
	MarkOverlaps(routes)
	want := []bool{true, true, false, true, true}
	for i := range routes {
		if routes[i].Overlaps != want[i] {
			t.Errorf("route %s overlap = %v, want %v", routes[i].CIDR, routes[i].Overlaps, want[i])
		}
	}
}

func TestListGroupsRoutesBySource(t *testing.T) {
	s := NewStore()
	s.AddServerWithSource("10.0.0.0/8", "server-exclude")
	s.AddServerWithSource("192.168.0.0/16", "server-include")
	s.AddServerWithSource("172.16.0.0/12", "server-exclude")

	routes := s.List()
	if routes[0].Source != "server-exclude" || routes[1].Source != "server-exclude" || routes[2].Source != "server-include" {
		t.Fatalf("routes are not grouped by source: %#v", routes)
	}
}
