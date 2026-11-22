# M0-D：防驻网三方案实测

## 命令语法（踩过的坑）

`cmd phone set-allowed-network-types-for-users` 的第三个参数是 **二进制字符串**（源码 `Long.parseLong(s, 2)`），不是十进制、不是名字串：

```sh
# 读（返回展示名）
cmd phone get-allowed-network-types-for-users -s 1
# 写（二进制）
cmd phone set-allowed-network-types-for-users -s 1 11001101001110000111
```

`-s` 是 **SIM slot id，从 0 开始**（`dumpsys isub` 里 `simSlotIndex`）：

| slot | subId | 卡 |
|---|---|---|
| 0 | 2 | 联通 / Lebara（roaming CHN-UNICOM） |
| 1 | 1 | **CTExcel（目标）** |

误把 `-s 2` 当 slot 会报 `2 is not a valid SLOT_ID`。

本机出厂/原值：`user=840583` = `GPRS|EDGE|UMTS|HSDPA|HSUPA|HSPA|LTE|HSPA+|GSM|LTE_CA|NR`
= 二进制 `11001101001110000111`。

## 结论

| 方案 | 结果 | 证据 |
|---|---|---|
| **A. GSM-only** (`1000000000000000`) | ✅ **可用** | 命令返回 `completed`，无 RIL 异常；90 秒内 subId=1 的 `mNetworkRegistrationInfos` **完全没有 WWAN 条目**（只剩 `WLAN PS = HOME`），`gsm.operator.alpha[1]` 为空 |
| **B. bitmask 0** (`0`) | ❌ **基带不支持** | 返回 `failed`，logcat：`PhoneInterfaceManager: setAllowedNetworkTypesForReason: CommandException: REQUEST_NOT_SUPPORTED`。DB 里显示 `UNKNOWN` 是假象，模仍按旧配置跑 |
| **C. 手动锁不可用 PLMN** | ⚠️ 未测 | `cmd phone` 无网络选择命令，只能走 `service call phone`（transaction code 需另查） |

## 对照组：不锁时会发生什么（恢复原值后约 1 分钟）

```
rmnet_data1@rmnet_ipa0 UNKNOWN  10.104.26.213/30     ← 蜂窝数据 PDN 已建立
mVoiceRegState=0(IN_SERVICE)
mDataRegState=0(IN_SERVICE)
```

即：完整配置下，这张卡会**很快驻网并拿到运营商内部 IPv4**。这就是模块要拦的状态，也是「不锁」的基线。

对比 GSM-only 时：`mNetworkRegistrationInfos` 里只剩 `WLAN PS`，一条 WWAN 都没有。

## 重要的方法学教训

mask=0 那次试验里，「90 秒没驻网」**不能**算方案有效的证据 —— 因为模根本没收到配置。判据必须是二选一：

1. logcat 里没有 `CommandException`（配置真的下发到模），**且**
2. `dumpsys telephony.registry` 里 subId=1 的 WWAN `NetworkRegistrationInfo` 为 `NOT_REG_SEARCHING` / 不存在。

只看 `get-allowed-network-types-for-users` 会骗人：它读的是 subscription DB，不是模的状态。

## 驻网尝试的观测（mask 未生效时的对照）

模处于完整配置时，CTExcel 会对中国移动 LTE 发起尝试并被**网络侧拒绝**：

```
domain=PS transportType=WWAN registrationState=DENIED rejectCause=15
cellIdentity=CellIdentityLte{ ... mMcc=460 mMnc=00 mAlphaLong=CHINA MOBILE mBands=[3] }
```

rejectCause=15 = `No suitable cells in tracking area`，即移动侧不认这张卡。**但这仍然是「已向中国基站发出注册请求」**，不等于「零接触」。所以 B 方案即使能写进去也不够，A 的「不扫描」才是目标形态。

## 恢复

```sh
cmd phone set-allowed-network-types-for-users -s 1 11001101001110000111
cmd phone get-allowed-network-types-for-users -s 1   # 期望 GPRS|EDGE|...|NR
```

slot 0（联通）从头到尾未修改，`allowedNetworkTypesForReasons=user=840583,power=54151` 原值不变。

## 遗留

- A 是**概率性**防护：国内 GSM 若仍有覆盖（移动 2G 部分在网），卡可能驻留 2G。需长时段观察 + 出门实测。
- C 未测；若要做 WebUI 可选模式，需先确定 `service call phone` 的 transaction code。
- 切换飞行模式会拆掉 IMS PDN 且不自动重建（已知基带行为），恢复 VoWiFi 需重连 WiFi 或整机断电。
