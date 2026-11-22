// asim-relay: 把手机基带发往本机的 IKE/ESP-in-UDP 转发到英国 trojan 节点。
//
// 工作方式：路由器 dnsmasq 把 ePDG 域名应答成本机 LAN IP，手机基带就会把
// IKE 发到这里监听的 500/4500 端口；本程序为每个客户端流开一条 trojan
// UDP 会话转给真实 ePDG，回包按原路送回。只有 ePDG 这一个域名的 UDP 受影响。
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
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

type Node struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	SNI      string `json:"sni"`
}

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

const idleTimeout = 180 * time.Second

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
	b, _ := json.MarshalIndent(r.cfg, "", "  ")
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
		if ips, err := resolveDoH(r.cfg.Domain); err != nil {
			log.Printf("resolve %s: %v", r.cfg.Domain, err)
		} else {
			if p := r.epdg.Load(); p == nil || !sameAddrs(*p, ips) {
				log.Printf("epdg %s -> %v", r.cfg.Domain, ips)
				r.epdg.Store(&ips)
				r.killFlows()
			}
		}
		time.Sleep(10 * time.Minute)
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

// ---------- trojan over TLS ----------

type trojanUDP struct {
	conn *tls.Conn
	br   *bufio.Reader
	mu   sync.Mutex
}

func trojanAuth(pw string) string {
	h := sha256.New224()
	h.Write([]byte(pw))
	return hex.EncodeToString(h.Sum(nil))
}

func dialTrojan(n Node, dst netip.AddrPort) (*trojanUDP, error) {
	addr := net.JoinHostPort(n.Host, fmt.Sprint(n.Port))
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	conn, err := tls.DialWithDialer(d, "tcp", addr, &tls.Config{
		ServerName:         n.SNI,
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		return nil, err
	}
	t := &trojanUDP{conn: conn, br: bufio.NewReaderSize(conn, 32*1024)}
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

// readFramePayload：解析 [ATYP][ADDR][PORT][LEN][CRLF][PAYLOAD]，返回 payload
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

func (t *trojanUDP) Close() error { return t.conn.Close() }

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
	sess, err := dialTrojan(r.cfg.Node, dst)
	if err != nil {
		log.Printf("flow %s: dial node: %v", key, err)
		return
	}
	log.Printf("flow %s -> %s via %s", key, dst, r.cfg.Node.Host)
	defer sess.Close()
	defer log.Printf("flow %s closed", key)

	go func() {
		for {
			p, err := sess.readFramePayload()
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
			if err := sess.writeFrame(dst, p); err != nil {
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
	if r.cfg.APIToken != "" && req.Header.Get("X-ASIM-Token") != r.cfg.APIToken {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch req.URL.Path {
	case "/status":
		r.writeStatus(w)
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
		r.cfg.Enabled = on
		r.saveConfig()
		log.Printf("relay %v via api", on)
		r.writeStatus(w)
	default:
		http.NotFound(w, req)
	}
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
	json.NewEncoder(w).Encode(map[string]any{
		"enabled":   r.on.Load(),
		"domain":    r.cfg.Domain,
		"epdg_ips":  ips,
		"uptime_s":  int(time.Since(r.start).Seconds()),
		"drops":     r.drops.Load(),
		"flows":     flows,
		"node_host": r.cfg.Node.Host,
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
