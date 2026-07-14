package profiles

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

type Profile struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Server            string   `json:"server"`
	Protocol          string   `json:"protocol,omitempty"`
	Username          string   `json:"username,omitempty"`
	Group             string   `json:"group,omitempty"`
	Certificate       string   `json:"certificate,omitempty"`
	ServerFingerprint string   `json:"serverFingerprint,omitempty"`
	ExtraArgs         []string `json:"extraArgs,omitempty"`
	AutoConnect       bool     `json:"autoConnect,omitempty"`
	Reconnect         bool     `json:"reconnect,omitempty"`
	TrustedNetworks   []string `json:"trustedNetworks,omitempty"`
	Verbose           bool     `json:"verbose,omitempty"`
	MACAddress        string   `json:"macAddress,omitempty"`
	RouteAdditions    []string `json:"routeAdditions,omitempty"`
	RouteDeletions    []string `json:"routeDeletions,omitempty"`
	Password          string   `json:"password,omitempty"`
	PasswordSet       bool     `json:"passwordSet,omitempty"`
}

func NewID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (p *Profile) Validate() error {
	p.Name, p.Server = strings.TrimSpace(p.Name), strings.TrimSpace(p.Server)
	if p.Name == "" {
		return errors.New("profile name is required")
	}
	u, err := url.Parse(p.Server)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return errors.New("server must be an http or https URL")
	}
	if p.ID == "" {
		p.ID = NewID()
	}
	if p.Protocol == "" {
		p.Protocol = "anyconnect"
	}
	if p.MACAddress != "" {
		matched := regexp.MustCompile(`(?i)^[0-9a-f]{2}([-:][0-9a-f]{2}){5}$`).MatchString(p.MACAddress)
		if !matched {
			return errors.New("MAC address must contain six hexadecimal octets")
		}
		// Keep the value in the same canonical form accepted by the reference
		// command line: --mac-address=02:00:00:00:00:01.
		p.MACAddress = strings.ToLower(strings.ReplaceAll(p.MACAddress, "-", ":"))
	}
	p.RouteAdditions, err = normalizeRouteRules(p.RouteAdditions)
	if err != nil {
		return fmt.Errorf("route additions: %w", err)
	}
	p.RouteDeletions, err = normalizeRouteRules(p.RouteDeletions)
	if err != nil {
		return fmt.Errorf("route deletions: %w", err)
	}
	deletions := make(map[string]bool, len(p.RouteDeletions))
	for _, cidr := range p.RouteDeletions {
		deletions[cidr] = true
	}
	for _, cidr := range p.RouteAdditions {
		if deletions[cidr] {
			return fmt.Errorf("route %s cannot be both added and deleted", cidr)
		}
	}
	return nil
}

func normalizeRouteRules(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		ip, network, err := net.ParseCIDR(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q", value)
		}
		network.IP = ip.Mask(network.Mask)
		cidr := network.String()
		if cidr == "127.0.0.0/8" || cidr == "::1/128" {
			return nil, errors.New("localhost routes cannot be changed")
		}
		if !seen[cidr] {
			seen[cidr] = true
			out = append(out, cidr)
		}
	}
	return out, nil
}
