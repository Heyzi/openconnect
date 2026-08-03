package network

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
)

type Route struct {
	ID       string `json:"id"`
	CIDR     string `json:"cidr"`
	Source   string `json:"source"`
	Overlaps bool   `json:"overlaps,omitempty"`
}

type Store struct {
	mu     sync.RWMutex
	routes []Route
}

func NewStore() *Store { return &Store{routes: []Route{}} }
func id() string {
	b := make([]byte, 8)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func canonical(cidr string) (string, error) {
	ip, n, e := net.ParseCIDR(strings.TrimSpace(cidr))
	if e != nil {
		return "", errors.New("invalid CIDR")
	}
	n.IP = ip.Mask(n.Mask)
	if n.String() == "127.0.0.0/8" || n.String() == "::1/128" {
		return "", errors.New("localhost routes cannot be changed")
	}
	return n.String(), nil
}
func (s *Store) List() []Route {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := append([]Route{}, s.routes...)
	sort.SliceStable(out, func(i, j int) bool {
		iServer := strings.HasPrefix(out[i].Source, "server")
		jServer := strings.HasPrefix(out[j].Source, "server")
		if iServer != jServer {
			return iServer
		}
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		return out[i].CIDR < out[j].CIDR
	})
	return out
}

// MarkOverlaps marks every route that intersects at least one other route.
// A contained subnet intersects its parent network, so both are marked.
func MarkOverlaps(routes []Route) {
	networks := make([]*net.IPNet, len(routes))
	for i := range routes {
		_, networks[i], _ = net.ParseCIDR(routes[i].CIDR)
		routes[i].Overlaps = false
	}
	for i := range routes {
		if networks[i] == nil {
			continue
		}
		for j := i + 1; j < len(routes); j++ {
			if networks[j] == nil || len(networks[i].IP) != len(networks[j].IP) {
				continue
			}
			if networks[i].Contains(networks[j].IP) || networks[j].Contains(networks[i].IP) {
				routes[i].Overlaps = true
				routes[j].Overlaps = true
			}
		}
	}
}
func (s *Store) Get(routeID string) (Route, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, route := range s.routes {
		if route.ID == routeID {
			return route, true
		}
	}
	return Route{}, false
}
func (s *Store) GetByCIDR(cidr string) (Route, bool) {
	canonicalCIDR, err := canonical(cidr)
	if err != nil {
		return Route{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, route := range s.routes {
		if route.CIDR == canonicalCIDR {
			return route, true
		}
	}
	return Route{}, false
}
func (s *Store) Add(cidr string) (Route, error) {
	canonicalCIDR, e := canonical(cidr)
	if e != nil {
		return Route{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.routes {
		if r.CIDR == canonicalCIDR {
			return Route{}, fmt.Errorf("route %s already exists", canonicalCIDR)
		}
	}
	r := Route{ID: id(), CIDR: canonicalCIDR, Source: "user"}
	s.routes = append(s.routes, r)
	return r, nil
}
func (s *Store) Update(routeID, cidr string) (Route, error) {
	canonicalCIDR, e := canonical(cidr)
	if e != nil {
		return Route{}, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.routes {
		if r.ID != routeID && r.CIDR == canonicalCIDR {
			return Route{}, fmt.Errorf("route %s already exists", canonicalCIDR)
		}
	}
	for i := range s.routes {
		if s.routes[i].ID == routeID {
			s.routes[i].CIDR = canonicalCIDR
			s.routes[i].Source = "user"
			return s.routes[i], nil
		}
	}
	return Route{}, osNotExist
}
func (s *Store) Delete(routeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.routes {
		if s.routes[i].ID == routeID {
			s.routes = append(s.routes[:i], s.routes[i+1:]...)
			return nil
		}
	}
	return osNotExist
}
func (s *Store) AddServerWithSource(cidr, source string) {
	canonicalCIDR, e := canonical(cidr)
	if e != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.routes {
		if r.CIDR == canonicalCIDR {
			return
		}
	}
	if source != "server-include" && source != "server-exclude" {
		source = "server"
	}
	s.routes = append(s.routes, Route{ID: id(), CIDR: canonicalCIDR, Source: source})
}
func (s *Store) ClearServer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.routes[:0]
	for _, r := range s.routes {
		if !strings.HasPrefix(r.Source, "server") {
			out = append(out, r)
		}
	}
	s.routes = out
}

// Clear drops the complete runtime view. Server routes and user overrides are
// scoped to one tunnel session and must not survive its disconnect.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = []Route{}
}

var osNotExist = errors.New("route not found")
