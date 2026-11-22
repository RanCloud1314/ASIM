package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Node is one upstream proxy. Protocol defaults to trojan so configs written
// before v0.3.0 keep working unchanged.
type Node struct {
	Protocol         string `json:"protocol,omitempty"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	Password         string `json:"password"` // trojan password, vless uuid, hysteria2 auth
	SNI              string `json:"sni,omitempty"`
	Insecure         *bool  `json:"insecure,omitempty"`
	Flow             string `json:"flow,omitempty"`
	Network          string `json:"network,omitempty"`
	Path             string `json:"path,omitempty"`
	HostHeader       string `json:"host_header,omitempty"`
	ServiceName      string `json:"service_name,omitempty"`
	RealityPublicKey string `json:"reality_public_key,omitempty"`
	RealityShortID   string `json:"reality_short_id,omitempty"`
	Fingerprint      string `json:"fingerprint,omitempty"`
	Encryption       string `json:"encryption,omitempty"`
}

func (n Node) proto() string {
	if n.Protocol == "" {
		return "trojan"
	}
	return n.Protocol
}

// skipVerify defaults to true for trojan (pre-0.3.0 behavior, most public
// trojan nodes use a mismatched certificate) and false for vless/hysteria2.
func (n Node) skipVerify() bool {
	if n.Insecure != nil {
		return *n.Insecure
	}
	return n.proto() == "trojan"
}

func (n Node) addr() string { return net.JoinHostPort(n.Host, strconv.Itoa(n.Port)) }

// validate rejects anything mihomo cannot carry, so a saved node always
// renders into a working config.
func (n Node) validate() error {
	switch n.proto() {
	case "trojan", "vless", "hysteria2":
	default:
		return fmt.Errorf("unknown protocol %q", n.Protocol)
	}
	if strings.TrimSpace(n.Host) == "" {
		return errors.New("host is empty")
	}
	if n.Port < 1 || n.Port > 65535 {
		return fmt.Errorf("port %d out of range", n.Port)
	}
	if n.Password == "" {
		return errors.New("password/uuid is empty")
	}
	if n.proto() == "vless" {
		switch n.network() {
		case "tcp", "ws", "grpc", "httpupgrade":
		default:
			return fmt.Errorf("mihomo 未接 vless %s，请改用 ws/grpc/tcp", n.Network)
		}
		if n.RealityPublicKey != "" && n.SNI == "" {
			return errors.New("reality needs sni")
		}
	}
	return nil
}

func (n Node) network() string {
	if n.Network == "" {
		return "tcp"
	}
	return n.Network
}

func (n Node) signature() string {
	b, _ := json.Marshal(n)
	return string(b)
}
