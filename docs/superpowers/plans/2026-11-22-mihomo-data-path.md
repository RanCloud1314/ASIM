# mihomo 数据面切换 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让路由器上的 mihomo 接管 ePDG 的 UDP 500/4500，asim-relay 退成只负责配置与状态的控制面。

**Architecture:** 手机经 dnsmasq 解析到真实 `31.94.76.x`，路由器把该 `/24` 路由进 mihomo 的 TUN；mihomo 用 trojan/UDP 送到英国节点。relay 保留全部 API，`PUT /config` 时把节点渲染成 mihomo 的 `config.yaml` 并热重载。

**Tech Stack:** Go 1.27（`net/http`、`httptest`，不引新依赖）、OpenWrt 25.12 procd/fw4/dnsmasq、mihomo v1.19.34。

**Spec:** `docs/superpowers/specs/2026-11-22-mihomo-data-path-design.md`

## Global Constraints

- 不新增 Go 依赖；YAML 手写渲染。
- blast radius 只能是 ePDG：规则最后一条必须是 `MATCH,DIRECT`，进 tun 的只有 `31.94.76.0/24`。
- 不动手机模块与 `asimd`，不动 slot 0。
- 路由器没有 `curl`；API 用 `wget -q -O - --header=...`。
- 线上节点是 `pokemon888-go.pikaholo.com:56007`（仓库旧值是 `56006`，以现场为准）。
- mihomo 固定 `v1.19.34`，资产 `mihomo-linux-amd64-compatible-v1.19.34.gz`。
- 验收：跨过第 30 分钟重注册且连续 ≥45 分钟无 `403 Registration_Error14`。

## Review Focus

1. 节点密码/SNI 含 `'`、`:`、`#`、空格时，渲染出的 YAML 仍能被 mihomo 解析 —— Task 1 的 `TestYamlStr` 与 `TestRenderTrojanEscaping`。
2. 各协议字段名不能串（trojan `password`、vless `uuid`/`servername`、hysteria2 `password`/`sni`），否则 mihomo 拒载 —— Task 1 的三种协议渲染断言。
3. mihomo 正在重启时热重载失败：配置文件必须已写成功，错误必须出现在 `PUT /config` 的响应里 —— Task 1 的 `TestSyncMihomoReloadFailure` 与 `TestHandleConfigReportsMihomoError`。
4. 路由不得越界到其他流量 —— Task 1 的 `TestRenderRulesScope`。
5. mihomo 不支持的 vless 网络（`xhttp`/`splithttp`）必须拒绝写入，不能留下坏配置导致 mihomo 下次起不来 —— Task 1 的 `TestRenderUnsupportedNetwork`。

---

### Task 1: mihomo 配置渲染与热同步

**Files:**
- Create: `router/asim-relay/mihomo.go`
- Test: `router/asim-relay/mihomo_test.go`
- Modify: `router/asim-relay/main.go`（`Config` 结构体、`main` 启动、`handleConfig` PUT 分支）
- Modify: `router/asim-relay/relay.json`

**Interfaces:**
- Produces:
  - `func renderMihomo(n Node) ([]byte, error)` —— 完整 `config.yaml`；`vless` 的 `xhttp`/`splithttp` 与未知协议返回 error。
  - `func yamlStr(s string) string` —— 需要时输出单引号标量并把 `'` 写成 `''`。
  - `func syncMihomo(path, api, secret string, n Node) error` —— 原子写 `path`（0600）后向 `api/configs?force=true` 发 `PUT {"path":path}`；写成功但重载失败时返回 `reload: ...` 且文件保留。
- Consumes: `Node`、`Node.skipVerify()`、`Node.proto()`、`Node.network()`（`node.go`）。

**映射表（实现依据）**

| Node 字段 | mihomo |
|---|---|
| `proto()==trojan` | `type: trojan`、`password`、`sni`、`skip-cert-verify` |
| `proto()==hysteria2` | `type: hysteria2`、`password`、`sni`、`skip-cert-verify` |
| `proto()==vless` tcp | `type: vless`、`uuid`、`tls: true`、`servername`、`flow`、`encryption`、`client-fingerprint`、`skip-cert-verify` |
| `network()==ws` | `network: ws`、`ws-opts.path`、`ws-opts.headers.Host`（来自 `HostHeader`） |
| `network()==httpupgrade` | `network: ws` + `ws-opts.v2ray-http-upgrade: true` |
| `network()==grpc` | `network: grpc`、`grpc-opts.grpc-service-name` |
| `network()==xhttp` / `splithttp` | 返回 error：mihomo 这条路径未验证，别写坏配置 |
| `RealityPublicKey` | `reality-opts.public-key`、`reality-opts.short-id` |
| `Fingerprint` 或存在 reality | `client-fingerprint`（缺省 `chrome`） |

固定部分：`mode: rule`、`log-level: info`、`ipv6: false`、`allow-lan: false`、`mixed-port: 0`、
`external-controller: 127.0.0.1:9090`、`secret: <yamlStr(secret)>`、`dns.enable: false`、
`tun{enable,stack: system,device: asim0,auto-route,auto-detect-interface,route-address: ["31.94.76.0/24"],dns-hijack: []}`、
`rules: ["IP-CIDR,31.94.76.0/24,ASIM-UK,no-resolve", "MATCH,DIRECT"]`。

- [ ] **Step 1: 写失败测试 `router/asim-relay/mihomo_test.go`**

```go
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
		"plain":     "plain",
		"a b":       "'a b'",
		"p'w":       "'p''w'",
		"a:b":       "'a:b'",
		"#hash":     "'#hash'",
		"":          "''",
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
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd router/asim-relay; go test ./... -run 'Render|YamlStr|SyncMihomo|HandleConfig' -v`
Expected: 编译失败，`undefined: renderMihomo`。

- [ ] **Step 3: 新建 `router/asim-relay/mihomo.go`**

实现三个接口。要点：

- `renderMihomo` 用 `strings.Builder` + `fmt.Fprintf` 按上面的固定部分逐行输出；协议块按映射表输出；`vless` 先调 `n.network()`，遇到 `xhttp`/`splithttp` 返回 `fmt.Errorf("mihomo 未接 vless %s，请改用 ws/grpc/tcp", netw)`。
- `yamlStr`：值匹配 `^[A-Za-z0-9._@/-]+$` 且非空时原样返回，否则 `'` + `strings.ReplaceAll(s, "'", "''")` + `'`。
- `syncMihomo`：`renderMihomo` → 写 `path+".tmp"`（0600）→ `os.Rename` → `http.Client{Timeout: 5*time.Second}` 发 PUT，URL `api + "/configs?force=true"`，`Authorization: Bearer <secret>`（secret 为空则不发），body `{"path":path}`；非 2xx 返回 `fmt.Errorf("reload: %s", body)`。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd router/asim-relay; go test ./... -run 'Render|YamlStr|SyncMihomo|HandleConfig' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 接到 `main.go`**

1. `Config` 加三个字段（`json` 名与下面一致）：

```go
MihomoConfig string `json:"mihomo_config"`
MihomoAPI    string `json:"mihomo_api"`
MihomoSecret string `json:"mihomo_secret"`
```

2. `main()` 在 `log.Printf("asim-relay up: ...")` 之后加：`MihomoConfig` 非空时调用 `syncMihomo`，失败只 `log.Printf("mihomo: %v", err)`，不退出。
3. `handleConfig` PUT 分支在 `r.saveConfig()` 之后、写响应之前取一份配置并调用 `syncMihomo`；错误放进响应：`"mihomo_error": err.Error()`（无错则不放该键，保持 `{"ok":true,"restart_required":...}` 原样）。secret 为空时用 `cfg.APIToken`。

- [ ] **Step 6: 更新 `relay.json`**

`port` 改成 `56007`（与路由器现场一致），并加：

```json
"mihomo_config": "/etc/asim/mihomo/config.yaml",
"mihomo_api": "http://127.0.0.1:9090",
"mihomo_secret": "",
```

- [ ] **Step 7: 全量测试与编译**

Run: `cd router/asim-relay; go vet ./...; go test ./...; $env:GOOS="linux"; $env:GOARCH="amd64"; go build -o asim-relay-linux-amd64 .`
Expected: vet 无输出、全部 PASS、生成二进制。

- [ ] **Step 8: 提交**

```bash
git add router/asim-relay/mihomo.go router/asim-relay/mihomo_test.go router/asim-relay/main.go router/asim-relay/relay.json
git commit -m "feat(relay): render mihomo config on node update and reload it"
```

---

### Task 2: 路由器侧服务文件

**Files:**
- Create: `router/mihomo/mihomo.init`
- Create: `router/mihomo/README.md`

**Interfaces:**
- Produces: `/etc/init.d/mihomo`（procd 管理 `/usr/bin/mihomo -d /etc/asim/mihomo`）；README 给出安装、验证、回滚命令。

- [ ] **Step 1: 写 `router/mihomo/mihomo.init`**

```sh
#!/bin/sh /etc/rc.common
# mihomo 只接管 ePDG 的 31.94.76.0/24；配置由 asim-relay 生成。
START=99
STOP=10
USE_PROCD=1

start_service() {
	procd_open_instance
	procd_set_param command /usr/bin/mihomo -d /etc/asim/mihomo
	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
```

- [ ] **Step 2: 写 `router/mihomo/README.md`**

必须包含：二进制来源（`mihomo-linux-amd64-compatible-v1.19.34.gz`）、部署路径
（`/usr/bin/mihomo`、`/etc/asim/mihomo/`、`/etc/init.d/mihomo`）、firewall 把 `asim0`
加进 lan zone、删 dnsmasq 覆盖行的命令、`ip route | grep 31.94.76` 与
`wget -q -O - --header='Authorization: Bearer <token>' http://127.0.0.1:9090/version`
两条验证、以及 30 秒回滚（停 mihomo → 恢复 dnsmasq 行 → 重启 dnsmasq → 重启手机）。

- [ ] **Step 3: 提交**

```bash
git add router/mihomo/
git commit -m "feat(router): add mihomo init script and install notes"
```

---

### Task 3: 部署到路由器并冒烟

**Files:** 无仓库改动；按 Task 2 的 README 操作。

**Interfaces:**
- Consumes: `router/asim-relay/asim-relay-linux-amd64`、`relay.json`、`router/mihomo/mihomo.init`。

- [ ] **Step 1: PC 侧取二进制**

```powershell
$u='https://github.com/MetaCubeX/mihomo/releases/download/v1.19.34/mihomo-linux-amd64-compatible-v1.19.34.gz'
Invoke-WebRequest $u -OutFile $env:TEMP\mihomo.gz
gzip -d $env:TEMP\mihomo.gz
Get-FileHash $env:TEMP\mihomo -Algorithm SHA256
```

Expected: 文件约 15–40 MB，`Get-FileHash` 有输出。

- [ ] **Step 2: 确认 TUN 可用**

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "ls -l /dev/net/tun; grep -c '^tun ' /proc/modules"
```

Expected: `/dev/net/tun` 存在。不存在则 `apk add kmod-tun` 后重试；仍不存在就停下来，不要继续。

- [ ] **Step 3: 推送并安装**

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtsend.py $env:TEMP\mihomo /usr/bin/mihomo
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtsend.py router\mihomo\mihomo.init /etc/init.d/mihomo
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "mkdir -p /etc/asim/mihomo; chmod 755 /usr/bin/mihomo /etc/init.d/mihomo"
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtsend.py router\asim-relay\asim-relay-linux-amd64 /usr/bin/asim-relay
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtsend.py router\asim-relay\relay.json /etc/asim/relay.json
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "chmod 755 /usr/bin/asim-relay; chmod 600 /etc/asim/relay.json; /etc/init.d/asim-relay restart"
```

Expected: relay 重启后 `/etc/asim/mihomo/config.yaml` 出现（relay 启动时生成）。

- [ ] **Step 4: 启动 mihomo 并验证**

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "/etc/init.d/mihomo enable; /etc/init.d/mihomo restart; sleep 3; logread | grep -i mihomo | tail -5; ip route | grep 31.94.76"
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "wget -q -O - --header='Authorization: Bearer 3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b' http://127.0.0.1:9090/version"
```

Expected: 日志无 `initial configuration failed`；`31.94.76.0/24 dev asim0` 出现；`/version` 返回 `{"meta":true,...}`。

- [ ] **Step 5: firewall 放行回程**

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "uci show firewall | grep -n asim0"
```

若没有 `asim0`，把它加进 lan zone 的设备列表（`uci add_list firewall.@zone[0].device='asim0'; uci commit firewall; /etc/init.d/firewall reload`），再确认：

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "nft list ruleset | grep -c asim0"
```

Expected: 计数 > 0。

- [ ] **Step 6: 删 dnsmasq 覆盖行**

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "grep -n 3gppnetwork /etc/dnsmasq.conf /etc/config/dhcp"
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "sed -i '/3gppnetwork.org/d' /etc/dnsmasq.conf; /etc/init.d/dnsmasq restart; nslookup epdg.epc.mnc033.mcc234.pub.3gppnetwork.org 127.0.0.1 | tail -6"
```

Expected: 解析结果是 `31.94.76.x`，不再是 `192.168.100.1`。

- [ ] **Step 7: 手机重启并确认注册**

```powershell
adb reboot; Start-Sleep 180
adb shell "dumpsys telephony.registry | grep -oE 'voice=[A-Z_]+|iwlan=[A-Z_]+' | tail -4"
adb shell "ip -br addr show rmnet_data1"
```

Expected: `voice=IN_SERVICE`、`iwlan=HOME`；`rmnet_data1` 有 `2a00:23ee:` 地址。

- [ ] **Step 8: 确认数据面走 mihomo**

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "wget -q -O - --header='Authorization: Bearer 3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b' http://127.0.0.1:9090/connections | grep -o '31.94.76.[0-9]*' | sort | uniq -c"
```

Expected: 有 `31.94.76.x` 的会话（UDP 500/4500）。没有就停下排查：先看 `ip route`，再开 `tun.auto-redirect: true` 重试。

---

### Task 4: 45 分钟验收窗口

**Files:** 无；记录写进 Task 5 的文档。

- [ ] **Step 1: 记录基线**

```powershell
$t0=Get-Date; adb shell "logcat -d | grep -c Registration_Error14"
```

- [ ] **Step 2: 开监控**

用现成的 `C:\Users\22938\AppData\Local\Temp\opencode\rt\watch2.ps1`（每 30 秒采样 voice/iwlan），同时挂手机持久 logcat：

```powershell
adb shell 'su -c "rm -f /data/local/tmp/asim-all.log*; setsid logcat -b all -v time -f /data/local/tmp/asim-all.log -r 4096 -n 8 </dev/null >/dev/null 2>&1 &"'
```

- [ ] **Step 3: 盯到第 35 分钟**

Expected: 全程 `voice=IN_SERVICE`、`iwlan=HOME`；第 30 分钟前后无 `403`、无 `OUT_OF_SERVICE`；
mihomo `/connections` 里 ePDG 会话一直在。

- [ ] **Step 4: 判卷**

```powershell
adb pull /data/local/tmp/asim-all.log
Select-String -Path .\asim-all.log -Pattern 'Registration_Error14|OUT_OF_SERVICE' | Measure-Object
adb shell "dumpsys telephony.registry | grep -oE 'voice=[A-Z_]+|iwlan=[A-Z_]+' | tail -4"
```

合格：`Registration_Error14` 计数与 Step 1 基线相同，且仍是 `IN_SERVICE`/`HOME`。

不合格：**不要切回 relay 试运气**。先抓 WAN 口看上游会话形态，把结论写进 `docs/VOWIFI-DEBUG.md`（H4 排除）。

---

### Task 5: 文档收口

**Files:**
- Modify: `docs/HANDOFF.md`（链路图、当前状态、文件地图）
- Modify: `docs/VOWIFI-DEBUG.md`（结论：已改 mihomo；H4 的判卷结果）
- Modify: `router/asim-relay/README.md`（开头说明数据面已交给 mihomo，relay 只负责 API 与配置生成）

- [ ] **Step 1: 更新三份文档**

必须写清：新链路、`/status` 的 `flows` 恒为空、`/enable` `/disable` 不再影响数据面、
节点改动经 WebUI 落到 mihomo、vless `xhttp/splithttp` 暂不支持、回滚步骤。

- [ ] **Step 2: 提交**

```bash
git add docs/HANDOFF.md docs/VOWIFI-DEBUG.md router/asim-relay/README.md
git commit -m "docs: mihomo becomes the ePDG data path"
```

---

## 执行顺序与依赖

Task 1 → Task 2 → Task 3 → Task 4 → Task 5。Task 3 的 Step 6/7 会中断手机服务，
放在最后一步做；任何一步失败都按 Task 2 README 的回滚走，不要在现场改代码。
