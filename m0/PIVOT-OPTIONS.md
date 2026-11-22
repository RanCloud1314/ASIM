# ASIM 架构重构选项（M0-B 已确认：IKE/IPsec 由基带执行）

## 已确认的前提（见 M0-B-FINDINGS.md）
1. 基带自己做 IKE/IPsec，包不经过手机 Linux IP 栈 → 手机上任何 TUN/VpnService/ip rule 都拦不到。
2. 基带用 AP 下发的 DNS 服务器解析 ePDG（实测拿到了 PC 上 Clash 的 fake-ip）→ **手机 hosts 无效**。
   想让基带把 ePDG 指到别处，只能控制 AP 下发给基带的 DNS 服务器，或让上游 DNS 返回自选地址。
3. 基带的 IWLAN 注册状态跨 AP 重启存活；清状态只能整机断电。

## 路线 1：LAN 侧中继（推荐，最省事）
- 常开设备（PC / NAS / 软路由 / 小主机）上跑 DNS + UDP 中继：
  - DNS：把 `<MNC/MCC 的 epdg 域名>` 应答成中继机自身 LAN IP，其余正常递归。
  - 中继：监听 500/4500，按 5 元组映射到真实 ePDG，经 Clash/sing-box 的英国节点转发（UDP）。
- 手机侧：WiFi 静态 DNS 指向中继机（或让热点 DHCP 直接下发该 DNS）。无需 root 模块。
- 状态：**已由今早 spike 等效验证**（PC 热点 + Clash，系统 WFC 注册成功）。
- 代价：需要一台常开设备；不是纯手机方案。

## 路线 2：手机自环中继（基本排除）
- 曾设想：hosts 把 ePDG 指到手机自身 wlan IP，手机内 UDP 中继接住 → 经 mihomo 送出。
- 已排除：基带不读 hosts。要改只能改下发给基带的 DNS，把 DNS 指向 127.0.0.1
  对基带而言是"指回它自己"，不可用。
- 结论：放弃。

## 路线 3：软件 VoWiFi（SWu / IKEv2 自建 IMS）
- 放弃系统 WFC，自己实现 ePDG 客户端 + IMS 注册（`spike/vowifi_gateway` 有雏形）。
- IKE 在用户态 → 天然能被 mihomo/TUN 代理，纯手机方案。
- 代价：AKA（SIM 桥）、IKEv2、ESP、SIP/IMS 注册、通话/短信与系统拨号器集成，
  工程量最大；EAP-AKA 需要 UICC 访问（KSU 下可行但 fragile）。

## 建议
先做路线 1 的最小可用版（一台 Linux/OpenWrt 上的 DNS + UDP 中继），
手机侧保持"飞行模式 + 系统 WFC"即可。路线 3 作为长期可选项，路线 2 放弃。
