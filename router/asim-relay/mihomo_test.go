package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustRender(t *testing.T, n Node) string {
	t.Helper()
	b, err := renderMihomo(n)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(b)
}

func TestRenderTrojanEscaping(t *testing.T) {
	out := mustRender(t, Node{Host: "node.example", Port: 56007, Password: "p'w: #1", SNI: "iosapps.itunes.apple.com"})
	for _, want := range []string{
		"type: trojan",
		"server: node.example",
		"port: 56007",
		"password: 'p''w: #1'",
		"udp: true",
		"skip-cert-verify: true",
		"device: asim0",
		`route-address: ["31.94.76.0/24"]`,
		"IP-CIDR,31.94.76.0/24,ASIM-UK,no-resolve",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderHysteria2(t *testing.T) {
	out := mustRender(t, Node{Protocol: "hysteria2", Host: "h.example", Port: 443, Password: "auth", SNI: "s.example"})
	for _, want := range []string{"type: hysteria2", "password: auth", "sni: s.example"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "uuid:") {
		t.Error("hysteria2 must not emit uuid")
	}
}

func TestRenderVLESSReality(t *testing.T) {
	out := mustRender(t, Node{
		Protocol: "vless", Host: "h", Port: 443, Password: "uuid-1", SNI: "s.com",
		Flow: "xtls-rprx-vision", Network: "ws", Path: "/p", HostHeader: "cdn.com",
		RealityPublicKey: "pk", RealityShortID: "sid",
	})
	for _, want := range []string{
		"type: vless", "uuid: uuid-1", "tls: true", "servername: s.com",
		"flow: xtls-rprx-vision", "network: ws", "path: /p", "Host: cdn.com",
		"public-key: pk", "short-id: sid", "client-fingerprint: chrome",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRenderUnsupportedNetwork(t *testing.T) {
	for _, netw := range []string{"xhttp", "splithttp"} {
		if _, err := renderMihomo(Node{Protocol: "vless", Host: "h", Port: 1, Password: "u", Network: netw}); err == nil {
			t.Errorf("%s: want error", netw)
		}
	}
}

func TestRenderRulesScope(t *testing.T) {
	out := mustRender(t, Node{Host: "h", Port: 1, Password: "p"})
	if !strings.Contains(out, "MATCH,DIRECT") {
		t.Error("last rule must be MATCH,DIRECT")
	}
	if strings.Contains(out, "0.0.0.0/1") || strings.Contains(out, "128.0.0.0/1") {
		t.Error("must not route the default prefix into tun")
	}
}

func TestYamlStr(t *testing.T) {
	cases := map[string]string{
		"plain": "plain",
		"a b":   "'a b'",
		"p'w":   "'p''w'",
		"a:b":   "'a:b'",
		"#hash": "'#hash'",
		"":      "''",
	}
	for in, want := range cases {
		if got := yamlStr(in); got != want {
			t.Errorf("yamlStr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSyncMihomo(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/configs" || r.URL.Query().Get("force") != "true" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Path string `json:"path"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotPath = body.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := syncMihomo(path, srv.URL, "tok", Node{Host: "h", Port: 1, Password: "p"}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if gotAuth != "Bearer tok" || gotPath != path {
		t.Errorf("auth/path = %q/%q", gotAuth, gotPath)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), "type: trojan") {
		t.Errorf("config not written: %v", err)
	}
}

func TestSyncMihomoReloadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "reloading", http.StatusInternalServerError)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "config.yaml")
	err := syncMihomo(path, srv.URL, "tok", Node{Host: "h", Port: 1, Password: "p"})
	if err == nil || !strings.Contains(err.Error(), "reload") {
		t.Fatalf("want reload error, got %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("config must still be written: %v", statErr)
	}
}

func TestHandleConfigReportsMihomoError(t *testing.T) {
	oldPath := cfgPath
	cfgPath = filepath.Join(t.TempDir(), "relay.json")
	defer func() { cfgPath = oldPath }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer srv.Close()

	r := &relay{cfg: Config{
		APIToken: "t", Enabled: true,
		MihomoConfig: filepath.Join(t.TempDir(), "config.yaml"),
		MihomoAPI:    srv.URL, MihomoSecret: "s",
	}}
	req := httptest.NewRequest(http.MethodPut, "/config",
		strings.NewReader(`{"node":{"host":"h","port":1,"password":"p"}}`))
	w := httptest.NewRecorder()
	r.handleConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "mihomo_error") {
		t.Fatalf("reload error must be surfaced: %s", w.Body.String())
	}
	if _, err := os.Stat(r.cfg.MihomoConfig); err != nil {
		t.Fatalf("config must be written before reload: %v", err)
	}
}
