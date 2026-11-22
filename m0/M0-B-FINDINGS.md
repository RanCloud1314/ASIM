# M0-B 排查记录（真机：小米 12S Pro / unicorn / Android 17 / KSU 3.4.2）

## 目标
证明「系统 WFC 的 IKE 能被内核 `ip rule` 抓进 mihomo TUN」，这是轻量路由方案的前提。

## 已确证的事实
1. **本机 IWLAN 由基带实现，不是内核/用户态 IKE**
   - `pm list packages` 只有 `vendor.qti.iwlan`，无 `com.android.iwlan`。
   - `IWlanService.apk` 内只有 `classes.dex`（1.5 MB），无 native lib；`/vendor/lib64` 无 `libike`/`charon`（只有 `libqtikeymaster4.so`）。
   - 触发链在 `imsd`：`QMI_IMS_DCM_PDP_ACTIVATE_REQ`，APN=`ims`，RAT type=2(IWLAN)，由 QMI 交给基带。
   - 数据网落在 `rmnet_data1`（modem IWLAN data call），不是内核 ipsec 接口。
2. 全程无内核侧痕迹：`/proc/net/udp{,6}` 无 500/4500 socket；`ip xfrm state` 为空；`/proc/net/xfrm_stat` 全零。
3. `vendor.qti.iwlan` 三个服务（QualifiedNetworksService / IWlanDataService / IWlanNetworkService）均已绑定，QMI 响应在流动，日志只有 `RESP_WAKE_LOCK_TIMEOUT` 噪声。
4. WFC 开关状态正常：`isWifiCallingEnabled=1`，`ImsManager: available=true, provisioned=true, mode=2`。
5. 飞行模式 ON 时基带仍可做 IWLAN（14:38 实测 IMS PDN 建立成功），说明 WFC 与防驻网不冲突。
6. 家庭 WiFi 直连（真实 ePDG IP 31.94.76.2/5/6/9/10）时，IMS PDN **从不建立**。
7. PC 热点 + Clash（DNS 返回 fake-ip 198.18.0.41）时，IMS PDN **在 network validation 通过后自动建立**（netId 109，CONNECTED）。
   → 说明框架/基带用的是 DNS 解析出来的那个地址，PC 侧 Clash 兜住了 UDP 500。
8. `cmd phone ims disable -s 1` 会把 WFC 开关关掉并把 IMS 栈弄坏（此后 UI 切换/重启才恢复），M0 后续不要再用。

## 已排除
- DNS 污染/解析失败（真实 IP、fake-ip 都能解析）
- IPv6 优先（ePDG 无 AAAA）
- SIM 未上电（`areUiccApplicationsEnabled=1`）
- WFC 开关未生效（订阅属性为 1，ImsManager 认为 enabled）
- mihomo/TUN/规则本身（M0-A 已通过，规则幂等脚本可重放）
- netlink 间歇 Permission denied（ksu 域限流，操作间加 sleep 1 即可）

## 决定性实验：结论 = 基带路径（TUN 方案在本机不成立）

### 实验一（隧道在线时黑洞）
1. 15:07:43 开机后 IMS PDN 自动建立（netId 101，`rmnet_data1` 拿到 `2a00:23ee:f950:36d5:.../64`，英国段）。
2. 15:08:1x 加 `ip rule add to 198.18.0.41 pref 8900 type blackhole`（ePDG 的 fake-ip，`ping` 立即 `Invalid argument`）。
3. 90 秒后复查：`rmnet_data1` **仍然 UP**，地址不变，隧道没有断。
4. 全程内核里 `grep -cE ':(500|4500) ' /proc/net/udp{,6}` = **0**，`ip xfrm state` 空。

→ 基带生成的 IKE/ESP 完全不经过手机 Linux 的 FIB 和 socket 层，`ip rule` / TUN 拦不到。
→ **ASIM v1 的 mihomo TUN + ip rule 架构在本机不成立。**

### 实验二（顺带确证：基带状态跨 AP 重启存活）
- 14:58 那次 AP 重启后，`DNC` 立刻报 `WLAN: UNKNOWN->IWLAN, UNKNOWN->HOME`，而 `rmnet_data1` 是 DOWN、无任何 PDN 记录 → 基带把自己当成"已在 IWLAN 注册"，因此拒绝重建 PDN。
- 这解释了 14:58~15:06 期间切 WFC 开关 / 重连 WiFi / 重启 `imsdaemon` 全部无效。
- 清除手段只有**整机断电**（`cmd phone restart-modem` 无 carrier 权限；`remoteproc` 被 SELinux 拦；AP 重启不动基带）。
- 断电后 15:07:43 PDN 立即自动重建 —— 与"基带在开机时自己发起 IWLAN"一致。

### 附带结论：代理必须放在手机 IP 栈之外
- 手机上的全局代理 / VpnService / TUN 对这条隧道**永远无效**（包出自基带）。
- 内层 IMS 流量也走独立 APN（`ims`/`rmnet_data1`），代理同样碰不到。
- 有效拦截点只能在手机之外：LAN 侧设备（PC/NAS/软路由）或放弃系统 WFC。

### 对 hosts 方案的影响
- 基带用的是 AP 下发的 DNS 服务器（实测拿到 PC 上 Clash 的 fake-ip），**不读 `/system/etc/hosts`**。
- 因此想让基带把 ePDG 解析到自选地址，只能改 AP 下发给基带的 DNS（`DnsAddresses`），
  或让上游 DNS 服务器返回自选结果 —— 手机本机 hosts 无效。

## 附带发现（对实现有用）
- 黑洞整段 `198.18.0.0/16` 会连带打断 Android network validation（校验域名同样走 fake-ip），导致 IMS PDN 根本不触发。
- 触发 IMS PDN 的有效动作：WiFi 加入新网络（validation 通过后）或设置页里切 WFC 开关；单纯 `svc wifi disable/enable` 不触发。
- ksu 域无法写 `/data/adb`（SELinux），也无法 `setprop persist.vendor.*`、`iptables`。模块启动脚本只能通过 KSU 模块安装流程落地。
