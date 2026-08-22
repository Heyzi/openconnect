package privileged

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	DefaultSocket   = "/var/run/openconnect-desktop.sock"
	ProtocolVersion = 8
)

func ComponentID(paths ...string) (string, error) {
	hash := sha256.New()
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if _, err = io.Copy(hash, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err = file.Close(); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

type ConnectRequest struct {
	Server, Protocol, Username, Group, Password, OTP, MACAddress string
	Verbose                                                      bool
	SaveServerScripts                                            bool
}
type RouteRequest struct{ ID, CIDR, PreviousCIDR string }
type Request struct {
	Operation       string          `json:"operation"`
	ProtocolVersion int             `json:"protocolVersion,omitempty"`
	Connect         *ConnectRequest `json:"connect,omitempty"`
	Route           *RouteRequest   `json:"route,omitempty"`
}
type Response struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	ConfigID  string `json:"configId,omitempty"`
	Running   *bool  `json:"running,omitempty"`
	Recovered *bool  `json:"recovered,omitempty"`
	LastExit  string `json:"lastExit,omitempty"`
}

type Client struct {
	Socket    string
	DoFunc    func(Request) error
	QueryFunc func(Request) (Response, error)
}

func (c Client) Do(req Request) error {
	if c.DoFunc != nil {
		return c.DoFunc(req)
	}
	_, err := c.Query(req)
	return err
}

func (c Client) Query(req Request) (Response, error) {
	if c.QueryFunc != nil {
		return c.QueryFunc(req)
	}
	var response Response
	socket := c.Socket
	if socket == "" {
		socket = DefaultSocket
	}
	conn, e := net.DialTimeout("unix", socket, 2*time.Second)
	if e != nil {
		return response, e
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if e = json.NewEncoder(conn).Encode(req); e != nil {
		return response, e
	}
	if e = json.NewDecoder(conn).Decode(&response); e != nil {
		return response, e
	}
	if !response.OK {
		return response, errors.New(response.Error)
	}
	return response, nil
}

// CaptureDir is where OpenConnect writes the gateway posture payload. The
// helper runs from launchd (no TMPDIR, so os.TempDir() is /tmp) while the app
// runs in the user session (per-user /var/folders TMPDIR), so the location must
// be spelled out instead of derived from os.TempDir() on both sides.
func CaptureDir(uid int) string {
	return fmt.Sprintf("/tmp/openconnect-desktop-csd-%d", uid)
}
