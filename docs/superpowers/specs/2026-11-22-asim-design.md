# ASIM 设计文档 — 海外卡防驻网 + 路由器侧 VoWiFi 分流

日期：2026-11-22（v2，M0-B 实测后重构）
状态：已确认架构方向，待路由器侧验证

## 背景与目的

用户人在中国，手机（小米 12S Pro / unicorn，Android 17，KernelSU 3.4.2）插两张卡：

- 卡 1（slot 1）：CTExcel（中国电信英国，IMSI 234/33，EE 网络），签约支持 VoWiFi，但必须**永不漫游驻网**（省漫游费、避免被运营商风控），只通过 WiFi 通话收发短信；
- 卡 2（slot 0）：中国联通 46001，正常使用，**不得受任何影响**。

问题分两半：

1. CTExcel 的 ePDG（`epdg.epc.mnc033.mcc234.pub.3gppnetwork.org`，EE 的 VoWiFi 网关）从中国直连不可达，需要经英国出口；
2. 该卡不能被允许注册到中国基站。

## 关键实测结论（M0-A / M0-B，2026-11-22）

| 结论 | 证据 |
|---|---|
| **系统 IWLAN 的 IKE/IPsec 由基带（modem）执行，不经过 Android/Linux 的 IP 栈** | 隧道在线时 `/proc/net/udp{,6}` 无 500/4500 socket、`ip xfrm state` 为空、`/proc/net/xfrm_stat` 全零；APK `vendor.qti.iwlan` 只有 dex，无任何 IKE 原生库；`imsd` 通过 QMI `PDP_ACTIVATE(RAT=IWLAN)` 让基带建 PDN |
| **手机上的任何代理都抓不到 ePDG 流量** | 隧道在线时对 ePDG 的 fake-ip 加 `ip rule ... blackhole`，90 秒后 `rmnet_data1` 仍 UP、IMS 仍 `WLAN: IWLAN, HOME`（隧道包不进内核 FIB） |
| **基带自己做 DNS 解析 ePDG**（用 AP 下发的 DNS 服务器） | PC 热点 + Clash 时基带拿到 fake-ip `198.18.0.41` 并成功建链；手机 `/system/etc/hosts` 对基带无效 |
| **基带 IWLAN 注册状态跨 AP 重启存活，只有整机断电才清** | 14:58 AP 重启后基带仍报 `WLAN: IWLAN, HOME` 但 `rmnet_data1` DOWN，此后拒绝重建 PDN；15:07 断电重启后 20 秒内自动建链 |
| **LAN 侧代理对系统 WFC 完全有效** | spike：手机连 PC 热点（PC Clash 走英国节点），系统 WFC 注册成功、通话短信可用；M0-B 复现，IMS `WLAN: IWLAN, HOME` |
| 订阅的英国 trojan 节点支持 UDP | 上述两次真实 IKE/ESP 都经它成功建链 |

**由此确定的硬边界：拦截点必须在手机 IP 栈之外。** 纯手机方案（TUN / ip rule / VpnService / 全局代理）对系统 WFC 一律无效；手机 hosts 也无效。

## 目标

1. **防驻网（手机模块，核心交付）**：勾选的卡永不注册到任何基站，同时 SIM 保持上电以支持 EAP-AKA；取消勾选 / 停用模块 / 卸载时完整恢复原状态。
2. **VoWiFi 可用（家庭 WiFi 场景）**：在家里 WiFi 下 CTExcel 的 WFC 正常注册、可收发短信/通话；实现方式是在**家庭路由器**上把 ePDG 流量分流到英国节点。
3. 双卡并存：联通卡的蜂窝与日常流量零波及。
4. 系统原生体验：系统拨号器/短信直接可用（不自己实现 IMS 栈）。
5. KSU 模块形态：安装即用，WebUI 管理；路由器侧提供可直接粘贴的配置片段。

## 非目标（明确不做）

- 不做手机端 IKE/TUN/ip rule 分流（已实测无效，见上）。
- **不支持"蜂窝数据作为 WFC 底座"**（原 v1 硬性要求，已证伪）：基带 IKE 走蜂窝时同样绕过手机 IP 栈，手机侧无处拦截；出门不在家庭 WiFi 时没有 VoWiFi。
- 不实现软件 ePDG 客户端 / IMS 栈（SWu 路线留档在 `spike/`，仅当 1、2 都做不成才考虑）。
- 不做 eSIM 管理、SIM 切换 UI。
- 不承诺紧急呼叫。

## 架构

```
        ┌──────────── 家庭局域网 ────────────┐
手机    │                                    │
 ├─ 基带 IWLAN IKE/ESP ──wlan0──→ 路由器 ──┐ │
 │   (dst=ePDG, 由基带 BSD 栈发出)          │ │
 │                                          ▼ │
 │                              mihomo/sing-box（UK 节点）
 │                                          │ │
 │                                          ▼ │
 │                                    🇬🇧 EE ePDG → IMS 注册
 │                                            │
 └─ 联通卡蜂窝 / 其他 App：路由器不干预 ──────┘

手机侧（KSU 模块）只做一件事：让 CTExcel 无法驻网，
并展示 IMS 注册状态、一键恢复。
```

### 组件

1. **路由器侧（OpenWrt，用户已有）**：mihomo（或 sing-box）以 TUN/TPROXY 方式接管本机转发的 UDP/TCP，规则只命中 ePDG：
   - `DOMAIN-SUFFIX,3gppnetwork.org,🇬🇧英国`
   - `IP-CIDR,31.94.76.0/24,🇬🇧英国`（EE ePDG 段，兜底）
   - 其余 `DIRECT`。
   - 手机把路由器当 DNS（DHCP 默认即是），fake-ip 由路由器 mihomo 提供，基带解析 ePDG 时拿到假 IP，流量自然进路由器代理。
   - 需开启 DNS 劫持（`dns-hijack: any:53`）与 UDP 转发，节点须 `udp: true`。
2. **`nocamp.sh`（手机模块核心）**：`lock <slotId> <mode>` / `unlock <slotId>`，改 `set-allowed-network-types-for-users`（或手动选网），原值存档；`uninstall.sh` 与 `asimd.sh stop` 必调 unlock。
3. **`asimd.sh`**：轻量守护，仅维护防驻网状态（防止系统更新/重启后 mask 被重置）、写 `state.json`（各卡 reg 状态、IMS 注册状态、mask 当前值）。
4. **WebUI**：卡槽勾选、防驻网模式选择（A/B/C）、状态面板、一键恢复、日志。

### 时序

1. 开机 → `service.sh` 读配置 → 对勾选槽执行 `nocamp.sh lock` → 写状态。
2. 手机连家庭 WiFi → 基带解析 ePDG（路由器 DNS 返回 fake-ip）→ IKE 经路由器 → 英国节点 → ePDG → IMS 注册（系统自己完成）。
3. 路由器规则变更 / 节点切换 → 基带自身重连逻辑会在几分钟内重试。

## 方案取舍

- **路由器侧分流 + 手机侧只防驻网（选定）**：改动最小，已在 PC 热点上端到端验证；手机模块只碰 `set-allowed-network-types-for-users`，风险面最小，卸载即恢复。
- **手机端 TUN + ip rule（已证伪，作废）**：基带 IKE 不进内核，方案不成立。
- **手机端 UDP 中继 + hosts（作废）**：基带不读 hosts；且改 DNS 也必须落到外部设备，无法带来"纯手机"收益。
- **软件 VoWiFi / SWu（备而不选）**：纯手机可行（IKE 在用户态，能被代理），但需自建 AKA+IKEv2+ESP+IMS 全栈，工程量高一个数量级。

## 用户已定的决策

- 出口：mihomo + 用户机场订阅（trojan 英国节点，`udp: true`）。
- 家庭路由器：OpenWrt，可 24 小时在线。
- 目标卡：CTExcel（subId=1）；联通卡（subId=2）不得受影响。
- **硬性要求：与用户日常代理 App 同时在线**（路由器方案下天然成立：手机侧不再有 TUN）。
- **硬性要求：勾选即阻止驻网**，SIM 保持上电可鉴权；取消/停用/卸载恢复原状。三方案（M0 实测选默认，WebUI 可切）：
  - A. `set-allowed-network-types-for-users` 限到 GSM-only（国内多数地区 2G 已退网）；
  - B. bitmask 0（若被接受）；
  - C. 手动选网锁定到不可用 PLMN。
  - 禁止 `disable-physical-subscription`（会断电 UICC，AKA 不可用）。

## 风险与对策

1. **路由器侧 fake-ip 会污染手机的联网校验**（实测：黑洞整段 fake-ip 会打断 Android network validation）：路由器不要把整段 fake-ip 黑洞；正常 mihomo fake-ip + 直连规则即可，PC 热点已验证校验能通过。
2. **家里 WiFi 下基带可能不主动建 IWLAN**（14:58 那次因基带状态卡死）：基带状态只在整机断电后干净。验收时必须"整机断电 → 开机 → 连 WiFi"测一次，而不是热切换。
3. **2G 兜底**：方案 A 若当地仍有 GSM 覆盖，卡可能驻留 2G。M0 实测各方案；默认值由实测决定，WebUI 可切 C。
4. **运营商侧对"从未驻网只走 VoWiFi"的限制**：CTExcel 可能要求周期性蜂窝注册，长期只用 WiFi 可能被停；属于运营策略，模块不解决，文档写明。
5. **Android/系统更新改变 IWLAN 行为**：升级后需复测（项目性质使然）。
6. **模块锁住网络类型后用户"手机像砖"**：uninstall.sh 与 stop 路径强制 unlock；`state.json` 里保留原值副本。

## 验收标准

1. 联通卡数据、其他 App 流量与装模块前无差异（测速 + 出口国家 `CN`）。
2. 家庭 WiFi 下，CTExcel WFC 注册成功（`WLAN: IWLAN, HOME`），收得到短信；整机断电重启后 5 分钟内自动恢复。
3. 勾选 CTExcel 后，该卡 voice/data 均不 `IN_SERVICE`（三种模式各测一次），同时 WFC 仍注册成功。
4. 取消勾选 / `asimd.sh stop` / 卸载模块后，网络类型恢复原值、卡可正常驻网。
5. WebUI 能显示状态（各卡 reg、IMS 注册、当前 mask）并切换防驻网模式。
