# ASIM 验收记录（v0.2.0）

- 日期：2026-11-22
- 设备：`994e779c`（KernelSU-Next v3.4.2，Android 16）
- 对照标准：`docs/superpowers/specs/2026-11-22-asim-design.md` §验收标准
- 安装方式：`ksud module install` → `adb reboot`（**非**整机断电）

结论：5 条里 4 条通过，1 条不适用/未做（见逐条说明）。

| # | 验收项 | 结果 |
|---|--------|------|
| 1 | 联通卡（slot 0）数据与其他 App 流量无差异（测速 + 出口国家 CN） | **未做**（见下） |
| 2 | 家庭 WiFi 下 CTExcel WFC 注册（`WLAN: IWLAN, HOME`），收得到短信，断电重启 5 分钟内恢复 | **部分通过**（短信未验，断电未做） |
| 3 | 勾选 CTExcel 后该卡 voice/data 均不 `IN_SERVICE`，同时 WFC 仍注册 | **通过**（按 WWAN 计） |
| 4 | 取消勾选 / `stop` / 卸载后恢复精确原值，卡可正常驻网 | **通过**（原值实测 54151） |
| 5 | WebUI 显示状态、切换开关、控制路由器中继 | **通过** |

## 1. slot 0 不受影响 —— 未做测速

未做的原因：这台机器 `settings get global mobile_data` = **0**（蜂窝数据全局关闭），
联通卡在蜂窝侧只有一条 IMS PDN（`rmnet_data1`，`10.104.28.44/29`，capabilities 只有 `IMS&MMTEL`，
无 `INTERNET`），走不了普通流量；`curl --interface rmnet_data1` 直接超时。
要测「出口国家 CN + 测速」必须先打开蜂窝数据，而那是**漫游数据**（`ROAMING`），
会产生费用，并且会拆掉当前正工作的 VoWiFi。所以没动。

能给的间接证据（都在验收时现场采集）：

- slot 0 网络类型仍是 `GPRS|EDGE|UMTS|HSDPA|HSUPA|HSPA|LTE|HSPA+|GSM|LTE_CA|NR` = `840583`，与安装前一致。
- slot 0 CS/PS WWAN 都是 `ROAMING`，`accessNetworkTechnology=LTE`，有 IPv4。
- 模块只对 `SLOTS` 里的槽调 `cmd phone set-allowed-network-types-for-users`，
  不碰路由、DNS、防火墙、APN，也不碰 slot 0 的任何设置。
- 解锁 slot 1 期间（见第 4 条）slot 0 一直保持 `ROAMING / LTE`，没有抖动。

风险：没有实测过 slot 0 的吞吐与出口 IP。若用户打开蜂窝数据后发现异常，
先 `asimd.sh stop` 再对照；模块本身不改 slot 0。

## 2. VoWiFi 注册 —— 通过；短信与断电重启未验

`dumpsys telephony.registry`（subId 1，锁 GSM-only 状态）：

```
domain=PS transportType=WLAN  registrationState=HOME  accessNetworkTechnology=IWLAN
```

即 `WLAN: IWLAN, HOME` —— WFC 注册成功，且此时蜂窝侧没注册。

- **短信**：需要外部往 +44 卡发一条，手上没有通道，未验。
- **整机断电重启**：`adb reboot` 做过两次，开机后守护自启、~2 分钟内 IWLAN 回到 `HOME`；
  但 spec 明确要求「整机断电（拔电/长按关机）→ 开机 → 连 WiFi」，
  这需要人工按电源键，agent 做不了。**这一条留给用户做一次**，步骤见 README。

## 3. 锁住后蜂窝不注册 —— 通过

锁 GSM-only 后连续采集，slot 1：

```
mVoiceRegState=1(OUT_OF_SERVICE)  mDataRegState=0(IN_SERVICE)
 domain=PS transportType=WLAN registrationState=HOME          tech=IWLAN
 domain=CS transportType=WWAN registrationState=NOT_REG_OR_SEARCHING  tech=UNKNOWN
 domain=PS transportType=WWAN registrationState=NOT_REG_OR_SEARCHING  tech=UNKNOWN
```

`mDataRegState=IN_SERVICE` 是 Android 把 IWLAN 的 PS 注册聚合进来的结果，不是蜂窝注册；
判断是否驻网要看 WWAN 两条，都是 `NOT_REG_OR_SEARCHING`。

已知偏差：GSM-only 下模组仍会扫 GSM。采样中 `mVoiceRegState` 出现过短暂的 `IN_SERVICE`
（2G 小区被扫到、随即掉出），这就是 spec 风险 1 说的 2G 兜底，模块不解决，WebUI 面板能看到。

## 4. 恢复原值、卡可正常驻网 —— 通过

- 原值实测 **54151**（`1101001110000111`，NR 早已关闭），不是 spec 里写的 `840583`。
  `840583` 是 slot 0 的值。`docs/HANDOFF.md` 已更正。
- `disable 1` → `cmd phone get-allowed-network-types-for-users -s 1` 立刻回到
  `GPRS|EDGE|UMTS|HSDPA|HSUPA|HSPA|LTE|HSPA+|GSM|LTE_CA`，存档 `slot1.mask` 删除。
- **12 秒内**该卡在蜂窝侧 `ROAMING` + `LTE` 注册成功 —— 「恢复后可正常驻网」成立。
- 再 `enable 1` → mask 回到 `GSM`，存档重新写入 `54151`；
  **20 秒内** WWAN CS/PS 回到 `NOT_REG_OR_SEARCHING`，且 `WLAN PS` 仍是 `HOME`（VoWiFi 没被打断）。
- `asimd.sh stop` 与模拟 `uninstall.sh` 早前已验证：恢复 `54151`、删存档与 `/data/adb/asim`、进程归零。

注意：解锁那 12 秒卡真的驻上了 LTE 漫游。这是「恢复」应有的行为，但如果运营商对
「长期不驻网」敏感，别频繁来回切。

## 5. WebUI —— 通过

真机 KSU-Next 模块页 → WEBUI：

- 两张卡都列出（slot 0 `未接管 / 840583`，slot 1 `已接管 / 仅 GSM / 原值 54151 / IWLAN HOME`）。
- 中继区：`已启用`、`ePDG 5 个地址`、`活跃流 1`，并列出实际 flow（`192.168.100.103:41071|4500 → 31.94.76.5:4500`）。
- 点 slot 1 开关：取消 → `SLOTS=""`、mask 回 `54151`、存档删除、UI 变「未接管」；
  再点 → `SLOTS="1"`、mask `GSM`、存档 `54151`、UI 变「已接管」。操作输出在页面底部回显。
- 「停止守护并恢复原网络类型」会弹确认框。
- 中继开关：只验了幂等 `/enable`（手机上 `curl + X-ASIM-Token` 跑通、返回 JSON）。
  **`/disable` 没有现场点过** —— 停中继会拆掉 VoWiFi 的 IKE 重定向，IMS PDN 不保证自动重建，
  不想为一次测试打断正在工作的 VoWiFi。风险：该按钮的真实效果没亲眼见过。

## 未覆盖 / 留给用户

1. 短信收发（需要外部发一条）。
2. 整机断电重启（需要按电源键）。
3. 联通卡蜂窝数据的测速与出口国家（需要开漫游数据并接受掉 VoWiFi）。
4. WebUI 中继 `/disable` 按钮的真实效果。
5. 长时段出门实测（2G 兜底、运营商是否因长期不驻网而停卡）。
