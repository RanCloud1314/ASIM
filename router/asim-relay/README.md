# asim-relay — 路由器/PC 上的 ePDG 中继

把手机发往本机的 IKE/ESP-in-UDP 转发到 upstream 节点，只影响 ePDG 这一个域名的 UDP。
支持 `trojan`、`vless`（内嵌 Xray 引擎，含 vision / ws / grpc / xhttp / REALITY）、
`hysteria2`。

## 为什么需要它

系统 IWLAN 的 IKE/IPsec 在**手机基带**里执行，绕过手机 Linux IP 栈（实测见
`m0/M0-B-FINDINGS.md`），手机上的 TUN / ip rule / 代理 App 都抓不到。唯一可行的
是在手机之外做中继。

## 工作方式

```
手机基带 --IKE(500)/ESP-in-UDP(4500)--> 路由器 (本程序) --trojan/vless/hysteria2--> 节点 --> EE ePDG
```

1. 路由器 dnsmasq 把 ePDG 域名应答成路由器 LAN IP，于是手机的 IKE 发到这里；
2. 本程序在 `listen_addr:500/4500` 收包，为每个客户端流开一条上游 UDP 会话；
3. 真实 ePDG 地址由上游节点侧解析（本程序用 DoH 拿 A 记录）；
4. 回包按原客户端地址送回。

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

# 3) dnsmasq 把 ePDG 指到路由器自己（只要一次，持久化在 /etc/dnsmasq.conf）
echo 'address=/epdg.epc.mnc033.mcc234.pub.3gppnetwork.org/192.168.100.1' >> /etc/dnsmasq.conf
/etc/init.d/dnsmasq restart

# 4) 启动
/etc/init.d/asim-relay enable
/etc/init.d/asim-relay restart
```

### 装在 PC 上

Release 提供 `asim-relay-windows-amd64.exe` 和 `asim-relay-linux-amd64`。

- `listen_addr` 改成 `0.0.0.0`（或 PC 的 LAN IP），`api_listen` 建议绑 LAN IP；
- 手机 WebUI 的「路由器地址」填这台 PC 的地址；
- Windows 放行入站 UDP 500/4500、TCP 8080（或 `api_listen` 的端口）；
- dnsmasq 的 `address=` 要指向 PC，而不是路由器。

```powershell
.\asim-relay-windows-amd64.exe -c .\relay.json
```

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

## 已知限制

- 只处理 ePDG 这一个域名的 UDP，不是通用代理。
- 节点掉线或网络抖动时，路由器会超时重连；IKE 自己会重传，恢复通常 1 分钟内。
- VLESS 内嵌 Xray（pin `v1.251208.0`，最后一个还支持 `allowInsecure` 的版本），
  二进制约 32MB；升级 Xray 前要改成证书 pin。
- Hysteria2 的 obfs / 端口跳跃未实现。
- 手机侧 IWLAN 注册状态可能被 AP 缓存；重连 WiFi 后仍卡死就只能重启手机。
