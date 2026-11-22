# asim-relay — 路由器/PC 上的 ePDG 控制面

从 2026-11-22 起，ePDG 的 IKE/ESP-in-UDP 数据面交给 mihomo（见
`router/mihomo/README.md`）；本程序只负责三件事：手机 WebUI 的 `/status`、`/ping`、
`/config`，把节点渲染成 mihomo 的 `config.yaml` 并热重载，以及保留旧的转发实现作对照
（默认不再收到流量，因为 dnsmasq 已不再把 ePDG 指向它）。

节点协议仍支持 `trojan`、`vless`（vision / ws / grpc / REALITY）、`hysteria2`；
mihomo 侧暂不支持 vless 的 `xhttp`/`splithttp`，保存这类节点会被拒绝。

## 为什么需要它

系统 IWLAN 的 IKE/IPsec 在**手机基带**里执行，绕过手机 Linux IP 栈（实测见
`m0/M0-B-FINDINGS.md`），手机上的 TUN / ip rule / 代理 App 都抓不到。唯一可行的
是在手机之外做中继。

## 工作方式

```
手机基带 --IKE 500 / ESP-in-UDP 4500--> 31.94.76.x
        --(默认网关)--> 路由器 --TUN asim0--> mihomo --trojan/vless/hysteria2--> 节点 --> EE ePDG
```

1. dnsmasq 不再覆盖 ePDG 域名，手机解析到真实的 `31.94.76.x`；
2. mihomo 的 TUN（`asim0`）只把 `31.94.76.0/24` 收进来，规则 `IP-CIDR,...,ASIM-UK`；
3. 本程序在启动和每次 `PUT /config` 时把当前节点写成 `/etc/asim/mihomo/config.yaml`，
   再调 mihomo 的 `PUT /configs` 热重载；
4. 旧的 `listen_addr:500/4500` 转发路径保留，但只有 dnsmasq 把域名指回路由器时才会被用到。

## 安装（OpenWrt x86_64 已实测）

```sh
# 1) 编译（在仓库根目录执行 .\build.ps1 会同时出 linux/windows 两个二进制）
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

Release 提供 `asim-relay-windows-amd64.exe` 和 `asim-relay-linux-amd64`。但数据面已经
交给 mihomo，PC 部署也需要在本机跑一份 mihomo 并把手机指向它；这条路没有实测，
路由器才是受支持的目标。`mihomo_config` 留空时 relay 不会写任何文件。

## 配置（relay.json）

```json
{
  "domain": "epdg.epc.mnc033.mcc234.pub.3gppnetwork.org",
  "listen_addr": "192.168.100.1",
  "listen_ports": [500, 4500],
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
  "enabled": true
}
```

| 字段 | 说明 |
|---|---|
| `domain` | ePDG 域名，中继自己走 DoH 解析 |
| `epdg_ip` | 可选，钉死一个 ePDG IP 跳过 DoH；留空则解析后沿用正在用的那个 IP |
| `protocol` | `trojan`（默认，兼容旧配置）/ `vless` / `hysteria2` |
| `host` / `port` | 节点地址；也可填域名 |
| `password` | trojan 密码、vless UUID、hysteria2 auth |
| `sni` | TLS SNI（hysteria2 为空时用 host） |
| `insecure` | 跳过证书校验；**trojan 默认 true**（兼容旧配置），vless/hysteria2 默认 false |
| `flow` | vless 专用，如 `xtls-rprx-vision`；节点要求时必填 |
| `network` | vless 传输：`tcp`（默认）/ `ws` / `grpc` / `httpupgrade` / `xhttp` |
| `path` / `host_header` | ws / httpupgrade / xhttp 的 path 和 Host 头 |
| `service_name` | gRPC serviceName |
| `reality_public_key` / `reality_short_id` / `fingerprint` | REALITY 参数；填了公钥就走 REALITY |

这些字段都能在手机 WebUI「节点配置」里改，保存后热生效（不用重启 relay）。
`listen_addr` / `listen_ports` / `api_listen` 改了要重启进程。

数据面（mihomo）相关字段：

| 字段 | 说明 |
|---|---|
| `mihomo_config` | mihomo 的 `config.yaml` 路径；留空 = 不生成、不重载 |
| `mihomo_api` | mihomo 控制器地址，默认 `http://127.0.0.1:9090` |
| `mihomo_secret` | 控制器 secret；留空时用 `api_token`（当前生成的配置没开 secret，此头会被忽略） |

ePDG 的 DoH 答案顺序会轮转，换序不代表旧 IP 失效：中继会一直用当前 IP，只有它从答案里
消失才切换；上游 UDP 会话若「只发不收」超过 25 秒会被主动回收，避免手机重传投进黑洞。

## 验证

```sh
# 监听是否到位
netstat -lunp | grep -E ':(500|4500)'

# 状态（和手机 WebUI 用同一个 API）
wget -q -O - --header='X-ASIM-Token: <token>' http://192.168.100.1:8080/status

# 真握手测延迟（会连节点并做一次 UDP 往返）
wget -q -O - --header='X-ASIM-Token: <token>' --post-data='' http://192.168.100.1:8080/ping
```

手机上打一次 WFC，或看一眼「WLAN 通话」开关，回路由器看日志：

- `logread | grep asim-relay` 出现 `flow <手机IP>:<port>|500 -> 31.94.76.x:500`（或 `|4500`）；
- 手机 `ip -br addr show rmnet_data1` 能看到 ePDG 分配的地址；
- 状态栏出现 WLAN 通话标识，短信/通话可用。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/status` | `enabled` / `version` / `protocol` / `epdg_ips` / `flows` / `drops` / `uptime_s` / `node_host` / `node_port` |
| GET | `/config` | 当前完整配置（含 node） |
| PUT | `/config` | 热更新 `node` / `domain` / `enabled` / `listen_addr`；监听变更返回 `restart_required` |
| POST | `/ping` | 用当前（或请求体里的 `node`）做一次真实 UDP 往返，返回 `{"ok":true,"ms":N}` |
| POST | `/enable` / `/disable` | 启停转发（同时写配置） |

除 `/enable` `/disable` 外都需要 `X-ASIM-Token` 头，值等于 `relay.json` 的 `api_token`。
配置文件权限 600。

`PUT /config` 在保存成功后会额外写 mihomo 配置并热重载；重载失败时响应里会多一个
`mihomo_error`，节点是否真正生效以该字段为准。

## 已知限制

- 只处理 ePDG 这一个域名的 UDP，不是通用代理。
- 数据面在 mihomo 时，`/status` 的 `flows` 恒为空（那是旧转发路径的计数）。
- `/enable` `/disable` 只控制旧转发路径，不再影响 mihomo 的数据面。
- 生成的 mihomo 配置只认 trojan / hysteria2 / vless（tcp、ws、httpupgrade、grpc、REALITY）；
  vless 的 `xhttp`/`splithttp` 保存时返回错误，不会写出坏配置。
- 节点掉线或网络抖动时，路由器会超时重连；IKE 自己会重传，恢复通常 1 分钟内。
- VLESS 内嵌 Xray（pin `v1.251208.0`，最后一个还支持 `allowInsecure` 的版本），
  二进制约 32MB；升级 Xray 前要改成证书 pin。
- Hysteria2 的 obfs / 端口跳跃未实现。
- 手机侧 IWLAN 注册状态可能被 AP 缓存；重连 WiFi 后仍卡死就只能重启手机。
