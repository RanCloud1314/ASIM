# ASIM — 海外卡防驻网（KernelSU 模块）

让海外 SIM 卡在中国**永不驻网**、同时保持上电以支持 EAP-AKA，配合家庭 OpenWrt 上的
`asim-relay` 中继，系统 WiFi Calling 可以正常工作。

- 目标卡（默认 slot 1 / CTExcel 234/33）被锁到 GSM-only，不扫 LTE/NR；
- SIM 仍上电，系统 IWLAN 走 WiFi 建立，VoWiFi 注册、收发短信/通话都正常；
- 其他卡（slot 0）、其他 App、日常代理 App 完全不受影响。

## 前置

家庭路由器上必须已部署 `asim-relay`（见 `router/asim-relay/README.md`），
并把 `epdg.epc.mnc033.mcc234.pub.3gppnetwork.org` 用 dnsmasq 指向路由器 LAN IP。
没有这一步，手机上什么都不会发生，只是不会驻网。

## 安装

```sh
.\build.ps1                                  # → asim-<version>.zip（PowerShell 5.1+）
KernelSU 管理器 → 安装 zip → 重启手机
```

安装后在 KernelSU 管理器里打开模块 WebUI 管理。

## 卸载

KernelSU 管理器卸载即可：`uninstall.sh` 会把网络类型恢复成安装前的精确原值并停掉守护。

## 手工恢复（应急）

```sh
adb shell 'su -c "cmd phone set-allowed-network-types-for-users -s 1 11001101001110000111"'
```

## 已知限制

- 必须在家庭 WiFi 下才有 VoWiFi；出门只有 WiFi 通话外的蜂窝能力（本方案不给）。
- GSM-only 不防 2G 驻留：当地若仍有 GSM 覆盖，卡可能驻留 2G。
- 切换飞行模式或断开 WiFi 会拆掉 IMS PDN 且不会自动重建，需重连 WiFi 或整机断电。
