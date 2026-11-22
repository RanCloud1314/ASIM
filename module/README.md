# ASIM — 海外卡防驻网（KernelSU 模块）

让海外 SIM 卡在中国**永不驻网**、同时保持上电以支持 EAP-AKA，配合家庭 OpenWrt 上的
`asim-relay` 中继，系统 WiFi Calling 可以正常工作。

- 目标卡（默认 slot 1 / CTExcel 234/33）被锁到 GSM-only，不扫 LTE/NR；
- SIM 仍上电，系统 IWLAN 走 WiFi 建立，VoWiFi 注册、收发短信/通话都正常；
- 其他卡（slot 0）、其他 App、日常代理 App 完全不受影响。

## 前置

家庭路由器（或局域网里的 PC）上必须已部署 `asim-relay`（见 `router/asim-relay/README.md`），
并把 `epdg.epc.mnc033.mcc234.pub.3gppnetwork.org` 用 dnsmasq 指向中继所在机器。
没有这一步，手机上什么都不会发生，只是不会驻网。

## 安装

```powershell
.\build.ps1                                  # → asim-<version>.zip（PowerShell 5.1+）
```

把 zip 装进 KernelSU 管理器，然后**重启手机**。

装完在 KernelSU 管理器的模块页点 `WEBUI` 打开控制台。守护会自动读取
`/data/adb/asim/asim.conf` 里的 `SLOTS` 并在每个 tick 施加 GSM-only。

## 使用

WebUI 上：

- 每张物理卡一张卡片：网络类型掩码、语音/数据注册、IWLAN 状态、原值。
- 右上角开关 = 是否接管这张卡。打开会把它加进 `SLOTS` 并立刻锁 GSM-only；
  关掉会从 `SLOTS` 移除并立刻恢复原值。**已经接管的卡由守护每 60 秒重新施加**，
  所以手工改回去会被改回来，要退出保护请用开关或下面的「停止守护」。
- 中继区显示运行时长、丢包、协议和活跃流；`测节点延迟` 做一次真实 UDP 往返。
- 「路由器地址」留空即自动用手机默认网关，也可填 PC 上中继的地址。
- 「节点配置」可以改 trojan / VLESS / Hysteria2 节点，保存后在路由器上热生效。
- 「停止守护并恢复原网络类型」= `asimd.sh stop`：立刻停守护、按存档恢复每张已接管的卡。
  想重新开启保护就重启手机。

配置文件 `/data/adb/asim/asim.conf`（安装时如不存在才写入默认值，升级不覆盖）：

```sh
SLOTS="1"                        # 要接管的槽位，空格分隔；slot id 从 0 开始，不是 subId
ROUTER_API=""                    # 留空 = 自动用手机默认网关
ROUTER_TOKEN="3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b"
```

## 卸载

KernelSU 管理器卸载即可：`uninstall.sh` 会先停守护、把网络类型恢复成安装前的精确原值，
再删掉 `/data/adb/asim`。

## 手工恢复（应急）

优先用脚本，它按存档恢复，不会写错值：

```sh
adb shell 'su -c "sh /data/adb/modules/asim/scripts/nocamp.sh unlock 1"'
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh stop"'   # 连同停守护
```

存档被删了、只能手写时，本机 slot 1 的原值是 **54151**：

```sh
adb shell 'su -c "cmd phone set-allowed-network-types-for-users -s 1 1101001110000111"'
adb shell 'cmd phone get-allowed-network-types-for-users -s 1'       # 应回 GPRS|EDGE|...|LTE_CA
```

如果手写值不对导致手机没信号，把上面四条路都试一遍，最后一招是卸载模块后重启。

## 自检

```sh
adb shell 'su -c "sh /data/adb/modules/asim/scripts/nocamp.sh selftest"'
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh selftest"'
```

只测纯函数和解析，不做写操作。

## 已知限制

- 必须在家庭 WiFi 下才有 VoWiFi；出门只有普通蜂窝能力，本方案不提供（已证伪）。
- GSM-only 不防 2G 驻留：当地若仍有 GSM 覆盖，卡可能短暂驻留 2G 并出现 `IN_SERVICE`，
  WebUI 的注册状态能看到。M0-D 室内 90 秒未见驻留，出门长时段未测。
- 切换飞行模式或断开 WiFi 会拆掉 IMS PDN 且不会自动重建，需重连 WiFi；
  基带 IWLAN 状态卡死时只能**整机断电**（`adb reboot` 有时不够）。
- `mVoiceRegState` / `mDataRegState` 是 Android 聚合后的值，`data` 显示 `IN_SERVICE`
  可能来自 IWLAN 而不是蜂窝。判断是否驻网看 WebUI 卡片里的 IWLAN 和 `dumpsys` 里的 WWAN 两条。
- 守护被 `kill -9` 且没人跑 `stop`/`uninstall` 时，卡会留在 GSM-only；存档还在，
  用上面的手工恢复即可。守护收到 TERM 时只清理退出，不去改卡（在 trap 里调 `cmd phone` 必然失败）。
- 运营商侧可能要求周期性蜂窝注册，长期只走 WiFi 有被停卡的风险；属于运营策略，模块不解决。
- 系统/基带升级可能改变 IWLAN 行为，升级后需要复测。
