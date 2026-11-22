# mihomo（路由器上的 ePDG 数据面）

`asim-relay` 不再转发 IKE/ESP；它只保留 API，并在节点变动时生成 mihomo 的
`config.yaml` 后热重载。真正把 `31.94.76.0/24` 送进英国节点的是 mihomo 的 TUN。

固定版本：`v1.19.34`，资产 `mihomo-linux-amd64-compatible-v1.19.34.gz`。

## 依赖

- `kmod-tun`（`ls -l /dev/net/tun` 必须存在）。
- 路由器能出网访问节点；relay 在启动和每次 `PUT /config` 时写配置。

## 安装

```sh
# 二进制与 init（在 PC 上取好再推上去）
# /usr/bin/mihomo            755
# /etc/init.d/mihomo         755
# /etc/asim/mihomo/          目录，config.yaml 由 relay 生成（600）

mkdir -p /etc/asim/mihomo
/etc/init.d/mihomo enable
/etc/init.d/mihomo restart
```

顺序：先重启一次 relay，确认 `/etc/asim/mihomo/config.yaml` 已出现，再启动 mihomo。
mihomo 在配置缺失时会启动失败，procd 每 5 秒重试，relay 写好后会自动起来。

## firewall：放行 tun 回程

`asim0` 不在任何 zone 里时，ePDG 的回包会被丢。把它加进 lan zone：

```sh
uci add_list firewall.@zone[0].device='asim0'
uci commit firewall
/etc/init.d/firewall reload
nft list ruleset | grep -c asim0     # 应大于 0
```

## dnsmasq：删掉旧的地址覆盖

旧方案把 ePDG 域名应答成路由器自己；现在要让手机拿到真实 IP：

```sh
sed -i '/3gppnetwork.org/d' /etc/dnsmasq.conf
/etc/init.d/dnsmasq restart
nslookup epdg.epc.mnc033.mcc234.pub.3gppnetwork.org 127.0.0.1 | tail -6   # 应为 31.94.76.x
```

## 验证

```sh
ip route show table all | grep 31.94.76      # mihomo 把路由放在 table 2022
ip rule | grep 9002
wget -q -O - --header='Authorization: Bearer <token>' http://127.0.0.1:9090/version
logread | grep -i mihomo | tail -5
```

`ip route show table all` 应有 `31.94.76.0/24 ... dev asim0 table 2022`；`/version` 返回 `{"meta":true,...}`；
日志里没有 `initial configuration failed`。手机重启后应恢复 `voice=IN_SERVICE`、
`iwlan=HOME`，且 mihomo `/connections` 里能看到到 `31.94.76.x:500/4500` 的 UDP 会话。

## 回滚

**只是想让 VoWiFi 停下来**（mihomo 或代理出问题）：在 WebUI 点「停用中继」，
或直接停服务。ePDG 会改走路由器自己的 WAN，不再进 TUN：

```sh
/etc/init.d/mihomo stop
```

**要退回自研转发**（v0.3.x 的老链路）：relay 从 v0.4.1 起不再监听 500/4500，
必须换回旧二进制，再恢复 dnsmasq 覆盖：

```sh
# Release v0.4.0 的二进制仍带旧转发路径
wget -O /usr/bin/asim-relay https://github.com/RanCloud1314/ASIM/releases/download/v0.4.0/asim-relay-linux-amd64
chmod 755 /usr/bin/asim-relay
/etc/init.d/mihomo stop
/etc/init.d/mihomo disable
echo 'address=/epdg.epc.mnc033.mcc234.pub.3gppnetwork.org/192.168.100.1' >> /etc/dnsmasq.conf
/etc/init.d/dnsmasq restart
/etc/init.d/asim-relay restart
# 然后重启手机
```

## 已知限制

- 只接管 ePDG 的 `/24`，其他流量直连；ePDG 换网段时要改
  `router/asim-relay/mihomo.go` 的 `mihomoCIDR` 并重新部署 relay。
- 节点只支持 trojan / hysteria2 / vless（tcp、ws、httpupgrade、grpc、REALITY）；
  vless 的 `xhttp`/`splithttp` 保存时会被拒绝，不会写出坏配置。
- `external-controller` 只绑 `127.0.0.1:9090`，没有 `secret`；不要把它暴露到 LAN。
