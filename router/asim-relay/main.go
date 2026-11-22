// asim-relay: 把手机基带发往本机的 IKE/ESP-in-UDP 转发到 upstream 节点
// （trojan / vless / hysteria2，见 node.go）。
//
// 工作方式：路由器 dnsmasq 把 ePDG 域名应答成本机 LAN IP，手机基带就会把
// IKE 发到这里监听的 500/4500 端口；本程序为每个客户端流开一条上游 UDP
// 会话转给真实 ePDG，回包按原路送回。只有 ePDG 这一个域名的 UDP 受影响。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type Config struct {
	Domain     string `json:"domain"`
	ListenAddr string `json:"listen_addr"`
	ListenPort []int  `json:"listen_ports"`
	Node       Node   `json:"node"`
	APIListen  string `json:"api_listen"`
	APIToken   string `json:"api_token"`
	Enabled    bool   `json:"enabled"`
}

var cfgPath string

const (
	idleTimeout = 180 * time.Second
	version     = "0.3.0"
)

type flow struct {
	client   *net.UDPAddr
	port     int
	conn     *net.UDPConn
	ch       chan []byte
	stop     chan struct{}
	once     sync.Once
	lastSeen atomic.Int64
	dst      string
}

type relay struct {
	cfg   Config
	cfgMu sync.RWMutex // guards cfg.Domain / cfg.Node against /config updates
	mu    sync.Mutex
	flows map[string]*flow
	epdg  atomic.Pointer[[]netip.Addr]
	on    atomic.Bool
	drops atomic.Uint64
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

// ---------- ePDG 解析（走 DoH，避开本地 dnsmasq 的覆盖） ----------

type dohResp struct {
	Answer []struct {
		Data string `json:"data"`
		Type int    `json:"type"`
	} `json:"Answer"`
}

func resolveDoH(name string) ([]netip.Addr, error) {
	u := "https://dns.alidns.com/resolve?name=" + name + "&type=A"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	req.Header.Set("Accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var dr dohResp
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&dr); err != nil {
		return nil, err
	}
	var out []netip.Addr
	for _, a := range dr.Answer {
		if a.Type != 1 {
			continue
		}
		if ip, err := netip.ParseAddr(a.Data); err == nil {
			out = append(out, ip)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no A record")
	}
	return out, nil
}

func (r *relay) currentEpdg() (netip.Addr, bool) {
	p := r.epdg.Load()
	if p == nil || len(*p) == 0 {
		return netip.Addr{}, false
	}
	return (*p)[0], true
}

func (r *relay) resolver() {
	for {
		r.resolveOnce()
		time.Sleep(10 * time.Minute)
	}
}

func (r *relay) resolveOnce() {
	r.cfgMu.RLock()
	domain := r.cfg.Domain
	r.cfgMu.RUnlock()
	ips, err := resolveDoH(domain)
	if err != nil {
		log.Printf("resolve %s: %v", domain, err)
		return
	}
	if p := r.epdg.Load(); p == nil || !sameAddrs(*p, ips) {
		log.Printf("epdg %s -> %v", domain, ips)
		r.epdg.Store(&ips)
		r.killFlows()
	}
}

func sameAddrs(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- 转发 ----------

func (r *relay) serveUDP(port int) {
	la, err := netip.ParseAddr(r.cfg.ListenAddr)
	if err != nil {
		log.Fatalf("listen_addr %q: %v", r.cfg.ListenAddr, err)
	}
	conn, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.AddrPortFrom(la, uint16(port))))
	if err != nil {
		log.Fatalf("listen udp %d: %v", port, err)
	}
	log.Printf("listening udp %s:%d", r.cfg.ListenAddr, port)
	buf := make([]byte, 64*1024)
	for {
		n, client, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("read udp %d: %v", port, err)
			continue
		}
		if !r.on.Load() {
			r.drops.Add(1)
			continue
		}
		p := make([]byte, n)
		copy(p, buf[:n])
		r.dispatch(conn, client, port, p)
	}
}

func (r *relay) dispatch(conn *net.UDPConn, client *net.UDPAddr, port int, payload []byte) {
	key := fmt.Sprintf("%s|%d", client.String(), port)
	r.mu.Lock()
	f := r.flows[key]
	if f == nil {
		f = &flow{client: client, port: port, conn: conn, ch: make(chan []byte, 32), stop: make(chan struct{})}
		r.flows[key] = f
		go r.runFlow(key, f)
	}
	r.mu.Unlock()
	f.lastSeen.Store(time.Now().Unix())
	select {
	case <-f.stop:
		r.drops.Add(1)
	case f.ch <- payload:
	default:
		r.drops.Add(1) // 队列满就丢，IKE 自己会重传
	}
}

func (r *relay) runFlow(key string, f *flow) {
	defer r.dropFlow(key)
	ip, ok := r.currentEpdg()
	if !ok {
		log.Printf("flow %s: no epdg ip yet", key)
		return
	}
	dst := netip.AddrPortFrom(ip, uint16(f.port))
	r.mu.Lock()
	f.dst = dst.String()
	r.mu.Unlock()
	r.cfgMu.RLock()
	node := r.cfg.Node
	r.cfgMu.RUnlock()
	sess, err := dialSession(node, dst)
	if err != nil {
		log.Printf("flow %s: dial node: %v", key, err)
		return
	}
	log.Printf("flow %s -> %s via %s (%s)", key, dst, node.Host, node.proto())
	defer sess.Close()
	defer log.Printf("flow %s closed", key)

	go func() {
		for {
			p, err := sess.Read()
			if err != nil {
				sess.Close()
				return
			}
			f.conn.WriteToUDP(p, f.client)
		}
	}()

	for {
		select {
		case p := <-f.ch:
			if err := sess.Write(p); err != nil {
				return
			}
			f.lastSeen.Store(time.Now().Unix())
		case <-time.After(30 * time.Second):
			if time.Since(time.Unix(f.lastSeen.Load(), 0)) > idleTimeout {
				return
			}
		case <-f.stop:
			return
		}
	}
}

func (f *flow) shutdown() {
	f.once.Do(func() { close(f.stop) })
}

func (r *relay) dropFlow(key string) {
	r.mu.Lock()
	if f, ok := r.flows[key]; ok {
		delete(r.flows, key)
		f.shutdown()
	}
	r.mu.Unlock()
}

func (r *relay) killFlows() {
	r.mu.Lock()
	for k, f := range r.flows {
		delete(r.flows, k)
		f.shutdown()
	}
	r.mu.Unlock()
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
		on := req.URL.Path == "/enable"
		r.on.Store(on)
		if !on {
			r.killFlows()
		}
		r.cfgMu.Lock()
		r.cfg.Enabled = on
		r.cfgMu.Unlock()
		r.saveConfig()
		log.Printf("relay %v via api", on)
		r.writeStatus(w)
	default:
		http.NotFound(w, req)
	}
}

// handleConfig serves and hot-applies node/domain/listen settings. Only node
// and domain apply without a restart; listen changes are saved for next start.
func (r *relay) handleConfig(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		r.cfgMu.RLock()
		cfg := r.cfg
		r.cfgMu.RUnlock()
		json.NewEncoder(w).Encode(cfg)
	case http.MethodPut:
		var in struct {
			Node       *Node  `json:"node"`
			Domain     string `json:"domain"`
			Enabled    *bool  `json:"enabled"`
			ListenAddr string `json:"listen_addr"`
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
		restart := false
		r.cfgMu.Lock()
		oldDomain := r.cfg.Domain
		oldNode := r.cfg.Node.signature()
		if in.Node != nil {
			r.cfg.Node = *in.Node
		}
		if in.Domain != "" {
			r.cfg.Domain = in.Domain
		}
		if in.Enabled != nil {
			r.cfg.Enabled = *in.Enabled
		}
		if in.ListenAddr != "" {
			if in.ListenAddr != r.cfg.ListenAddr {
				restart = true
			}
			r.cfg.ListenAddr = in.ListenAddr
		}
		newNode := r.cfg.Node.signature()
		domain := r.cfg.Domain
		enabled := r.cfg.Enabled
		r.cfgMu.Unlock()

		if newNode != oldNode {
			r.killFlows()
			r.closeEngines()
			log.Printf("node updated via api: %s", r.cfgHost())
		}
		if domain != oldDomain {
			r.epdg.Store(nil)
			r.killFlows()
			go r.resolveOnce()
			log.Printf("domain updated via api: %s", domain)
		}
		r.on.Store(enabled)
		if !enabled {
			r.killFlows()
		}
		r.saveConfig()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "restart_required": restart})
	default:
		http.Error(w, "GET or PUT", http.StatusMethodNotAllowed)
	}
}

func (r *relay) cfgHost() string {
	r.cfgMu.RLock()
	defer r.cfgMu.RUnlock()
	return r.cfg.Node.addr()
}

func (r *relay) closeEngines() {
	xrayEng.Close()
	hyEng.Close()
}

// handlePing does one real UDP round trip so the UI latency reflects the
// actual node handshake, not just TCP reachability.
func (r *relay) handlePing(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Node *Node `json:"node"`
	}
	json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&in)
	r.cfgMu.RLock()
	node := r.cfg.Node
	r.cfgMu.RUnlock()
	if in.Node != nil {
		node = *in.Node
	}
	ms, err := probe(node)
	if err != nil {
		log.Printf("ping %s failed: %v", node.addr(), err)
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "ms": ms})
}

func (r *relay) writeStatus(w http.ResponseWriter) {
	type flowInfo struct {
		Client   string `json:"client"`
		Dest     string `json:"dest"`
		LastSeen int    `json:"last_seen_s"`
	}
	var ips []string
	if p := r.epdg.Load(); p != nil {
		for _, a := range *p {
			ips = append(ips, a.String())
		}
	}
	flows := []flowInfo{}
	r.mu.Lock()
	for k, f := range r.flows {
		flows = append(flows, flowInfo{k, f.dst, int(time.Since(time.Unix(f.lastSeen.Load(), 0)).Seconds())})
	}
	r.mu.Unlock()
	sort.Slice(flows, func(i, j int) bool { return flows[i].Client < flows[j].Client })
	r.cfgMu.RLock()
	cfg := r.cfg
	r.cfgMu.RUnlock()
	json.NewEncoder(w).Encode(map[string]any{
		"enabled":   r.on.Load(),
		"version":   version,
		"domain":    cfg.Domain,
		"protocol":  cfg.Node.proto(),
		"epdg_ips":  ips,
		"uptime_s":  int(time.Since(r.start).Seconds()),
		"drops":     r.drops.Load(),
		"flows":     flows,
		"node_host": cfg.Node.Host,
		"node_port": cfg.Node.Port,
	})
}

// ---------- main ----------

func main() {
	flag.StringVar(&cfgPath, "c", "/etc/asim/relay.json", "config path")
	flag.Parse()

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		log.Fatalf("config %s: %v", cfgPath, err)
	}
	r := &relay{cfg: cfg, flows: map[string]*flow{}, start: time.Now()}
	r.on.Store(cfg.Enabled)

	go r.resolver()
	for _, p := range cfg.ListenPort {
		go r.serveUDP(p)
	}

	srv := &http.Server{Addr: cfg.APIListen, Handler: http.HandlerFunc(r.api)}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api: %v", err)
		}
	}()
	log.Printf("asim-relay up: domain=%s api=%s enabled=%v", cfg.Domain, cfg.APIListen, cfg.Enabled)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	log.Printf("shutting down")
	srv.Close()
	r.killFlows()
}
