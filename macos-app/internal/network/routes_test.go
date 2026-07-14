package network

import "testing"

func TestRuntimeRouteCRUD(t *testing.T) {
	s := NewStore()
	s.AddServer("10.1.2.3/8")
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
	s.AddServer("10.0.0.0/8")
	if _, err := s.Add("192.168.0.0/16"); err != nil {
		t.Fatal(err)
	}
	s.Clear()
	if routes := s.List(); len(routes) != 0 {
		t.Fatalf("routes survived disconnect: %#v", routes)
	}
}
