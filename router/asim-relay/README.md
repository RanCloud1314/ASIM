# asim-relay — 路由器上的 ePDG 控制面

ePDG 的 IKE/ESP-in-UDP 由 mihomo 的 TUN 承载（见 `router/mihomo/README.md`）。
本程序只做三件事：

1. 手机 WebUI 的 `/status`、`/ping`、`/config`；
2. 把当前节点渲染成 mihomo 的 `config.yaml` 并热重载（启动、保存节点、启停开关时）；
3. `/status` 和 `/ping` 的数字都从 mihomo 现取，不再自己转发任何包。

所以二进制里没有代理内核（约 7MB），也不监听 500/4500。

节点协议：`trojan`、`vless`（vision / ws / grpc / httpupgrade / REALITY）、`hysteria2`。
mihomo 不支持的 vless `xhttp` / `splithttp` 在保存时直接拒绝，不会写出坏配置。

## 为什么在路由器上

系统 IWLAN 的 IKE/IPsec 在**手机基带**里执行，绕过手机 Linux IP 栈（实测见
`m0/M0-B-FINDINGS.md`），手机上的 TUN / ip rule / 代理 App 都抓不到。唯一可行的
是在手机之外拦。

## 工作方式

```
手机基带 --IKE 500 / ESP-in-UDP 4500--> 31.94.76.x
        --(默认网关)--> 路由器 --TUN asim0--> mihomo --trojan/vless/hysteria2--> 节点 --> EE ePDG
```

1. dnsmasq 不覆盖 ePDG 域名，手机解析到真实的 `31.94.76.x`；
2. mihomo 的 TUN（`asim0`）只把 `31.94.76.0/24` 收进来，规则 `IP-CIDR,...,ASIM-UK`；
3. 本程序在启动、`PUT /config`、`/enable` `/disable` 时写 `/etc/asim/mihomo/config.yaml`，
   再调 mihomo 的 `PUT /configs` 热重载。

## 安装（OpenWrt x86_64 已实测）

```sh
# 1) 编译（仓库根目录 .\build.ps1 会同时出 linux/windows 两个二进制）
cd router/asim-relay
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o asim-relay-linux-amd64 .

# 2) 传上去
scp asim-relay-linux-amd64 root@192.168.100.1:/usr/bin/asim-relay
scp relay.json              root@192.168.100.1:/etc/asim/relay.json
scp asim-relay.init         root@192.168.100.1:/etc/init.d/asim-relay
ssh root@192.168.100.1 'chmod 755 /usr/bin/asim-relay /etc/init.d/asim-relay; chmod 600 /etc/asim/relay.json'

# 3) 数据面不在这里：按 router/mihomo/README.md 装 mihomo，
#    并确保 /etc/dnsmasq.conf 里没有把 ePDG 指到路由器的 address= 行。

# 4) 启动（启动时会生成 mihomo 的 config.yaml）
/etc/init.d/asim-relay enable
/etc/init.d/asim-relay restart
```

### 装在 PC 上

Release 提供 `asim-relay-windows-amd64.exe` 和 `asim-relay-linux-amd64`。但数据面在
mihomo，PC 部署同样要在这台机器上跑一份 mihomo 并把手机指过去；这条路没有实测，
路由器才是受支持的目标。`mihomo_config` 留空时 relay 不会写任何文件。

## 配置（relay.json）

```json
{
  "node": {
    "protocol": "vless",
    "host": "1.2.3.4",
    "port": 443,
    "password": "uuid-或密码",
    "sni": "www.example.com",
    "insecure": true,
    "flow": "xtls-rprx-vision",
    "network": "tcp"
  },
  "api_listen": "192.168.100.1:8080",
  "api_token": "换成自己的 token",
  "enabled": true,
  "mihomo_config": "/etc/asim/mihomo/config.yaml",
  "mihomo_api": "http://127.0.0.1:9090",
  "mihomo_secret": ""
}
```

| 字段 | 说明 |
|---|---|
| `protocol` | `trojan`（默认，兼容旧配置）/ `vless` / `hysteria2` |
| `host` / `port` | 节点地址；也可填域名 |
| `password` | trojan 密码、vless UUID、hysteria2 auth |
| `sni` | TLS SNI（hysteria2 为空时用 host） |
| `insecure` | 跳过证书校验；**trojan 默认 true**（兼容旧配置），vless/hysteria2 默认 false |
| `flow` | vless 专用，如 `xtls-rprx-vision`；节点要求时必填 |
| `network` | vless 传输：`tcp`（默认）/ `ws` / `grpc` / `httpupgrade` |
| `path` / `host_header` | ws / httpupgrade 的 path 和 Host 头 |
| `service_name` | gRPC serviceName |
| `reality_public_key` / `reality_short_id` / `fingerprint` | REALITY 参数；填了公钥就走 REALITY |
| `mihomo_config` | mihomo 的 `config.yaml` 路径；留空 = 不生成、不重载 |
| `mihomo_api` | mihomo 控制器地址，默认 `http://127.0.0.1:9090` |
| `mihomo_secret` | 控制器 secret；留空时用 `api_token`（生成的配置没开 secret，此头会被忽略） |

节点字段都能在手机 WebUI「节点配置」里改，保存后热写配置并重载 mihomo。
`api_listen` 改了要重启进程。

## 验证

```sh
# 状态（和手机 WebUI 用同一个 API）
wget -q -O - --header='X-ASIM-Token: <token>' http://192.168.100.1:8080/status

# 延迟：让 mihomo 通过代理取一次 URL
wget -q -O - --header='X-ASIM-Token: <token>' --post-data='' http://192.168.100.1:8080/ping

# 数据面本体
ip route show table all | grep 31.94.76
wget -q -O - --header='Authorization: Bearer <token>' http://127.0.0.1:9090/connections
```

`/status` 的 `flows` 和 `epdg_ips` 直接来自 mihomo 的 `/connections`（只看
`31.94.76.0/24` 的 UDP）。手机恢复 VoWiFi 后应能看到到 `31.94.76.x:500/4500` 的会话。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/status` | `enabled` / `version` / `protocol` / `flows` / `epdg_ips` / `uptime_s` / `node_host` / `node_port`，mihomo 不可达时多一个 `mihomo_error` |
| GET | `/config` | 当前完整配置（含 node） |
| PUT | `/config` | 热更新 `node` / `enabled`，并立即重载 mihomo |
| POST | `/ping` | 让 mihomo 经代理取一次 URL，返回 `{"ok":true,"ms":N}` |
| POST | `/enable` / `/disable` | 开/关 mihomo 的 TUN（`disable` 后 ePDG 直接走路由器 WAN） |

除 `/enable` `/disable` 外都需要 `X-ASIM-Token` 头，值等于 `relay.json` 的 `api_token`。
配置文件权限 600。

`PUT /config` 保存后会写 mihomo 配置并热重载；重载失败时响应里多一个 `mihomo_error`，
节点是否真正生效以该字段为准。

## 已知限制

- 只处理 ePDG 的 `31.94.76.0/24`，不是通用代理；换网段要改 `mihomo.go` 的 `mihomoCIDR`。
- 依赖 mihomo 在跑：它挂了 `/status` 仍是 200，但 `flows` 为空并带 `mihomo_error`。
- `/ping` 测的是「mihomo → 节点」的 HTTP 往返，不是 IKE 的 UDP 时延；数值只用来比较节点好坏。
- `external-controller` 只绑 `127.0.0.1:9090` 且没有 `secret`，不要暴露到 LAN。
- Hysteria2 的 obfs / 端口跳跃未实现（mihomo 侧支持，本程序不写这两项）。
- 手机侧 IWLAN 注册状态可能被 AP 缓存；重连 WiFi 后仍卡死就只能重启手机。
