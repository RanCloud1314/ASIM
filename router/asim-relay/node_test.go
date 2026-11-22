package main

import (
	"strings"
	"testing"
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
		{"vless bad network", Node{Protocol: "vless", Host: "h", Port: 1, Password: "u", Network: "kcp"}, "kcp"},
		{"vless xhttp", Node{Protocol: "vless", Host: "h", Port: 1, Password: "u", Network: "xhttp"}, "xhttp"},
		{"vless splithttp", Node{Protocol: "vless", Host: "h", Port: 1, Password: "u", Network: "splithttp"}, "splithttp"},
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
