# asim-relay — 路由器侧 ePDG 中继

把手机基带发往本机的 IKE/ESP-in-UDP 转发到英国 trojan 节点。只影响 ePDG 这一个域名的 UDP，其他流量完全不碰。

## 为什么需要它

系统 IWLAN 的 IKE/IPsec 由**手机基带**执行，不经过手机 Linux IP 栈（实测见 `m0/M0-B-FINDINGS.md`），手机上的 TUN / ip rule / 代理 App 都抓不到。唯一可行点是在手机之外拦。

## 工作方式

```
手机基带 --IKE(500)/ESP-in-UDP(4500)--> 路由器 (本程序) --trojan over TLS--> 英国节点 --> EE ePDG
```

1. 路由器 dnsmasq 把 ePDG 域名应答成路由器 LAN IP（见下），基带就会把 IKE 发到这里；
2. 本程序在 `listen_addr:500/4500` 收包，为每个客户端流开一条 trojan UDP 会话；
3. 真实 ePDG 地址由程序自己走 DoH（`dns.alidns.com`）解析，不受 dnsmasq 覆盖影响；
4. 回包按原 5 元组送回。

## 安装（OpenWrt x86_64 已实测）

```sh
# 1) 编译（在开发机）
cd router/asim-relay
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o asim-relay-linux-amd64 .

# 2) 传二进制与配置
scp asim-relay-linux-amd64 root@192.168.100.1:/usr/bin/asim-relay
scp relay.json              root@192.168.100.1:/etc/asim/relay.json
scp asim-relay.init         root@192.168.100.1:/etc/init.d/asim-relay
ssh root@192.168.100.1 'chmod 755 /usr/bin/asim-relay /etc/init.d/asim-relay; chmod 600 /etc/asim/relay.json'

# 3) dnsmasq 把 ePDG 指到路由器自己（只需一次，持久保存在 /etc/dnsmasq.conf）
echo 'address=/epdg.epc.mnc033.mcc234.pub.3gppnetwork.org/192.168.100.1' >> /etc/dnsmasq.conf
/etc/init.d/dnsmasq restart

# 4) 起服务
/etc/init.d/asim-relay enable
/etc/init.d/asim-relay restart
```

## 验证

```sh
# 监听是否就位
netstat -lunp | grep -E ':(500|4500)'

# 状态（手机 WebUI 用的同一个 API）
wget -q -O - --header='X-ASIM-Token: <token>' http://192.168.100.1:8080/status
```

手机上触发一次 WFC（切一下「WLAN 通话」开关，或整机断电重启），期望：

- `logread | grep asim-relay` 出现 `flow <手机IP>:<port>|500 -> 31.94.76.x:500`，随后 `|4500`；
- 手机 `ip -br addr show rmnet_data1` 出现 ePDG 分配的地址；
- 状态栏出现 WLAN 通话标识，可收短信/通话。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/status` | `enabled` / `epdg_ips` / `flows` / `drops` / `uptime_s` |
| POST | `/enable` | 开始转发（写入配置） |
| POST | `/disable` | 停止转发、断开现有关联（写入配置） |

除 `/status` 外都需要 `X-ASIM-Token` 头，值即 `relay.json` 里的 `api_token`（该文件权限 600）。

## 已知限制

- 只有 `31.94.76.0/24` 这类 EE ePDG 地址可达；换运营商要改 `domain` 并确认 `relay.json` 里 `listen_addr` 仍是路由器 LAN IP。
- 节点域名解析偶发超时（路由器刚启动时），IKE 重传会自动重试，最多多等 1 分钟。
- 基带的 IWLAN 注册状态跨 AP 重启存活；改配置后建议整机断电重启手机来强制重建。
