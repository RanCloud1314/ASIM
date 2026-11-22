# ASIM 交接文档

> 新上下文从这里开始读。读完本文只需要再看 `docs/superpowers/plans/2026-11-22-asim.md` 的 Task 5 就能开工。

日期：2026-11-22（M0-C / M0-D 实测完成后）

## 一句话

CTExcel 英国卡（234/33）在中国**永不驻网**，但 SIM 保持上电；在家 WiFi 下系统 VoWiFi 正常工作。
手机侧 KSU 模块只负责防驻网，VoWiFi 分流在家庭 OpenWrt（或局域网 PC）上（mihomo TUN → 英国节点 → EE ePDG；
Go 中继只保留控制面）。

## 当前状态

| 部分 | 状态 |
|---|---|
| 路由器 `asim-relay`（Go） | ✅ v0.3.1 只做控制面（`/status` `/ping` `/config`），节点变更时生成 mihomo 配置并热重载 |
| 路由器 mihomo | ✅ v1.19.34：TUN `asim0` 只接管 `31.94.76.0/24`，数据面已切到它（2026-11-22 23:3x），实机验证见 `docs/VOWIFI-DEBUG.md` |
| 防驻网选型 | ✅ 定为 **A（GSM-only）**；B 被基带拒绝；C 未测（不做） |
| KSU 模块 | ✅ v0.4.0：防驻网守护 + WebUI（卡槽/路由器地址/节点配置/延迟）+ 自检；含 asimd pid 复用修复 |
| 手机当前状态 | 两张卡都可正常驻网；adb 在线；slot 1 原值 `user=54151`（NR 已关） |

## 链路（已验证）

```
手机基带 --IKE 500 / ESP-in-UDP 4500--> 31.94.76.x
        --(默认网关)--> 路由器 --TUN asim0 (mihomo)--> 英国节点 pokemon888-go.pikaholo.com:56007 --> EE ePDG 31.94.76.x
```

- dnsmasq 不再覆盖 ePDG 域名（旧的 `address=` 行已删），手机解析到真实 `31.94.76.x`；
- mihomo 的 TUN 只路由 `31.94.76.0/24`，规则 `IP-CIDR,31.94.76.0/24,ASIM-UK,no-resolve`，
  其余流量 `MATCH,DIRECT`；
- `asim-relay` 不再转发，只在启动 / `PUT /config` 时写 `/etc/asim/mihomo/config.yaml` 并热重载。

细节见 `m0/M0-C-RESULT.md`、`router/asim-relay/README.md`。

## 环境

- 手机：小米 12S Pro（unicorn），Android 17，KernelSU 3.4.2，adb 序列号 `994e779c`。
- 路由器：OpenWrt 25.12.6 x86_64（apk 包管理），LAN `192.168.100.1`，`root` / `password`。
  - SSH helper：`python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "<命令>"`（paramiko；**没有 `timeout` 命令，没有 `curl`**，用 `wget -q -O -`）。
  - 部署 helper：`python C:\Users\22938\AppData\Local\Temp\opencode\rt\deploy.py "<命令>"`（走 exec stdin 传文件，路由器无 sftp-server）。
- 路由器上已有：`/usr/bin/asim-relay`、`/etc/asim/relay.json`、`/etc/init.d/asim-relay`、`/usr/bin/mihomo`、`/etc/init.d/mihomo`、`/etc/asim/mihomo/config.yaml`、`tcpdump`。
- mihomo 控制器：`http://127.0.0.1:9090`（只绑本机，无 secret）。
- API token：`3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b`，`http://192.168.100.1:8080/status`。

## 卡槽映射（必须记住，踩过坑）

`cmd phone` 的 `-s` 是 **slot id，从 0 开始**，不是 subId：

| slot | subId | 卡 | 说明 |
|---|---|---|---|
| 0 | 2 | 联通（漫游显示 CHN-UNICOM，来自 Lebara NL 卡） | 正常使用，**不得动** |
| 1 | 1 | **CTExcel 234/33（目标）** | 要锁 |

映射来源：`adb shell "dumpsys isub | grep -oE 'id=[0-9]+ .*simSlotIndex=[0-9]+'"`。
写代码时**不要硬编码**，用解析结果。

## 已验证的硬事实（别重复踩）

### 命令语法

`set-allowed-network-types-for-users` 的参数是**二进制字符串**（源码 `Long.parseLong(s, 2)`），不是十进制也不是名字：

```sh
cmd phone get-allowed-network-types-for-users -s 1   # 返回展示名，人读用
cmd phone set-allowed-network-types-for-users -s 1 1000000000000000   # GSM-only
```

各槽的原值**不同，必须以现场读数为准**（2026-11-22 实测）：

| slot | 卡 | `user=` | 二进制 |
|---|---|---|---|
| 0 | 联通 / Lebara | `840583` | `11001101001110000111`（含 NR） |
| 1 | CTExcel | `54151` | `1101001110000111`（**NR 已关**） |

**任何恢复操作都必须写回该槽当时的精确值，不要用「全选」或别的槽的值。**
模块的做法是 lock 前把现场值存进 `/data/adb/asim/slot<slot>.mask`，unlock 时按存档恢复。

### 三种防驻网方案

| 方案 | 结论 |
|---|---|
| **A. GSM-only**（`1000000000000000` = bit15） | ✅ 采用。命令 `completed`，无 RIL 异常，subId=1 完全没有 WWAN 注册条目 |
| B. bitmask 0 | ❌ **基带不支持**：`CommandException: REQUEST_NOT_SUPPORTED`。DB 显示 `UNKNOWN` 是假象，模仍按旧配置跑 |
| C. 手动锁 PLMN | 未测，`cmd phone` 无此命令，需 `service call phone`。**已决定不做** |

判据必须看两处，只看 `get` 会被骗：logcat 无 `CommandException`，且 `dumpsys telephony.registry` 里该 subId 的 WWAN `NetworkRegistrationInfo` 消失或为 `NOT_REG_SEARCHING`。

### 权限

`cmd phone` 在 `u:r:ksu:s0`（KSU 的 `su`）下可正常用，`id` = `uid=0(root)`。模块脚本可以调。

### 基带行为

- 切飞行模式 / 断 WiFi 会**拆掉 IMS PDN 且不自动重建**，恢复 VoWiFi 要重连 WiFi 或**整机断电**（AP 重启无效）。
- 基带 IWLAN 状态只在整机断电后清干净。
- 不可用：`cmd phone restart-modem`（无 carrier 权限）、`remoteproc` 写（SELinux 拦）。
- 禁止 `disable-physical-subscription`（会断电 UICC，AKA 不可用）。

### 恢复原值（手工应急）

```sh
# 优先：走模块自己的存档恢复（最准确的「原值」）
adb shell 'su -c "sh /data/adb/modules/asim/scripts/nocamp.sh unlock 1"'

# 没有存档时（例如模块已被删）：手写该槽的原值，slot 1 当前是 54151
adb shell 'su -c "cmd phone set-allowed-network-types-for-users -s 1 1101001110000111"'
adb shell 'cmd phone get-allowed-network-types-for-users -s 1'
```

## 掉线根因（2026-11-22 晚，v0.3.1）

> 2026-11-22 23:3x 起数据面已换成路由器上的 mihomo（PC 上的 A/B 证明它能撑过 30 分钟
> 重注册）。本节记录的是换之前对 asim-relay 的排查；换完后的 45 分钟判卷结果见
> `docs/VOWIFI-DEBUG.md`。

症状：开机后 VoWiFi 正常，约 5–11 分钟后 IMS PDN 被拆（`PREFERRED_DATA_SWITCHED` /
`CONNECTIVITY_SERVICE_UNWANTED`），voice 变 `OUT_OF_SERVICE`，约 5 分钟后自恢复；WiFi 全程没断。

抓包 + 日志定位到中继的两个缺陷（已修）：

1. `resolveOnce` 每 10 分钟重解析 ePDG，DoH 答案只要**换序**就 `killFlows()`，把一条正常
   的 IPsec 会话掐断。现在只在「正在用的 IP 从答案集里消失」时才换、才切流。
2. flow 的读协程出错只 `Close` 不 drop；而上游 trojan UDP 会话会被节点在**空闲 60 秒**后
   `EOF`（实测 `flow ...: read: EOF`）。这期间写仍然成功、回包永远不来，手机的重传全投进
   黑洞，必须等 180s idle 才重建——正好对上 5 分钟空窗。现在读出错立刻 drop，且「只发不收
   超过 25s」主动回收。

附带结论：`.5` 和 `.9` 都能完整走完 IKE INIT/AUTH + 双向 ESP（两段抓包各有 36 条），
所以「换到 .5 就坏」不成立，坏的是中继自己把会话切了/挂死了。

另：强制重试（飞行模式）多次之后，IMS 会直接回 SIP `403 Registration_Error14`，
PDN 建起来 2 秒内就被撤——这是运营商侧的短暂拒绝，停下等退避即可，不要连着强拉。

补一条 19:50 复盘的硬证据：那段抓包（19:20:47 注册 → 19:50:48 拆线）**隧道全程健康**，
19:45–19:50 每 20 秒一个 keepalive、收发各 1 个，最后两个 MOBIKE INFORMATIONAL 也都拿到了
回应；拆线第一行是 `TNA-102: NetworkAgent channel lost` 紧跟
`onTearDown: reason=CONNECTIVITY_SERVICE_UNWANTED`，也就是 IMS 侧主动放掉了 IWLAN 请求，
不是中继丢包。中继日志在同一时间段没有任何错误/切 IP/回收。所以这条 30 分钟周期掉线
不在中继，下次再遇到先看手机侧 IMS（`ImsPhone`/`getImsRegistrationTechnology=-1`）。

## asimd 重启后不启动（2026-11-22 晚，已修）

重启手机后线程守护没起来，`state.json` 停更、槽位不锁、WiFi Calling 也不来。原因：
`asimd.pid` 里的 pid 在重启后被别的进程复用，`is_running` 只做 `kill -0` 就判「already
running」直接退出；`stop` 还会去 kill 那个无关进程。现在 `is_running` 同时校验
`/proc/<pid>/cmdline` 含 `asimd`，`stop` 也走同一个判断，selftest 加了两个用例。

## 下一步

数据面已切到 mihomo；下一步是看完 45 分钟窗口的判卷结果并把结论写进
`docs/VOWIFI-DEBUG.md`。
（`docs/superpowers/plans/2026-11-22-asim.md` 的实施计划已全部落地，只剩验收。）

## 文件地图

| 路径 | 说明 |
|---|---|
| `docs/HANDOFF.md` | 项目总交接（本文） |
| `docs/VOWIFI-DEBUG.md` | **周期性掉线排错专用交接**：403 时间线、已排除项、下一步实验、调试命令 |
| `docs/superpowers/specs/2026-11-22-asim-design.md` | 设计权威文档（v3） |
| `docs/superpowers/plans/2026-11-22-asim.md` | 实施计划，逐 Task 执行 |
| `m0/M0-B-FINDINGS.md` | 为什么手机侧分流不可行的决定性证据 |
| `m0/M0-C-RESULT.md` | 路由器中继验证记录 |
| `m0/M0-D-RESULT.md` | 防驻网三方案实测记录 |
| `router/asim-relay/` | 路由器 Go 控制面源码 + 配置 + init 脚本 |
| `router/mihomo/` | mihomo 的 init 脚本与安装/验证/回滚说明 |
| `module/` | KSU 模块（当前只有骨架） |
| `spike/` | 作废路线的留档，不要动 |

## 不许做的事

- 不要试图在手机侧做 IKE/TUN/ip rule 分流（已实测无效，见 `m0/M0-B-FINDINGS.md`）。
- 不要动 slot 0（联通卡）的任何网络设置。
- 不要用 `disable-physical-subscription`。
- 不要在 `router/asim-relay` 里加「顺便代理其他流量」的功能——blast radius 必须保持在 ePDG 一个域名。
