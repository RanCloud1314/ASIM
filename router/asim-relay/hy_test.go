package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"

	hyserver "github.com/apernet/hysteria/core/v2/server"
)

type allowAll struct{}

func (allowAll) Authenticate(addr net.Addr, auth string, tx uint64) (bool, string) {
	return auth == "pw", "test"
}

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestHysteria2RoundTrip runs a real hysteria2 server on loopback so the
// engine's client config (addr/auth/TLS) and the UDP session path are verified
// without depending on a public node.
func TestHysteria2RoundTrip(t *testing.T) {
	echo, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, from, err := echo.ReadFromUDP(b)
			if err != nil {
				return
			}
			echo.WriteToUDP(b[:n], from)
		}
	}()

	pc, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := hyserver.NewServer(&hyserver.Config{
		TLSConfig:     hyserver.TLSConfig{Certificates: []tls.Certificate{selfSigned(t)}},
		Conn:          pc,
		Authenticator: allowAll{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	go srv.Serve()

	port := pc.LocalAddr().(*net.UDPAddr).Port
	insecure := true
	node := Node{
		Protocol: "hysteria2",
		Host:     "127.0.0.1",
		Port:     port,
		Password: "pw",
		SNI:      "localhost",
		Insecure: &insecure,
	}
	dst := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(echo.LocalAddr().(*net.UDPAddr).Port))
	s, err := dialSession(node, dst)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Write([]byte("hy-ping")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hy-ping" {
		t.Fatalf("echo %q on port %s", got, strconv.Itoa(port))
	}
}
