# mihomo 数据面切换（ePDG 中继）设计

日期：2026-11-22
状态：已实施
> 2026-11-23 更新：本设计的「旧转发路径保留」已作废 —— v0.4.1 把 asim-relay 的
> 500/4500 监听、DoH 解析和内嵌 Xray/Hysteria 全部删除，只留控制面。详见
> `router/asim-relay/README.md` 与 `docs/VOWIFI-DEBUG.md` 的「代码状态」。

## 意图与为什么

`docs/VOWIFI-DEBUG.md` 的 A/B 已经把出口、服务器、凭据排掉：同一个节点，PC 上的 mihomo
能撑过第 30 分钟重注册，自研 asim-relay 在同一秒被 EE 回 `403 Registration_Error14`。
剩下的差异只在传输实现。继续猜 asim-relay 哪行写错，成本高于直接换成唯一被证明过的实现。

目标：路由器上的 mihomo 接管 ePDG 的 UDP 500/4500，asim-relay 退成控制面。
验收：跨过一次 30 分钟重注册，并连续 ≥45 分钟无 403、无 `OUT_OF_SERVICE`。

非目标：

- 不修 asim-relay 的数据面缺陷（会话合并、心跳都不做）；它的转发路径保留但不使用。
- 不做 fake-ip、不做 TPROXY、不做自动恢复守护、不做通用代理。
- 不动手机模块与 `asimd`；不改卡槽相关的一切。

反面目标（即使能跑也算失败）：

- mihomo 接管 ePDG 以外的流量。
- DNS 改动影响到手机的网络校验或其他域名。
- 节点配置出现两个需要手工同步的真相源。

## 约束

- 路由器 OpenWrt 25.12.6 x86_64，**没有 curl**（用 `wget -q -O -`），procd 管理服务。
- dnsmasq 的覆盖行持久化在 `/etc/dnsmasq.conf`；`/etc/config/firewall` 是 uci。
- 基带只认 AP 下发的 DNS（`m0/M0-B-FINDINGS.md`），所以拦截只能靠 DNS 或路由。
- 线上节点已热切到 `:56007`；仓库 `relay.json` 还是 `:56006`，**以路由器现场值为准**。
- blast radius 必须保持「只有 ePDG 这一个域」。

## 方案比较

| 方案 | 取舍 | 结论 |
|---|---|---|
| 继续修 asim-relay（合并 500/4500 到一条会话、加心跳） | 不解决「同出口不同实现」这个已证实的差异，还要再等 30 分钟一轮验证 | 否 |
| mihomo 跑在 PC，路由器加静态路由指过去 | 多一跳、依赖 PC 常开，和现在「路由器独立工作」的目标相反 | 否 |
| **mihomo 跑在路由器，TUN + 真实 IP 路由** | 只加一条 `/24` 路由；不需要 mihomo DNS | **选定** |
| mihomo 跑在路由器，dnsmasq 按域名转发给 fake-ip DNS | 抗 ePDG 换网段，但多一层 DNS + fake-ip，且要处理与 Android 校验的关系 | 备选，仅在实测换网段时启用 |

控制面（手机 WebUI）的三个选项：一起接上 / 先跑通再接 / 砍掉 relay。
选定**一起接上**：保留 relay 的 `/status`、`/ping`、`/config`，`PUT /config` 时额外渲染
mihomo 配置并热重载。

## 选定设计

### 链路

```
手机基带 --IKE 500 / ESP-in-UDP 4500--> 31.94.76.x
        --(默认网关)--> 路由器 --(TUN asim0 路由 31.94.76.0/24)--> mihomo
        --trojan/UDP--> 英国节点 --> EE ePDG
```

### 路由器侧

1. 装 mihomo（linux-amd64 静态二进制）到 `/usr/bin/mihomo`，工作目录 `/etc/asim/mihomo/`，
   `config.yaml` 由 relay 生成。
2. `/etc/init.d/mihomo`（procd，`respawn 3600 5 5`，`-d /etc/asim/mihomo`）。
3. mihomo 配置要点：
   - `mode: rule`、`ipv6: false`、`dns.enable: false`；
   - `tun.enable: true`、`stack: system`、`device: asim0`、`auto-route: true`、
     `route-address: ["31.94.76.0/24"]`、`auto-detect-interface: true`、`dns-hijack: []`；
   - 一个 trojan 代理（`udp: true`，`skip-cert-verify` 跟随 WebUI 的 `insecure`）；
   - `rules: [IP-CIDR,31.94.76.0/24,ASIM-UK,no-resolve, MATCH,DIRECT]`；
   - 不开 `auto-redirect`（先靠纯路由；实测进不去 tun 再开）。
4. firewall：把 `asim0` 加进 lan zone 的设备列表，保证 tun→br-lan 的回程不被丢；
   其他 zone 不动。
5. dnsmasq：删掉 `address=/epdg.epc.mnc033.mcc234.pub.3gppnetwork.org/192.168.100.1`，
   手机改为经 dnsmasq 上游解析到真实 `31.94.76.x`。其他域名不受影响。
6. 回滚（30 秒内）：停用 mihomo、恢复 dnsmasq 那行、重启 dnsmasq；relay 仍在跑，
   监听 500/4500 没变，手机重启即回到旧链路。

### 控制面（asim-relay）

- `relay.json` 新增 `mihomo_config`（默认 `/etc/asim/mihomo/config.yaml`）与
  `mihomo_api`（默认 `http://127.0.0.1:9090`）；`mihomo_secret` 为空时复用 `api_token`。
- relay 启动时按当前 node 渲染一次配置并热重载（解决 mihomo 先起、配置后到的顺序问题）。
- `PUT /config` 通过校验后：渲染 → 原子写盘（tmp+rename）→ `PUT /configs?force=true`
  （body `{"path": ...}`）。重载失败不回滚写入，但把错误放进响应。
- YAML 用手写渲染（不引新依赖）；字符串用单引号 + `''` 转义。
- 协议映射：`trojan` / `hysteria2` / `vless`（含 `network`、`flow`、`ws-opts`、
  `grpc-opts`、`reality-opts`），覆盖 WebUI 现有的表单字段；mihomo 不支持的字段忽略。
- 不变的部分：relay 自己的 500/4500 监听、DoH 解析、`/status`、`/ping`、`/enable`、
  `/disable` 全部保留。已知语义变化（写进文档，不改 UI）：
  `/status` 的 `flows` 恒为空；`/enable` `/disable` 不再影响数据面。

### 手机侧

零改动。`relay.sh` 继续打路由器 8080；WebUI 的节点保存经 relay 落到 mihomo。

## 风险与待现场确认

1. **A/B 的 mihomo 在 PC 热点，路由器版多出转发/NAT/MTU 三个变量。** 若仍是精确
   30 分 01 秒 403，H4 排除，问题转运营商侧，停止在传输上投入。
2. 三个现场点：`kmod-tun` 是否可装、`/dev/net/tun` 是否存在；firewall zone 改完
   回程是否通；WAN 的 PPPoE MTU 下 IKE/ESP 是否被分片（必要时给 tun 设 MTU）。
3. 首轮要重启手机（基带 DNS 缓存 + 旧 PDN 不能靠换 WiFi 清掉，见 H1）。
4. 删 dnsmasq 覆盖行后，其他设备解析该域名会走公网，行为正确但属于可见变化。

## 验证

- 装完：`ip route | grep 31.94.76` 指向 `asim0`；mihomo 控制器 `/connections` 能看到
  手机到 `31.94.76.x:500/4500` 的 UDP 会话；`logread` 有 mihomo 启动日志。
- 手机重启后：`rmnet_data1` 拿到英国段地址、`voice=IN_SERVICE iwlan=HOME`。
- 盯满 45 分钟，每 5 分钟采样一次 voice/iwlan；重点看第 30 分钟那次重注册。
- 合格标准：第 30 分钟重注册成功 + 无 403；至少 1 个干净样本，最好 2 个。
- 失败时先看 mihomo 日志与 `ip route`，再抓 WAN 口确认会话形态，不要改回 relay 试运气。

## 留给计划阶段的活

- 选定 mihomo release 版本并核对 `route-address` 字段名（老版本叫
  `inet4-route-address`），写进实施步骤。
- Go 侧的单元测试范围：YAML 渲染的三种协议 + 转义。
- 文档更新点：`docs/HANDOFF.md` 链路、`docs/VOWIFI-DEBUG.md` 结论、
  `router/asim-relay/README.md` 中继的新定位。
