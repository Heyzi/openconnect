package privileged

import (
	"encoding/json"
	"errors"
	"net"
	"time"
)

const DefaultSocket = "/var/run/openconnect-desktop.sock"

type ConnectRequest struct {
	Server, Protocol, Username, Group, Password, OTP, MACAddress string
	Verbose                                                      bool
}
type RouteRequest struct{ ID, CIDR, PreviousCIDR string }
type Request struct {
	Operation       string          `json:"operation"`
	ProtocolVersion int             `json:"protocolVersion,omitempty"`
	Connect         *ConnectRequest `json:"connect,omitempty"`
	Route           *RouteRequest   `json:"route,omitempty"`
}
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

type Client struct {
	Socket string
	DoFunc func(Request) error
}

func (c Client) Do(req Request) error {
	if c.DoFunc != nil {
		return c.DoFunc(req)
	}
	socket := c.Socket
	if socket == "" {
		socket = DefaultSocket
	}
	conn, e := net.DialTimeout("unix", socket, 2*time.Second)
	if e != nil {
		return e
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	if e = json.NewEncoder(conn).Encode(req); e != nil {
		return e
	}
	var response Response
	if e = json.NewDecoder(conn).Decode(&response); e != nil {
		return e
	}
	if !response.OK {
		return errors.New(response.Error)
	}
	return nil
}
