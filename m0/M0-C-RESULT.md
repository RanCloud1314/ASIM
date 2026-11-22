# M0-C 结果：路由器侧 ePDG 中继 —— 通过

设备：OpenWrt 25.12.6 / x86_64 / LAN 192.168.100.1；手机 192.168.100.103（飞行模式 ON，仅 WiFi）。

## 验证顺序

1. **DNS 指向生效**：`/etc/dnsmasq.conf` 加 `address=/epdg.epc.mnc033.mcc234.pub.3gppnetwork.org/192.168.100.1`，
   `nslookup` 返回 `192.168.100.1`。→ 下面这条抓包证明**基带用的就是这个应答**：

   ```
   15:30:44 192.168.100.103.40950 > 192.168.100.1.500: isakmp: parent_sa ikev2_init[I]
   15:30:56 192.168.100.103.40950 > 192.168.100.1.500: isakmp: parent_sa ikev2_init[I]
   ```
   （源端口随机、12 秒重传；当时路由器无人应答，所以停在这里。）

2. **Go 中继上线**（`router/asim-relay`，trojan over TLS，DoH 自行解析真实 ePDG）：
   ```
   15:37:07 flow 192.168.100.103:44545|500  -> 31.94.76.10:500
   15:37:08 flow 192.168.100.103:44545|4500 -> 31.94.76.10:4500
   ```
3. **隧道建立**：
   - `15:37:10 notifyDataConnectionForSubscriber: phoneId=1 subId=1 state: CONNECTED, transport: WLAN, network type: IWLAN`
   - `ip -br addr show rmnet_data1` → `2a00:23ee:f950:6b9b:2411:b0ff:fe5f:ed07/64`（英国 BT 段，由 ePDG 下发）
4. **IMS 注册**：`SST-1: getCombinedRegState: return STATE_IN_SERVICE for IWLAN as Data is in service and WFC is enabled`
5. **通话验证**：`15:38:07 CallAnalytics: [Normal Call, Success, CODE_USER_TERMINATED, (501), IWLAN, (18), 1]`

## 结论

- 架构成立：**手机基带 → 路由器 Go 中继 → 英国 trojan → EE ePDG → 系统 WFC**。
- 整个方案只影响 `192.168.100.1:500/4500` 这一条流，家里其他设备、手机其他流量完全不经手。
- 基带确实按路由器下发的 DNS 走，**不需要 hosts、不需要 TUN、不需要 nftables**。
- 遗留：节点域名解析在路由器刚开机时偶发超时（IKE 重传自动恢复，最多多等 1 分钟）。
