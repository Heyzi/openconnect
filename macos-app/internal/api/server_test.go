package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"openconnect.local/desktop/internal/keychain"
	"openconnect.local/desktop/internal/logging"
	"openconnect.local/desktop/internal/network"
	"openconnect.local/desktop/internal/openconnect"
	"openconnect.local/desktop/internal/privileged"
	"openconnect.local/desktop/internal/profiles"
)

func TestServerReachabilityCheck(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	lookup := func(_ context.Context, host string) ([]string, error) {
		if host != "vpn.example" {
			t.Fatalf("unexpected lookup: %s", host)
		}
		return []string{"192.0.2.10"}, nil
	}
	if err := checkServerReachableWithNetwork("https://vpn.example:4443", lookup, func(network, address string, timeout time.Duration) (net.Conn, error) {
		if network != "tcp" || address != "192.0.2.10:4443" || timeout != 3*time.Second {
			t.Fatalf("unexpected dial: %s %s %s", network, address, timeout)
		}
		return left, nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := checkServerReachableWithNetwork("https://vpn.example", lookup, func(string, string, time.Duration) (net.Conn, error) {
		return nil, errors.New("offline")
	}, nil); err == nil || !strings.Contains(err.Error(), "TCP connection") {
		t.Fatalf("closed VPN endpoint error = %v", err)
	}
}

func TestServerReachabilityReportsDNSFailureSeparately(t *testing.T) {
	err := checkServerReachableWithNetwork("https://vpn.example", func(context.Context, string) ([]string, error) {
		return nil, errors.New("resolver timeout")
	}, func(string, string, time.Duration) (net.Conn, error) {
		t.Fatal("dial called after DNS failure")
		return nil, nil
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "DNS lookup for VPN server vpn.example failed") {
		t.Fatalf("error = %v", err)
	}
}

func TestServerReachabilityRepairsStaleHostRoute(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	dials := 0
	err := checkServerReachableWithNetwork("https://vpn.example", func(context.Context, string) ([]string, error) {
		return []string{"192.0.2.10"}, nil
	}, func(string, string, time.Duration) (net.Conn, error) {
		dials++
		if dials == 1 {
			return nil, syscall.EADDRNOTAVAIL
		}
		return left, nil
	}, func(cidr string) error {
		if cidr != "192.0.2.10/32" {
			t.Fatalf("repaired route = %q", cidr)
		}
		return nil
	})
	if err != nil || dials != 2 {
		t.Fatalf("error = %v, dials = %d", err, dials)
	}
}

func TestServerFailoverUsesFirstReachableServer(t *testing.T) {
	checked := []string{}
	server, err := firstReachableServer([]string{"https://vpn1.example", "https://vpn2.example", "https://vpn3.example"}, func(server string) error {
		checked = append(checked, server)
		if server == "https://vpn2.example" {
			return nil
		}
		return errors.New("offline")
	})
	if err != nil || server != "https://vpn2.example" || len(checked) != 2 {
		t.Fatalf("server = %q, checked = %#v, error = %v", server, checked, err)
	}
}

func testServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	ps, e := profiles.NewStore(filepath.Join(dir, "profiles.json"))
	if e != nil {
		t.Fatal(e)
	}
	ns := network.NewStore()
	logs := logging.New(10)
	client := privileged.Client{DoFunc: func(privileged.Request) error { return nil }}
	s := New(ps, ns, keychain.Store{}, openconnect.NewWithClient(client, filepath.Join(dir, "state.json"), logs, ns), logs, "test-commit")
	s.host = "127.0.0.1:32123"
	return s
}
func request(s *Server, method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+s.host+path, strings.NewReader(body))
	r.Host = s.host
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	return w
}
func TestBootstrapCanReopenAndProtectsSession(t *testing.T) {
	s := testServer(t)
	bootstrap := s.bootstrap
	unauth := request(s, "GET", "/api/v1/status", "", nil, "")
	if unauth.Code != 401 {
		t.Fatalf("unauth status=%d", unauth.Code)
	}
	first := request(s, "GET", "/bootstrap?token="+bootstrap, "", nil, "")
	if first.Code != 303 {
		t.Fatalf("bootstrap status=%d", first.Code)
	}
	cookie := first.Result().Cookies()[0]
	reused := request(s, "GET", "/bootstrap?token="+bootstrap, "", nil, "")
	if reused.Code != http.StatusSeeOther {
		t.Fatalf("reused bootstrap status=%d", reused.Code)
	}
	if missing := request(s, "GET", "/bootstrap", "", nil, ""); missing.Code != 403 {
		t.Fatalf("empty bootstrap status=%d", missing.Code)
	}
	ok := request(s, "GET", "/api/v1/status", "", cookie, "")
	if ok.Code != 200 {
		t.Fatalf("session status=%d", ok.Code)
	}
	info := request(s, "GET", "/api/v1/session", "", cookie, "")
	if info.Code != 200 || !strings.Contains(info.Body.String(), `"buildCommit":"test-commit"`) {
		t.Fatalf("session info %d: %s", info.Code, info.Body.String())
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	s := testServer(t)
	boot := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	cookie := boot.Result().Cookies()[0]
	logout := request(s, "POST", "/api/v1/logout", "", cookie, s.csrf)
	if logout.Code != http.StatusNoContent || logout.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("logout status=%d cookies=%v", logout.Code, logout.Result().Cookies())
	}
	if got := request(s, "GET", "/api/v1/status", "", cookie, ""); got.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session status=%d", got.Code)
	}
	reopened := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	if reopened.Code != http.StatusSeeOther {
		t.Fatalf("reopen status=%d", reopened.Code)
	}
	if got := request(s, "GET", "/api/v1/status", "", reopened.Result().Cookies()[0], ""); got.Code != http.StatusOK {
		t.Fatalf("reopened session status=%d", got.Code)
	}
}

func TestCreateProfileIgnoresClientSuppliedID(t *testing.T) {
	s := testServer(t)
	boot := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	cookie := boot.Result().Cookies()[0]
	result := request(s, "POST", "/api/v1/profiles", `{"id":"00112233445566778899aabb","name":"Work","server":"https://vpn.example"}`, cookie, s.csrf)
	if result.Code != http.StatusOK || strings.Contains(result.Body.String(), "00112233445566778899aabb") {
		t.Fatalf("create profile %d: %s", result.Code, result.Body.String())
	}
}

func TestPasswordFailureDoesNotSaveProfile(t *testing.T) {
	s := testServer(t)
	s.keychain = keychain.Store{SetFunc: func(string, string) error { return errors.New("keychain unavailable") }}
	r := httptest.NewRequest("POST", "http://"+s.host+"/api/v1/profiles", strings.NewReader(`{"name":"Work","server":"https://vpn.example","password":"secret"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.saveProfile(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("save status=%d: %s", w.Code, w.Body.String())
	}
	if len(s.profiles.List()) != 0 {
		t.Fatal("profile was saved after Keychain failure")
	}
}

func TestInspectionEndpoint(t *testing.T) {
	s := testServer(t)
	s.logs.Add("Info", "OpenConnect", "CSTP connected")
	boot := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	cookie := boot.Result().Cookies()[0]
	result := request(s, "GET", "/api/v1/inspection", "", cookie, "")
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"id":"tunnel"`) {
		t.Fatalf("inspection %d: %s", result.Code, result.Body.String())
	}
}
func TestCSRFAndRouteValidation(t *testing.T) {
	s := testServer(t)
	boot := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	cookie := boot.Result().Cookies()[0]
	without := request(s, "POST", "/api/v1/routes", `{"cidr":"10.0.0.0/8"}`, cookie, "")
	if without.Code != 403 {
		t.Fatalf("missing CSRF status=%d", without.Code)
	}
	bad := request(s, "POST", "/api/v1/routes", `{"cidr":"127.0.0.0/8"}`, cookie, s.csrf)
	if bad.Code != 400 {
		t.Fatalf("unsafe route status=%d", bad.Code)
	}
	good := request(s, "POST", "/api/v1/routes", `{"cidr":"10.1.2.3/8"}`, cookie, s.csrf)
	if good.Code != 201 || !strings.Contains(good.Body.String(), "10.0.0.0/8") {
		t.Fatalf("route response %d: %s", good.Code, good.Body.String())
	}
}
func TestRoutesIncludeSavedProfileAdditions(t *testing.T) {
	s := testServer(t)
	profile, err := s.profiles.Save(profiles.Profile{Name: "Work", Server: "https://vpn.example", RouteAdditions: []string{"10.0.0.0/8"}})
	if err != nil {
		t.Fatal(err)
	}
	boot := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	cookie := boot.Result().Cookies()[0]
	routes := request(s, "GET", "/api/v1/routes?profileId="+profile.ID, "", cookie, "")
	if routes.Code != http.StatusOK || !strings.Contains(routes.Body.String(), `"cidr":"10.0.0.0/8","source":"saved"`) {
		t.Fatalf("saved routes %d: %s", routes.Code, routes.Body.String())
	}
}
func TestRejectsForeignHostAndOrigin(t *testing.T) {
	s := testServer(t)
	r := httptest.NewRequest("GET", "http://evil.example/api/v1/status", nil)
	r.Host = "evil.example"
	w := httptest.NewRecorder()
	s.handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("foreign host status=%d", w.Code)
	}
}
