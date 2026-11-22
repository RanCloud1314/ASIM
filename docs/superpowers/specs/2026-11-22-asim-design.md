# ASIM 设计文档 — 海外卡 VoWiFi 代理路由 KSU 模块

日期：2026-11-22
状态：待确认（spike 已验证核心链路）

## 背景与目的

用户人在中国，手机（小米 12S Pro / unicorn，Android 17，KernelSU 3.4.2）插两张卡：
- 卡 1（slot 1）：CTExcel（中国电信英国，IMSI 234/33，跑 EE 网络），签约支持 VoWiFi，但用户希望它**永不漫游驻网**（省漫游费），只通过 WiFi 通话收发短信/电话；
- 卡 2（slot 0）：中国联通 46001，正常使用。

问题：CTExcel 的 ePDG（`epdg.epc.mnc033.mcc234.pub.3gppnetwork.org`，EE 的 VoWiFi 网关）从中国直连不可达/不可用，需要把这张卡产生的 VoWiFi 流量经英国出口节点转发；同时手机上其他一切流量（联通卡数据、所有 App、用户已装的代理 App）完全不受影响。

**Spike 已验证（2026-11-20/21）**：飞行模式 + 连接 PC 热点（PC Clash Verge 走英国节点）时，系统自带 WFC 成功注册 IMS，电话短信可用。Clash 日志证实 ePDG 的 IKE（UDP 500/4500）就是普通 wlan 流量；`ip xfrm state` 证实本机为软件 IWLAN（AP 侧内核 IPsec，SA 端口 4500 NAT-T，系统对 IWLAN 流量打 fwmark 0x20000，ip rule 优先级 9999 强制查 wlan0 表）。

## 目标

1. 去掉 PC：手机端独立完成"CTExcel 卡 VoWiFi 流量 → 英国节点 → ePDG"。
2. 双卡并存：联通卡的蜂窝与日常流量零波及。
3. 系统原生体验：系统拨号器/短信直接可用（不自己实现 IMS 栈）。
4. 全运营商通用：ePDG 地址由 SIM 的 IMSI 按公式推导，不是 CTExcel 特化。
5. KSU 模块形态：安装即用，WebUI 管理。

## 非目标（明确不做）

- 不实现软件 ePDG 客户端 / IMS 栈（SWu-IKEv2 路线已备好但不在本项目，除非轻量路线失效才启用；spike 代码在 `spike/` 留档）。
- 不管理手机上其他代理流量（用户已有 Clash 客户端）。
- 不做 eSIM 管理、双卡切换 UI 之类的"SIM 管理器"大而全功能。
- 不承诺紧急呼叫（E911 地址等由运营商 WFC 自带逻辑处理）。

## 关键事实（实测依据）

| 事实 | 来源 |
|---|---|
| VoWiFi = IKEv2/IPsec to ePDG，UDP 500/4500 + ESP(NAT-T) | `ip xfrm state`（SA: espinudp sport 4500） |
| 软件 IWLAN：xfrm 在 AP 内核，流量 uid 1000（system_server），mark 0x20000 | `ip rule`：9999 fwmark 0x20000/0xfffff lookup wlan0 |
| ePDG FQDN 由 IMSI 推导：`epdg.epc.mnc<MNC0pad3>.mcc<MCC>.pub.3gppnetwork.org` | 3GPP TS 23.003 + Clash 日志实测 |
| 出口 IP 必须落地英国（EE 侧 geo 检查的假说，spike 中英国节点成功，未测其他国家） | spike |
| 少数运营商经 CarrierConfig 覆盖 ePDG 地址 | AOSP 文档；**本机实测 `cmd phone cc get-value` 被 SELinux 拒绝（shell 与 ksu 域均拒），v1 只用公式推导，CarrierConfig 覆盖场景（主要是部分美国运营商）列为后续项** |
| IMS SIP/RTP 全部封装在 IPsec 隧道内（tunnel mode），只需路由外层包 | `ip xfrm state` mode tunnel |
| KSU root 域可以增删 `ip rule`（add 用 `to X pref N table T`，删除用 `ip rule del pref N`） | 2026-11-22 实测：add/del 均成功 |

## 架构

```
                 ┌──────────────── 手机 ────────────────┐
系统 IWLAN 栈 ──IKE/ESP──→ ePDG 真实 IP                 │
   (uid 1000, mark 0x20000)                             │
        │ dst=ePDG 的 ip rule (pref < 9999)            │
        ▼                                               │
  ASIM mihomo 实例 (root, TUN, 只含 ePDG 路由)          │
        │ trojan (UK node)                              │
        ▼ 经系统默认网络(wifi/蜂窝)                      │
   🇬🇧 节点 → EE ePDG → IMS 注册                        │
                                                       │
联通卡蜂窝数据、其他 App、用户代理 App：全部原路 ────────┘
```

### 组件

1. **mihomo（root 常驻）**：模块自带静态 arm64 二进制。TUN 模式，`inet4-route-address` 仅包含当前解析出的 ePDG IP 集合；出站走用户订阅里的指定节点（默认英国01/02，可换）。配置由守护进程生成，不含用户其他分流规则。
2. **asimd（守护进程，脚本即可）**：
   - 枚举在用 SIM（`dumpsys isub`），按公式推导每张卡的 ePDG FQDN（含 CarrierConfig 覆盖检查 `cmd phone cc get-value`）；
   - 周期解析 FQDN → IP（v4/v6），变化时更新 mihomo 的 TUN 路由并重载；
   - 维护分流：`ip rule add to <epdg_ip> pref 9000 table <mihomo_tun_table>`（优先级压过系统 9999），保证系统 mark 的 IKE 包也被 TUN 截获；对称删除；
   - 只处理用户勾选的卡槽（WebUI 配置），默认只勾选 CTExcel 所在槽。
3. **WebUI（KSU 标准 webroot）**：卡槽列表与勾选、节点选择（列出订阅节点）、状态面板（ePDG 当前 IP、TUN 是否存活、IMS 注册状态——读 `dumpsys telephony.registry`）、启动/停止、日志查看。

### 时序（关键路径）

1. 开机/启动模块 → mihomo 起 TUN（初始空路由）→ asimd 解析 ePDG IP → 写入路由与 ip rule。
2. 系统 WFC 发起 IKE（dst=ePDG）→ dst 命中我们的 ip rule（pref 9000）→ 进 TUN → 英国节点 → ePDG。
3. IPsec 建立 → IMS 注册（系统自己完成）→ 电话/短信可用。
4. ePDG IP 轮换/网络切换 → asimd 重新解析更新；若节点不可用 → WebUI 告警。

## 方案取舍

- **轻量路由方案（选定）**：复用系统 WFC 栈，只做流量路由。工程量小（≈1 个模块 + 守护脚本 + WebUI），spike 已端到端验证。
- **软件 VoWiFi 方案（备而未选）**：SWu-IKEv2 + 自定义 ImsService（krazey/ims 路线）。可摆脱对系统 WFC 的依赖（比如运营商不放开 WFC 时），但要实现 IKE+IMS 全栈，风险与工程量高一个数量级。`spike/` 中已留 AKA 鉴权桥（SimAuthServer）与 vowifi_gateway 客户端代码。
- **PC/路由器级分流（否决）**：不能出门使用，已否决。

## 用户已定的决策

- 出口协议与节点：mihomo + 用户机场订阅（trojan 英国节点）。
- 手机：小米 12S Pro，Android 17，KSU。
- 目标卡：CTExcel（另一张联通卡不得受影响）。
- **硬性要求：与用户日常代理 App（Clash 类，VpnService 模式）同时在线工作**，不得要求关闭。
- **硬性要求：WFC 底座网络既可以是 WiFi，也可以是联通卡的蜂窝数据**（v1 验收，对应验收 #3）。
- **硬性要求：一旦某张卡被勾选接管，模块必须阻止该卡与基站注册**（防国际漫游驻网），同时 SIM 保持上电可鉴权（WFC 依赖 AKA），WebUI 取消勾选或模块停用时恢复原状。实现三选一（M0 实测定默认，WebUI 可切换）：A. `cmd phone set-allowed-network-types-for-users` 限制到 GSM-only（国内多数地区 2G 已退网）；B. bitmask 0（若被接受）；C. 手动选网锁定到不可用 PLMN。不能使用 `disable-physical-subscription`：它会断电 UICC，AKA 无法进行。

### 共存设计依据（以上两条如何成立）

- VpnService 抓不到 uid 1000（system_server/iwlan）的流量，用户日常代理 App 与 ASIM 不抢同一条流；ASIM mihomo 以 root 跑，其对节点的出站连接也绕过 VpnService——无双层套娃，直接走当前默认网络（WiFi 或蜂窝）。
- 唯一交叉点是 ip rule 优先级：ASIM 的 dst 型规则放 pref 8999（VpnService 常见规则在 15000+），只命中 ePDG IP，对用户 App 的其他分流零影响。
- 内核备选：若 mihomo TUN 与 VpnService 实测互抢（M0 冒烟不过），换 sing-box（TUN/TPROXY）再试，设计不变。

## 实现侧已定（对 builder 审查的回应）

- **节点固定**：每张卡绑定一个指定节点（不走 url-test/负载均衡组），避免出口 IP 漂移导致 IPsec 会话中途断掉。换节点是 WebUI 显式操作。（builder 问题 3）
- **SELinux**：`ip rule` 增删在本机 ksu 域实测可用；CarrierConfig 读取被拒，v1 仅公式推导。守护进程维持 shell 脚本方案，不引 sepolicy 补丁。（builder 问题 5）
- **go/no-go 前置**：M0 冒烟不过即换路线，不做无效工程。（builder 问题 1、2 的实测部分）

## 留给实现的细节

- asimd 用 shell 脚本 + toybox 还是编译一个小二进制：实现者定（倾向脚本，可审计）。
- ePDG 解析 DNS 的选择（走系统 DNS / 直连 223.5.5.5 / 走 mihomo 内置 DNS）：实现者测污染情况后定，默认系统 DNS。
- WebUI 与 asimd 的通信（文件状态 JSON vs 本地 HTTP）：实现者定，倾向文件 JSON + ksu exec 调用控制命令。
- mihomo 订阅的获取与更新方式（用户贴入订阅 URL，模块定期拉取）。
- IPv6 ePDG 的处理顺序（先 v4 验证，v6 后补）。

## 里程碑与 go/no-go 冒烟测试（M0，动工前必做）

spike 验证的是"PC Clash Verge 替代出口"，**没有**验证手机端 mihomo。M0 在真机上一口气回答三件事：

1. 订阅的 trojan 英国节点从**手机端**拨号时，UDP 500/4500/ESP(NAT-T) 能否可靠转发（很多机场禁非 DNS 的 UDP——此条不过，轻量路线整体作废，转 SWu 软件隧道路线，另立项）。
2. 手机端 mihomo TUN + `ip rule add to <epdg> pref 8999` 与系统 iwlan mark 0x20000 规则共存时，系统 IKE 包是否真被截进 TUN（不过则退路：iptables mangle OUTPUT 对 dst 打 mark）。
3. 与用户已装代理 App 同时开启时是否互抢路由。

## 风险与对策

1. **系统 iwlan 的 mark 路由可能绕过我们的规则**（rule 9999 在 mark 命中时是否优先）：dst 型 ip rule 放在更小 pref，理论必中；若仍失败，退路是 iptables mangle OUTPUT 对 dst 打新 mark。spike 阶段未在真机上验证此规则共存，**这是首个要在真机上冒烟的点**。
2. **用户已装的 VPN 代理 App 与新 TUN 冲突**：两者都是 tun + ip rule，可能互抢。对策：模块文档注明互斥/共存测试；必要时只在做 WFC 时挂 TUN。
3. **EE 对出口 IP geo 敏感**：必须英国节点；若换运营商（如美国卡），节点国家要跟着换——WebUI 提供按卡配置节点。
4. **ePDG IP 轮换导致会话中断**：解析周期（建议 5 分钟）+ 变更时平滑更新；WFC 系统本身有重连逻辑。
5. **CTExcel 卡被误启用驻网**：模块不主动改卡状态；用户自己保持该卡"启用但飞行模式"或设置数据关闭。文档写清。
6. **Android/系统更新改变 iwlan 行为**：模块绑定本机版本行为，升级后需复测（项目性质使然，无法根治）。
7. **订阅凭据安全**：配置落盘在模块目录（root only 权限），WebUI 不回显密码。

## 验收标准

1. 联通卡数据、其他 App 流量速度/路由与装模块前无差异（用测速与 traceroute 验证）。
2. 飞行模式 + WiFi（或不开飞行、卡不驻网状态下）CTExcel WFC 注册成功，可收发短信、通话。
3. 手机蜂窝网络（联通数据）下同样成立——即不依赖 WiFi。
4. 断开/切换网络后 5 分钟内自动恢复注册。
5. WebUI 能看到状态并切换节点。
