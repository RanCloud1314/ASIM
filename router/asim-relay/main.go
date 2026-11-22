// asim-relay: control plane for the ePDG data path.
//
// The IKE/ESP-in-UDP flow is carried by mihomo (TUN asim0, router/mihomo);
// this program only serves the phone WebUI (/status, /ping, /config) and keeps
// mihomo's config.yaml in sync with the saved node.
package main

import (
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

type Config struct {
	Node      Node   `json:"node"`
	APIListen string `json:"api_listen"`
	APIToken  string `json:"api_token"`
	Enabled   bool   `json:"enabled"`
	// MihomoConfig is the config.yaml mihomo runs; empty disables the sync.
	MihomoConfig string `json:"mihomo_config"`
	MihomoAPI    string `json:"mihomo_api"`
	MihomoSecret string `json:"mihomo_secret"`
}

var cfgPath string

const version = "0.4.1"

type relay struct {
	cfg   Config
	cfgMu sync.RWMutex // guards cfg against /config and /enable updates
	start time.Time
}

// ---------- config ----------

func loadConfig(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(b, &c)
	return c, err
}

func (r *relay) saveConfig() {
	r.cfgMu.RLock()
	b, _ := json.MarshalIndent(r.cfg, "", "  ")
	r.cfgMu.RUnlock()
	if err := os.WriteFile(cfgPath, b, 0600); err != nil {
		log.Printf("save config: %v", err)
	}
}

func (c Config) secret() string {
	if c.MihomoSecret != "" {
		return c.MihomoSecret
	}
	return c.APIToken
}

// syncConfig pushes the saved node to mihomo. A failure is logged, not fatal:
// mihomo keeps running its previous config and the API reports the error.
func (r *relay) syncConfig() error {
	r.cfgMu.RLock()
	cfg := r.cfg
	r.cfgMu.RUnlock()
	if cfg.MihomoConfig == "" {
		return nil
	}
	return syncMihomo(cfg.MihomoConfig, cfg.MihomoAPI, cfg.secret(), cfg.Node, cfg.Enabled)
}

// ---------- HTTP API（给手机 WebUI 用） ----------

func (r *relay) api(w http.ResponseWriter, req *http.Request) {
	r.cfgMu.RLock()
	token := r.cfg.APIToken
	r.cfgMu.RUnlock()
	if token != "" && req.Header.Get("X-ASIM-Token") != token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch req.URL.Path {
	case "/status":
		r.writeStatus(w)
	case "/config":
		r.handleConfig(w, req)
	case "/ping":
		r.handlePing(w, req)
	case "/enable", "/disable":
		if req.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		r.cfgMu.Lock()
		r.cfg.Enabled = req.URL.Path == "/enable"
		r.cfgMu.Unlock()
		r.saveConfig()
		err := r.syncConfig()
		if err != nil {
			log.Printf("mihomo: %v", err)
		}
		r.writeStatus(w)
	default:
		http.NotFound(w, req)
	}
}

// handleConfig serves and hot-applies the node. Saving re-renders mihomo's
// config and asks it to reload.
func (r *relay) handleConfig(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.cfgMu.RLock()
		cfg := r.cfg
		r.cfgMu.RUnlock()
		json.NewEncoder(w).Encode(cfg)
	case http.MethodPut:
		var in struct {
			Node    *Node `json:"node"`
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if in.Node != nil {
			if err := in.Node.validate(); err != nil {
				http.Error(w, "node: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		r.cfgMu.Lock()
		if in.Node != nil {
			r.cfg.Node = *in.Node
		}
		if in.Enabled != nil {
			r.cfg.Enabled = *in.Enabled
		}
		r.cfgMu.Unlock()
		r.saveConfig()
		resp := map[string]any{"ok": true}
		if err := r.syncConfig(); err != nil {
			log.Printf("mihomo: %v", err)
			resp["mihomo_error"] = err.Error()
		}
		json.NewEncoder(w).Encode(resp)
	default:
		http.Error(w, "GET or PUT", http.StatusMethodNotAllowed)
	}
}

// handlePing asks mihomo to fetch a URL through the proxy, so the number
// reflects the live data path rather than raw TCP reachability.
func (r *relay) handlePing(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	r.cfgMu.RLock()
	api, secret := r.cfg.MihomoAPI, r.cfg.secret()
	r.cfgMu.RUnlock()
	ms, err := mihomoDelay(api, secret)
	if err != nil {
		log.Printf("ping: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "ms": ms})
}

func (r *relay) writeStatus(w http.ResponseWriter) {
	r.cfgMu.RLock()
	cfg := r.cfg
	r.cfgMu.RUnlock()
	flows, ips, mErr := mihomoSessions(cfg.MihomoAPI, cfg.secret())
	resp := map[string]any{
		"enabled":   cfg.Enabled,
		"version":   version,
		"protocol":  cfg.Node.proto(),
		"uptime_s":  int(time.Since(r.start).Seconds()),
		"flows":     flows,
		"epdg_ips":  ips,
		"node_host": cfg.Node.Host,
		"node_port": cfg.Node.Port,
	}
	if mErr != nil {
		resp["mihomo_error"] = mErr.Error()
	}
	json.NewEncoder(w).Encode(resp)
}

// ---------- main ----------

func main() {
	flag.StringVar(&cfgPath, "c", "/etc/asim/relay.json", "config path")
	flag.Parse()

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config %s: %v", cfgPath, err)
	}
	r := &relay{cfg: cfg, start: time.Now()}
	if err := r.syncConfig(); err != nil {
		log.Printf("mihomo: %v", err)
	}

	srv := &http.Server{Addr: cfg.APIListen, Handler: http.HandlerFunc(r.api)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api: %v", err)
		}
	}()
	log.Printf("asim-relay up: api=%s enabled=%v mihomo=%s", cfg.APIListen, cfg.Enabled, cfg.MihomoConfig)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	log.Printf("shutting down")
	srv.Close()
}
