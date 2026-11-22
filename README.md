# ASIM — 让海外卡「永不驻网」，只用 WiFi Calling

把一张在中国大陆漫游的海外 SIM 卡（实测 CTExcel 英国卡，234/33）锁到 **GSM-only**，
让它**不再驻留中国运营商的 LTE/NR 网络**，但 SIM 保持上电，EAP-AKA 仍可用；
VoWiFi（系统 WiFi Calling）的 IKE/IPsec 由家里的 OpenWrt 路由器转发到英国的 ePDG，
于是**光靠家庭 WiFi 就能收短信、打电话**，蜂窝侧全程不注册。

```
        ┌──────────── 手机 ────────────┐        ┌──────── 家里 OpenWrt ────────┐
        │ KSU 模块 asim                │        │ asim-relay (Go)             │
        │  ├ 守护 asimd（每 60s 重施加）│        │  ├ dnsmasq: ePDG → 路由器自己 │
        │  └ WebUI 状态面板/开关        │        │  └ :500/:4500 收包            │
        │                              │        └──────────┬──────────────────┘
        │ 基带 --IKE 500/4500──────────┼───────────────────┘
        │      （不经过手机 IP 栈）      │                   │ trojan / vless / hy2
        │ 蜂窝侧: GSM-only, 不注册      │                   ▼
        └──────────────────────────────┘        英国节点 → EE ePDG 31.94.76.x
```

- **手机侧**只做一件事：把指定卡槽的网络类型改成 GSM-only，并盯着别被系统改回去。
- **路由器侧**才是真正的流量中转，手机上的 TUN / ip rule / 代理 App 都抓不到基带的 IPsec
  （实测见 `m0/M0-B-FINDINGS.md`），所以只能在手机之外拦。

## 为什么不是「飞行模式 + WiFi」

飞行模式会断电 UICC，EAP-AKA 就拿不到 SIM 凭证，VoWiFi 注册必失败。
所以必须让卡**上电但不驻网**——这就是 GSM-only 的目的。

## 前置条件

| 项 | 要求 |
|---|---|
| 手机 root | KernelSU 或 KernelSU-Next（Magisk 未适配：WebUI 用的是 `ksu.exec`） |
| 系统 | Android 13+（用到 `cmd phone get/set-allowed-network-types-for-users`） |
| 路由器 | OpenWrt，能跑 Go 二进制，x86_64 已实测 |
| 节点 | 一个能到英国 ePDG 的节点：`trojan` / `vless` / `hysteria2` 任一（见 `router/asim-relay/relay.json`） |
| SIM | 一个走 VoWiFi 的海外卡；本仓库默认 CTExcel 英国卡（MCC/MNC 234/33） |

## 一、路由器：先让 VoWiFi 能通

手机上的模块只负责「不驻网」，VoWiFi 能不能成完全取决于这一步。**先把路由器配好**，
用手机原生的 WiFi Calling 验通一次（不装模块也能验），再往下做。

```sh
# 1) 开发机上编译（需要 Go）
cd router/asim-relay
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o asim-relay-linux-amd64 .

# 2) 传上去
scp asim-relay-linux-amd64 root@192.168.100.1:/usr/bin/asim-relay
scp relay.json                root@192.168.100.1:/etc/asim/relay.json
scp asim-relay.init           root@192.168.100.1:/etc/init.d/asim-relay
ssh root@192.168.100.1 'chmod 755 /usr/bin/asim-relay /etc/init.d/asim-relay; chmod 600 /etc/asim/relay.json'

# 3) 把 ePDG 域名指到路由器自己（只要一次，写进 /etc/dnsmasq.conf 就持久）
ssh root@192.168.100.1 "echo 'address=/epdg.epc.mnc033.mcc234.pub.3gppnetwork.org/192.168.100.1' >> /etc/dnsmasq.conf && /etc/init.d/dnsmasq restart"

# 4) 起服务
ssh root@192.168.100.1 '/etc/init.d/asim-relay enable && /etc/init.d/asim-relay restart'
```

改 `relay.json` 里的节点地址、密码、`listen_addr`（默认 `192.168.100.1`）。
节点也可以装好后在 WebUI 里改，不用再登路由器。
换网段要同时改这里和 dnsmasq 的 `address=`。细节见 [`router/asim-relay/README.md`](router/asim-relay/README.md)。

### 中继装在电脑上（可选）

Release 里有 `asim-relay-windows-amd64.exe` 和 `asim-relay-linux-amd64`。
把 `relay.json` 的 `listen_addr` / `api_listen` 改成 PC 的局域网 IP 或 `0.0.0.0`，
手机 WebUI 的「路由器地址」填这台 PC 的地址即可（不填则自动用手机默认网关）。
Windows 上放行入站 UDP 500/4500 和 TCP 8080。

验证：手机连上家里 WiFi → 打开「WLAN 通话」→ 状态栏出现 WiFi 通话标识、能收短信。
不通就看路由器 `logread | grep asim-relay` 有没有 `flow ... -> 31.94.76.x:500`。

## 二、手机：装 KSU 模块

```powershell
.\build.ps1                                 # → asim-v0.3.0.zip + 两个平台的 relay（PowerShell 5.1+）
```

1. KernelSU 管理器 → 模块 → 安装 `asim-v0.3.0.zip`；
2. **重启手机**（不是热重载，守护在 `service.sh` 里等 `sys.boot_completed`）；
3. 模块页点 `WEBUI` 打开控制台。

装完会在 `/data/adb/asim/` 放一份配置（**已存在就不覆盖**，升级安全）：

```sh
SLOTS="1"                                   # 要接管的槽位，空格分隔；slot id 从 0 开始，不是 subId
ROUTER_API=""                               # 留空 = 自动用手机默认网关；也可填 http://IP:8080
ROUTER_TOKEN="改成你自己的 token"
```

> ⚠️ **`SLOTS` 填的是 slot id，不是 subId。** 用
> `adb shell "dumpsys isub | grep -oE 'id=[0-9]+ .*simSlotIndex=[0-9]+'"` 自己核对。
> 双卡机常见的坑：`-s 0` 和 `-s 1` 对应哪张卡，跟你在设置里看到的顺序不一定一致。

## 三、使用教程

### 打开 WebUI

KernelSU 管理器 → 模块 → ASIM → `WEBUI`。页面每 10 秒自动刷新一次。

### 界面长什么样

```
┌ slot 1 · CTExcel   subId 1              [ ●] ┐
│  网络类型   语音/数据            IWLAN        │
│  仅 GSM     OUT_OF_SERVICE / …   HOME        │
│  已接管    原值 54151                         │
└──────────────────────────────────────────────┘
┌ 路由器地址                                    ┐
│ [ http://192.168.100.1:8080        ]  [保存]  │
│ [ 使用默认网关 ]                              │
└──────────────────────────────────────────────┘
┌ 路由器中继                                    ┐
│ 已启用  vless  v0.3.0  延迟 243 ms            │
│ 运行时间    丢包        活跃流                 │
│ 2 小时 2 分  0          2                     │
│ 192.168.100.103:41071|4500 → 31.94.76.5:4500 │
│        [ 停用中继 ]  [ 测节点延迟 ]            │
└──────────────────────────────────────────────┘
┌ 节点配置                                      ┐
│ 协议 / 地址 / 端口 / 密码或UUID / SNI          │
│ □ 跳过证书校验   ▸ 高级（VLESS 传输 / REALITY）│
│            [ 读取 ]  [ 保存 ]                  │
└──────────────────────────────────────────────┘
┌ 应急                                          ┐
│        [ 停止守护并恢复原网络类型 ]             │
└──────────────────────────────────────────────┘
```

- **卡片右上角开关 = 是否接管这张卡。**
  打开 → 加进 `SLOTS` 并立刻锁 GSM-only；关掉 → 移出 `SLOTS` 并**立刻恢复原值**。
  已经是「已接管」的卡由守护每 60 秒重新施加一次，所以手工用 `cmd phone set...` 改回去
  会在一分钟内被改回来 —— 想退出保护请用开关，别跟守护打架。
- **网络类型**：接管后显示「仅 GSM」，未接管显示十进制掩码。
- **语音 / 数据**：Android 的 `ServiceState` 聚合值。**`data` 显示 `IN_SERVICE` 不代表驻网**，
  它可能来自 IWLAN。判断有没有驻网看 IWLAN 和下面「怎么确认没驻网」。
- **IWLAN**：`HOME` = VoWiFi 注册成功，这才是你要的绿色状态。
  其它值里 `NOT_REG_OR_SEARCHING` 表示还没连上（刚开机等 1~3 分钟）。
- **路由器中继**：显示运行时长、丢包、协议、ePDG 地址数和活跃流；`测节点延迟` 会做一次真实 UDP 往返（含握手），花几百毫秒到几秒。
- **路由器地址**：留空即跟随手机默认网关；中继装在电脑上时填电脑的 LAN 地址。
- **节点配置**：改协议 / 地址 / 端口 / 密码或 UUID / SNI 后点保存，路由器上立刻生效（监听地址变更除外，需要重启 relay）。VLESS 的传输方式、flow、REALITY 公钥在「高级」里。
- **操作输出**：每次点按钮的真实 stdout/stderr 都会回显在页面底部，排障先看这里。

### 典型一天

1. 回家，连上家里 WiFi（第一次要手动连，模块不帮你连 WiFi）；
2. 打开 WebUI，确认 slot 1 是「已接管 / 仅 GSM / IWLAN HOME」；
3. 等 1~3 分钟 VoWiFi 注册完成，状态栏出现 WiFi 通话标识；
4. 收短信、打电话；
5. 出门——没 WiFi 就没有 VoWiFi（本方案不给蜂窝兜底，见下面的限制）。

### 怎么确认「真的没驻网」

```sh
adb shell 'su -c "dumpsys telephony.registry"' | Select-String 'subId=1 '
```

```
domain=PS transportType=WLAN registrationState=HOME                    ← VoWiFi，要的
domain=CS transportType=WWAN registrationState=NOT_REG_OR_SEARCHING    ← 蜂窝语音，要的
domain=PS transportType=WWAN registrationState=NOT_REG_OR_SEARCHING    ← 蜂窝数据，要的
```

三条 WWAN 只要出现 `IN_SERVICE` / `ROAMING`，就是驻上了。

## 四、卸载与应急恢复

正常卸载：KernelSU 管理器里卸载模块 —— `uninstall.sh` 会先停守护、按存档恢复每张卡的**原始**
网络类型，再删掉 `/data/adb/asim`。

手工恢复（脚本按存档来，不会写错值，优先用它）：

```sh
adb shell 'su -c "sh /data/adb/modules/asim/scripts/nocamp.sh unlock 1"'
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh stop"'   # 连守护一起停
```

存档也没了，只能手写（本机实测原值 slot 1 = **54151** = `1101001110000111`）：

```sh
adb shell 'su -c "cmd phone set-allowed-network-types-for-users -s 1 1101001110000111"'
adb shell 'cmd phone get-allowed-network-types-for-users -s 1'       # 应回 GPRS|EDGE|…|LTE_CA
```

**每台机子的原值不一样，一定要以自己机器的 `get` 读数为准**（`slot<id>.mask` 里存的就是它）。

心跳都没了（模块删了、卡还是没信号）：手动把上面那条手写命令跑一遍，再重启手机。

## 五、自检

```sh
adb shell 'su -c "sh /data/adb/modules/asim/scripts/nocamp.sh selftest"'
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh selftest"'
```

只跑纯函数与解析，不写设备。两个都 `selftest passed` 才算安装完整。

## 六、已知限制（都是真的，不是免责声明）

- **出门没有 VoWiFi。** 蜂窝底座方案已证伪（见 `m0/`），本方案只在家庭 WiFi 下工作。
- **GSM-only 不防 2G 驻留。** 当地若仍有 GSM 覆盖，卡可能短暂驻留 2G 并出现 `IN_SERVICE`。
  室内 90 秒未见，长时段出门未测。
- **切飞行模式 / 断 WiFi 会拆掉 IMS PDN，且不会自动重建。** 要重连 WiFi，
  基带 IWLAN 状态卡死时只能**整机断电**（`adb reboot` 有时不够）。
- **守护被 `kill -9` 且没人跑 stop/uninstall** 时，卡会留在 GSM-only；存档还在，用上面手工恢复。
- **运营商可能要求周期性蜂窝注册**，长期只走 WiFi 有被停卡的风险；属运营策略，模块不解决。
- **系统/基带升级可能改变 IWLAN 行为**，升级后需要复测。
- WebUI 的「停用中继」会拆掉 VoWiFi 的 IKE 重定向，**回不来可能要重启**，别在等电话时点。
- **VLESS 内嵌 Xray 引擎，二进制约 32MB**（原 trojan 版约 7MB）；路由器 x86_64 无压力，老 ARM 路由器先看磁盘。
- **Hysteria2 走 QUIC**，被墙或 UDP 被封时测延迟会超时，属网络问题；本地回环已测通。
- 节点密码/UUID 存在 `relay.json`（0600），改配置时经 LAN 明文 HTTP 传输，和 token 同等级信任，别在不可信网络里用。

## 仓库结构

```
module/            KSU 模块（nocamp.sh 防驻网、asimd.sh 守护、relay.sh 中继桥、webroot/ WebUI）
router/asim-relay/ 中继（ePDG IKE/ESP 转发；trojan / vless / hysteria2）
m0/                M0 阶段实测记录与结论（为什么是这个方案）
docs/              交接文档、设计 spec、实施 plan、验收记录
build.ps1          打包 module/ → asim-vX.Y.Z.zip，并编译两个平台的 relay
```

## 版本

当前 `v0.3.0`：防驻网 + 守护 + WebUI + 多协议中继（trojan / vless / hysteria2）+ WebUI 改节点/路由器地址/测延迟 + 真机验收。
逐条验收证据见 [`docs/superpowers/plans/2026-11-22-asim-acceptance.md`](docs/superpowers/plans/2026-11-22-asim-acceptance.md)。
