package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := Node{Host: "example.com", Port: 443, Password: "pw"}
	cases := []struct {
		name string
		node Node
		want string
	}{
		{"trojan default", ok, ""},
		{"vless tcp", Node{Protocol: "vless", Host: "h", Port: 443, Password: "uuid", SNI: "s"}, ""},
		{"vless ws+reality", Node{Protocol: "vless", Host: "h", Port: 443, Password: "uuid", SNI: "s", Network: "ws", RealityPublicKey: "k"}, ""},
		{"hysteria2", Node{Protocol: "hysteria2", Host: "h", Port: 443, Password: "auth"}, ""},
		{"unknown protocol", Node{Protocol: "ss", Host: "h", Port: 443, Password: "pw"}, "unknown protocol"},
		{"empty host", Node{Protocol: "trojan", Port: 443, Password: "pw"}, "host is empty"},
		{"port range", Node{Host: "h", Port: 70000, Password: "pw"}, "out of range"},
		{"empty password", Node{Host: "h", Port: 1}, "empty"},
		{"vless bad network", Node{Protocol: "vless", Host: "h", Port: 1, Password: "u", Network: "kcp"}, "network"},
		{"reality without sni", Node{Protocol: "vless", Host: "h", Port: 1, Password: "u", RealityPublicKey: "k"}, "reality needs sni"},
	}
	for _, c := range cases {
		err := c.node.validate()
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want contains %q", c.name, err, c.want)
		}
	}
}

func TestSkipVerifyDefault(t *testing.T) {
	yes := true
	no := false
	if !(Node{}).skipVerify() {
		t.Error("trojan should default to insecure (pre-0.3.0 behavior)")
	}
	if (Node{Protocol: "hysteria2"}).skipVerify() {
		t.Error("hysteria2 should default to verifying")
	}
	if !(Node{Insecure: &yes}).skipVerify() {
		t.Error("explicit insecure=true ignored")
	}
	if (Node{Protocol: "trojan", Insecure: &no}).skipVerify() {
		t.Error("explicit insecure=false ignored")
	}
}

func TestXrayConfigJSON(t *testing.T) {
	n := Node{
		Protocol: "vless", Host: "1.2.3.4", Port: 443,
		Password: "11111111-2222-3333-4444-555555555555",
		SNI:      "www.shopify.com", Network: "ws", Path: "/ray",
		HostHeader: "cdn.example.com", Flow: "xtls-rprx-vision",
	}
	raw, err := xrayConfigJSON(n, 12345)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	in := cfg["inbounds"].([]any)[0].(map[string]any)
	if in["protocol"] != "socks" || in["port"].(float64) != 12345 {
		t.Fatalf("bad inbound: %v", in)
	}
	out := cfg["outbounds"].([]any)[0].(map[string]any)
	st := out["streamSettings"].(map[string]any)
	if st["network"] != "ws" || st["security"] != "tls" {
		t.Fatalf("bad stream: %v", st)
	}
	if st["wsSettings"].(map[string]any)["path"] != "/ray" {
		t.Fatalf("bad ws settings: %v", st["wsSettings"])
	}
	if st["wsSettings"].(map[string]any)["host"] != "cdn.example.com" {
		t.Fatalf("ws host header lost")
	}
	if st["tlsSettings"].(map[string]any)["allowInsecure"] != false {
		t.Fatalf("vless should verify by default: %v", st["tlsSettings"])
	}
	user := out["settings"].(map[string]any)["vnext"].([]any)[0].(map[string]any)["users"].([]any)[0].(map[string]any)
	if user["id"] != n.Password || user["flow"] != "xtls-rprx-vision" || user["encryption"] != "none" {
		t.Fatalf("bad user: %v", user)
	}

	// reality overrides tls and pins fingerprint default.
	n.Network = "tcp"
	n.RealityPublicKey, n.RealityShortID = "pub", "ab"
	raw, _ = xrayConfigJSON(n, 1)
	json.Unmarshal(raw, &cfg)
	st = cfg["outbounds"].([]any)[0].(map[string]any)["streamSettings"].(map[string]any)
	if st["security"] != "reality" || st["tlsSettings"] != nil {
		t.Fatalf("reality not applied: %v", st)
	}
	rs := st["realitySettings"].(map[string]any)
	if rs["publicKey"] != "pub" || rs["shortId"] != "ab" || rs["fingerprint"] != "chrome" || rs["serverName"] != n.SNI {
		t.Fatalf("bad reality settings: %v", rs)
	}
}

func TestTrojanFraming(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	defer srv.Close()

	dst := netip.MustParseAddrPort("8.8.8.8:53")
	n := Node{Password: "secret"}

	done := make(chan error, 1)
	go func() {
		want := trojanAuth("secret") + "\r\n\x03\x01\x08\x08\x08\x08\x00\x35\r\n"
		buf := make([]byte, len(want))
		if _, err := io.ReadFull(srv, buf); err != nil {
			done <- err
			return
		}
		if string(buf) != want {
			done <- &frameErr{"handshake", string(buf), want}
			return
		}
		// client write: [addr][len][CRLF][payload]
		hdr := make([]byte, 1+4+2+2+2)
		if _, err := io.ReadFull(srv, hdr); err != nil {
			done <- err
			return
		}
		got := string(hdr)
		if got != "\x01\x08\x08\x08\x08\x00\x35\x00\x05\r\n" {
			done <- &frameErr{"frame header", got, "\x01\x08\x08\x08\x08\x00\x35\x00\x05\r\n"}
			return
		}
		pl := make([]byte, 5)
		if _, err := io.ReadFull(srv, pl); err != nil {
			done <- err
			return
		}
		// reply frame: same shape, payload "hello"
		srv.Write([]byte("\x01\x08\x08\x08\x08\x00\x35\x00\x05\r\nhello"))
		done <- nil
	}()

	tu, err := newTrojanUDP(cli, n, dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := tu.Write([]byte("world")); err != nil {
		t.Fatal(err)
	}
	got, err := tu.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("read payload %q", got)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type frameErr struct{ what, got, want string }

func (e *frameErr) Error() string {
	return e.what + ": got " + e.got + " want " + e.want
}

// TestSocksUDP runs a fake socks5 server that relays UDP back verbatim, so the
// client's associate + header parsing path is exercised over real sockets.
func TestSocksUDP(t *testing.T) {
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	relayAddr := udp.LocalAddr().(*net.UDPAddr)

	go func() {
		c, err := tcp.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		br := bufio.NewReader(c)
		var g [2]byte
		io.ReadFull(br, g[:])
		c.Write([]byte{5, 0})
		var h [4]byte
		io.ReadFull(br, h[:])
		rep := []byte{5, 0, 0, 1}
		rep = append(rep, relayAddr.IP...)
		rep = binary.BigEndian.AppendUint16(rep, uint16(relayAddr.Port))
		c.Write(rep)
		io.Copy(io.Discard, br)
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := udp.ReadFromUDP(buf)
			if err != nil {
				return
			}
			udp.WriteToUDP(buf[:n], from)
		}
	}()

	dst := netip.MustParseAddrPort("223.5.5.5:53")
	s, err := dialSocksUDP(tcp.Addr().String(), dst)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write([]byte("ping-payload")); err != nil {
		t.Fatal(err)
	}
	s.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "ping-payload" {
		t.Fatalf("payload %q", got)
	}
}
