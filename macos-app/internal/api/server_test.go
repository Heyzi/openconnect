package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	if err := checkServerReachableWithDial("https://vpn.example:4443", func(network, address string, timeout time.Duration) (net.Conn, error) {
		if network != "tcp" || address != "vpn.example:4443" || timeout != 3*time.Second {
			t.Fatalf("unexpected dial: %s %s %s", network, address, timeout)
		}
		return left, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := checkServerReachableWithDial("https://vpn.example", func(string, string, time.Duration) (net.Conn, error) {
		return nil, errors.New("offline")
	}); err == nil {
		t.Fatal("accepted closed VPN endpoint")
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
	s := New(ps, ns, keychain.Store{}, openconnect.NewWithClient(client, filepath.Join(dir, "state.json"), logs, ns), logs)
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
func TestBootstrapIsOneTimeAndProtectsSession(t *testing.T) {
	s := testServer(t)
	unauth := request(s, "GET", "/api/v1/status", "", nil, "")
	if unauth.Code != 401 {
		t.Fatalf("unauth status=%d", unauth.Code)
	}
	first := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	if first.Code != 303 {
		t.Fatalf("bootstrap status=%d", first.Code)
	}
	cookie := first.Result().Cookies()[0]
	reopen := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", cookie, "")
	if reopen.Code != 303 {
		t.Fatalf("authenticated reopen status=%d", reopen.Code)
	}
	second := request(s, "GET", "/bootstrap?token="+s.bootstrap, "", nil, "")
	if second.Code != 403 {
		t.Fatalf("reused token status=%d", second.Code)
	}
	ok := request(s, "GET", "/api/v1/status", "", cookie, "")
	if ok.Code != 200 {
		t.Fatalf("session status=%d", ok.Code)
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
