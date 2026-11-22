package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	hyclient "github.com/apernet/hysteria/core/v2/client"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"

	// Xray handlers/transports used by the vless outbound. Kept minimal so the
	// binary doesn't pull wireguard/gvisor and the rest of the distro.
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/log"
	_ "github.com/xtls/xray-core/app/policy"
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/freedom"
	_ "github.com/xtls/xray-core/proxy/socks"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/transport/internet/grpc"
	_ "github.com/xtls/xray-core/transport/internet/httpupgrade"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/splithttp"
	_ "github.com/xtls/xray-core/transport/internet/tagged/taggedimpl"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/udp"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
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
		case "tcp", "ws", "grpc", "httpupgrade", "xhttp", "splithttp":
		default:
			return fmt.Errorf("unknown vless network %q", n.Network)
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

// udpSession is one bidirectional UDP conversation with a fixed destination.
type udpSession interface {
	Write([]byte) error
	Read() ([]byte, error)
	Close() error
}

func dialSession(n Node, dst netip.AddrPort) (udpSession, error) {
	if err := n.validate(); err != nil {
		return nil, err
	}
	switch n.proto() {
	case "trojan":
		return dialTrojan(n, dst)
	case "vless":
		proxy, err := xrayEng.socksAddr(n)
		if err != nil {
			return nil, err
		}
		return dialSocksUDP(proxy, dst)
	case "hysteria2":
		return hyEng.session(n, dst)
	}
	return nil, fmt.Errorf("unknown protocol %q", n.Protocol)
}

// ---------- trojan over TLS ----------

type trojanUDP struct {
	conn net.Conn
	br   *bufio.Reader
	mu   sync.Mutex
	dst  netip.AddrPort
}

func trojanAuth(pw string) string {
	h := sha256.New224()
	h.Write([]byte(pw))
	return hex.EncodeToString(h.Sum(nil))
}

func dialTrojan(n Node, dst netip.AddrPort) (*trojanUDP, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", n.addr(), &tls.Config{
		ServerName:         n.SNI,
		InsecureSkipVerify: n.skipVerify(),
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	return newTrojanUDP(conn, n, dst)
}

// newTrojanUDP writes the UDP ASSOCIATE handshake and wraps the stream.
func newTrojanUDP(conn net.Conn, n Node, dst netip.AddrPort) (*trojanUDP, error) {
	t := &trojanUDP{conn: conn, br: bufio.NewReaderSize(conn, 32*1024), dst: dst}
	buf := make([]byte, 0, 64)
	buf = append(buf, trojanAuth(n.Password)...)
	buf = append(buf, 0x0d, 0x0a)
	buf = append(buf, 0x03) // CMD UDP ASSOCIATE
	buf = appendAddr(buf, dst)
	buf = append(buf, 0x0d, 0x0a)
	if _, err := conn.Write(buf); err != nil {
		conn.Close()
		return nil, err
	}
	return t, nil
}

func appendAddr(b []byte, ap netip.AddrPort) []byte {
	a := ap.Addr()
	if a.Is4() {
		b = append(b, 0x01)
		v := a.As4()
		b = append(b, v[:]...)
	} else {
		b = append(b, 0x04)
		v := a.As16()
		b = append(b, v[:]...)
	}
	p := ap.Port()
	return append(b, byte(p>>8), byte(p))
}

// writeFrame: [addr+port][len][CRLF][payload]
func (t *trojanUDP) writeFrame(dst netip.AddrPort, payload []byte) error {
	buf := make([]byte, 0, 32+len(payload))
	buf = appendAddr(buf, dst)
	n := len(payload)
	buf = append(buf, byte(n>>8), byte(n), 0x0d, 0x0a)
	buf = append(buf, payload...)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := t.conn.Write(buf)
	return err
}

// readFramePayload parses [ATYP][ADDR][PORT][LEN][CRLF][PAYLOAD].
func (t *trojanUDP) readFramePayload() ([]byte, error) {
	t.conn.SetReadDeadline(time.Now().Add(idleTimeout))
	atyp, err := t.br.ReadByte()
	if err != nil {
		return nil, err
	}
	switch atyp {
	case 0x01:
		if _, err := t.br.Discard(4); err != nil {
			return nil, err
		}
	case 0x04:
		if _, err := t.br.Discard(16); err != nil {
			return nil, err
		}
	case 0x03:
		l, err := t.br.ReadByte()
		if err != nil {
			return nil, err
		}
		if _, err := t.br.Discard(int(l)); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("bad atyp %#x", atyp)
	}
	if _, err := t.br.Discard(2); err != nil { // port
		return nil, err
	}
	var lb [2]byte
	if _, err := io.ReadFull(t.br, lb[:]); err != nil {
		return nil, err
	}
	n := int(lb[0])<<8 | int(lb[1])
	if _, err := t.br.Discard(2); err != nil { // CRLF
		return nil, err
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(t.br, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (t *trojanUDP) Write(p []byte) error {
	return t.writeFrame(t.dst, p)
}

func (t *trojanUDP) Read() ([]byte, error) { return t.readFramePayload() }

func (t *trojanUDP) Close() error { return t.conn.Close() }

// ---------- vless via an in-process xray instance + socks5 udp ----------

type xrayEngine struct {
	mu   sync.Mutex
	inst *core.Instance
	port int
	sig  string
}

var xrayEng xrayEngine

func (e *xrayEngine) socksAddr(n Node) (string, error) {
	sig := n.signature()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.inst != nil && e.sig == sig {
		return net.JoinHostPort("127.0.0.1", strconv.Itoa(e.port)), nil
	}
	e.closeLocked()

	port, err := freeTCPPort()
	if err != nil {
		return "", err
	}
	raw, err := xrayConfigJSON(n, port)
	if err != nil {
		return "", err
	}
	var c conf.Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return "", fmt.Errorf("xray config: %w", err)
	}
	xc, err := c.Build()
	if err != nil {
		return "", fmt.Errorf("xray build: %w", err)
	}
	inst, err := core.New(xc)
	if err != nil {
		return "", fmt.Errorf("xray new: %w", err)
	}
	if err := inst.Start(); err != nil {
		inst.Close()
		return "", fmt.Errorf("xray start: %w", err)
	}
	e.inst, e.port, e.sig = inst, port, sig
	log.Printf("vless engine up via %s (socks 127.0.0.1:%d)", n.addr(), port)
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), nil
}

func (e *xrayEngine) closeLocked() {
	if e.inst != nil {
		e.inst.Close()
		e.inst = nil
	}
	e.port, e.sig = 0, ""
}

func (e *xrayEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closeLocked()
}

func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// xrayConfigJSON maps Node to an xray JSON config with a local socks inbound.
func xrayConfigJSON(n Node, socksPort int) ([]byte, error) {
	security := "tls"
	if n.RealityPublicKey != "" {
		security = "reality"
	}
	stream := map[string]any{"network": n.network(), "security": security}

	if security == "reality" {
		fp := n.Fingerprint
		if fp == "" {
			fp = "chrome"
		}
		stream["realitySettings"] = map[string]any{
			"serverName":  n.SNI,
			"publicKey":   n.RealityPublicKey,
			"shortId":     n.RealityShortID,
			"fingerprint": fp,
		}
	} else {
		tlsSettings := map[string]any{
			"serverName":    n.SNI,
			"allowInsecure": n.skipVerify(),
		}
		if n.Fingerprint != "" {
			tlsSettings["fingerprint"] = n.Fingerprint
		}
		stream["tlsSettings"] = tlsSettings
	}

	path := n.Path
	if path == "" {
		path = "/"
	}
	switch n.network() {
	case "ws":
		ws := map[string]any{"path": path}
		if n.HostHeader != "" {
			ws["host"] = n.HostHeader
		}
		stream["wsSettings"] = ws
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": n.ServiceName}
	case "httpupgrade":
		hu := map[string]any{"path": path}
		if n.HostHeader != "" {
			hu["host"] = n.HostHeader
		}
		stream["httpupgradeSettings"] = hu
	case "xhttp", "splithttp":
		xh := map[string]any{"path": path, "mode": "auto"}
		if n.HostHeader != "" {
			xh["host"] = n.HostHeader
		}
		stream["xhttpSettings"] = xh
	}

	enc := n.Encryption
	if enc == "" {
		enc = "none"
	}
	user := map[string]any{"id": n.Password, "encryption": enc}
	if n.Flow != "" {
		user["flow"] = n.Flow
	}
	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{
			map[string]any{
				"listen":   "127.0.0.1",
				"port":     socksPort,
				"protocol": "socks",
				"settings": map[string]any{"auth": "noauth", "udp": true},
			},
		},
		"outbounds": []any{
			map[string]any{
				"protocol": "vless",
				"settings": map[string]any{
					"vnext": []any{
						map[string]any{
							"address": n.Host,
							"port":    n.Port,
							"users":   []any{user},
						},
					},
				},
				"streamSettings": stream,
			},
		},
	}
	return json.Marshal(cfg)
}

// socksUDP is a socks5 UDP ASSOCIATE session to a fixed destination.
type socksUDP struct {
	ctrl net.Conn
	conn *net.UDPConn
	dst  netip.AddrPort
}

func dialSocksUDP(proxy string, dst netip.AddrPort) (*socksUDP, error) {
	ctrl, err := net.DialTimeout("tcp", proxy, 5*time.Second)
	if err != nil {
		return nil, err
	}
	ctrl.SetDeadline(time.Now().Add(5 * time.Second))
	fail := func(err error) (*socksUDP, error) {
		ctrl.Close()
		return nil, err
	}
	if _, err := ctrl.Write([]byte{5, 1, 0}); err != nil {
		return fail(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(ctrl, greeting[:]); err != nil {
		return fail(err)
	}
	if greeting != [2]byte{5, 0} {
		return fail(fmt.Errorf("socks greeting %v", greeting))
	}
	if _, err := ctrl.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return fail(err)
	}
	relay, err := readSocksAddr(ctrl)
	if err != nil {
		return fail(err)
	}
	ctrl.SetDeadline(time.Time{})
	ra, err := net.ResolveUDPAddr("udp", relay)
	if err != nil {
		return fail(err)
	}
	u, err := net.DialUDP("udp", nil, ra)
	if err != nil {
		return fail(err)
	}
	return &socksUDP{ctrl: ctrl, conn: u, dst: dst}, nil
}

// readSocksAddr reads VER REP RSV ATYP ADDR PORT and returns host:port.
func readSocksAddr(r io.Reader) (string, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return "", err
	}
	if h[0] != 5 || h[1] != 0 {
		return "", fmt.Errorf("socks reply %v", h)
	}
	host, err := readSocksHost(r, h[3])
	if err != nil {
		return "", err
	}
	var pb [2]byte
	if _, err := io.ReadFull(r, pb[:]); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(pb[:])
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func readSocksHost(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 1:
		var b [4]byte
		_, err := io.ReadFull(r, b[:])
		return net.IP(b[:]).String(), err
	case 4:
		var b [16]byte
		_, err := io.ReadFull(r, b[:])
		return net.IP(b[:]).String(), err
	case 3:
		var l [1]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", err
		}
		b := make([]byte, int(l[0]))
		_, err := io.ReadFull(r, b)
		return string(b), err
	}
	return "", fmt.Errorf("socks atyp %d", atyp)
}

func (s *socksUDP) Write(p []byte) error {
	buf := make([]byte, 0, 3+18+len(p))
	buf = append(buf, 0, 0, 0) // RSV RSV FRAG
	a := s.dst.Addr()
	if a.Is4() {
		buf = append(buf, 1)
		v := a.As4()
		buf = append(buf, v[:]...)
	} else {
		buf = append(buf, 4)
		v := a.As16()
		buf = append(buf, v[:]...)
	}
	port := s.dst.Port()
	buf = append(buf, byte(port>>8), byte(port))
	buf = append(buf, p...)
	s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	_, err := s.conn.Write(buf)
	return err
}

func (s *socksUDP) Read() ([]byte, error) {
	buf := make([]byte, 64*1024+32)
	s.conn.SetReadDeadline(time.Now().Add(idleTimeout))
	n, err := s.conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 4 {
		return nil, errors.New("short socks udp frame")
	}
	if buf[2] != 0 {
		return nil, errors.New("fragmented socks udp not supported")
	}
	atyp := buf[3]
	off := 4
	switch atyp {
	case 1:
		off += 4
	case 4:
		off += 16
	case 3:
		if n < 5 {
			return nil, errors.New("short socks udp domain")
		}
		off += 1 + int(buf[4])
	default:
		return nil, fmt.Errorf("socks udp atyp %d", atyp)
	}
	off += 2 // port
	if n < off {
		return nil, errors.New("short socks udp frame")
	}
	return buf[off:n], nil
}

func (s *socksUDP) Close() error {
	s.conn.Close()
	return s.ctrl.Close()
}

// ---------- hysteria2 ----------

type hyEngine struct {
	mu  sync.Mutex
	sig string
	cli hyclient.Client
}

var hyEng hyEngine

func (e *hyEngine) clientFor(n Node) (hyclient.Client, error) {
	sig := n.signature()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli != nil && e.sig == sig {
		return e.cli, nil
	}
	if e.cli != nil {
		e.cli.Close()
		e.cli = nil
		e.sig = ""
	}
	addr, err := net.ResolveUDPAddr("udp", n.addr())
	if err != nil {
		return nil, err
	}
	sni := n.SNI
	if sni == "" {
		sni = n.Host
	}
	cli, _, err := hyclient.NewClient(&hyclient.Config{
		ServerAddr: addr,
		Auth:       n.Password,
		TLSConfig: hyclient.TLSConfig{
			ServerName:         sni,
			InsecureSkipVerify: n.skipVerify(),
		},
	})
	if err != nil {
		return nil, err
	}
	e.cli, e.sig = cli, sig
	log.Printf("hysteria2 engine up via %s", n.addr())
	return cli, nil
}

func (e *hyEngine) session(n Node, dst netip.AddrPort) (udpSession, error) {
	cli, err := e.clientFor(n)
	if err != nil {
		return nil, err
	}
	uc, err := cli.UDP()
	if err != nil {
		// A dead QUIC client stays cached; drop it so the next packet reconnects.
		e.mu.Lock()
		if e.cli == cli {
			cli.Close()
			e.cli, e.sig = nil, ""
		}
		e.mu.Unlock()
		return nil, err
	}
	return &hyUDP{uc: uc, dst: dst.String()}, nil
}

func (e *hyEngine) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cli != nil {
		e.cli.Close()
		e.cli, e.sig = nil, ""
	}
}

type hyUDP struct {
	uc  hyclient.HyUDPConn
	dst string
}

func (h *hyUDP) Write(p []byte) error { return h.uc.Send(p, h.dst) }

func (h *hyUDP) Read() ([]byte, error) {
	p, _, err := h.uc.Receive()
	return p, err
}

func (h *hyUDP) Close() error { return h.uc.Close() }

// ---------- latency probe ----------

var pingDst = netip.MustParseAddrPort("223.5.5.5:53")

// probe.dnsQuery is a tiny A query for example.com.
var probeQuery = []byte{0x12, 0x34, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0,
	7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}

// probe measures a real UDP round trip through the node, handshake included.
func probe(n Node) (int64, error) {
	if err := n.validate(); err != nil {
		return 0, err
	}
	start := time.Now()
	s, err := dialSession(n, pingDst)
	if err != nil {
		return 0, err
	}
	defer s.Close()
	done := make(chan error, 1)
	go func() {
		if err := s.Write(probeQuery); err != nil {
			done <- err
			return
		}
		p, err := s.Read()
		if err == nil && len(p) < 12 {
			err = fmt.Errorf("short dns reply (%d bytes)", len(p))
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return 0, err
		}
		return time.Since(start).Milliseconds(), nil
	case <-time.After(10 * time.Second):
		return 0, errors.New("timeout")
	}
}
