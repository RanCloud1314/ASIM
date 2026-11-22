# VoWiFi 周期性掉线 — 排错交接

> 交接时间：2026-11-22 21:10。这是一个**专门排错**的上下文，代码改动已经落地并推送，
> 剩下的是「403 为什么周期出现、怎么恢复」。读这一份就够，不用翻聊天记录。

## 一句话现状

中继（v0.3.1）和手机模块（asimd pid 修复）都已修好并经现场验证；**剩下的唯一问题是
CTExcel/EE 侧对 IMS 重注册回 `403 Registration_Error14`，掉线后手机自己不重试，
一直躺到有人手动重启**。怎么恢复、要不要自动恢复，还没定。

## 现象（用户原话）

「手机用一段时间后无服务/无法拨号。」典型节奏：注册成功 → **20–40 分钟** → 突然
`OUT_OF_SERVICE`，不能打也不能接 → 重启手机立刻恢复。

## 环境

| 项 | 值 |
|---|---|
| 手机 | 小米 12S Pro，`adb` 序列号 `994e779c`，KernelSU root |
| 卡槽 | slot 0 = Lebara（联通，**不许动**）、slot 1 = CTExcel `subId=1` `orig=54151` `mask=32768`（GSM-only） |
| 手机 IP | `192.168.100.103`（wlan0） |
| 路由器 | OpenWrt 25.12.6 x86_64，LAN `192.168.100.1`，API `http://192.168.100.1:8080`，token `3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b` |
| 中继二进制 | `/usr/bin/asim-relay`，sha256 `22a7c2ee…beba4bff`（= Release v0.3.1 同一份） |
| 节点 | `pokemon888-go.pikaholo.com:56006` → `36.248.14.233`，trojan，SNI `iosapps.itunes.apple.com`，insecure |
| ePDG | `epdg.epc.mnc033.mcc234.pub.3gppnetwork.org`，IP 集 `31.94.76.2/.5/.6/.9/.10`（DoH 顺序会轮转） |

## 链路（已验证）

```
手机 IMS ──ESP/IPsec──> 192.168.100.1:500/4500 ──> asim-relay ──trojan/UDP──> 节点 ──> ePDG/P-CSCF
```

模块只干两件事：把 slot 1 锁成 GSM-only 不让它驻网、把 ePDG 的 UDP 500/4500 引到路由器
中继。其他流量完全不受影响。中继只认 ePDG 这一个域名。

## 已经修好并验证的两件事（别再怀疑这两处）

### 1. 中继 v0.3.1（commits `ab69349`、`4937d38`、`d0bea1c`，Release tag `v0.3.1`）

- `resolveOnce` 每 10 分钟重解析 ePDG，DoH 答案只要**换序**就 `killFlows()`，把正常会话掐断。
  现在只在「正在用的 IP 从答案集里消失」时才切。
- flow 的读协程出错只 `Close` 不 drop；节点会把 **空闲 60 秒** 的 UDP 会话 `EOF` 掉，
  期间写成功、回包永不来 → 手机重传全进黑洞。现在读错立刻 drop，且「只发不收超 25s」主动回收。
- 新增 `epdg_ip` 可钉死 IP。

现场验证：`19:22:13` DoH 换序（`.9` 挪到第三位）**只打日志没切流**；`20:26:09` 再次换序，
会话照旧（一直咬 `31.94.76.5`）。`19:21:41` 上游 `read: EOF` 后 flow 立刻 drop 重建。

### 2. 手机模块 asimd pid 复用（commit `0cd14a5`，docs `3293861`）

重启后 `asimd.pid` 里的 pid 被别的进程复用，`is_running` 只做 `kill -0` 就判「already
running」直接退出 → **守护不启动、槽位不锁、WiFi Calling 起不来**；`stop` 还会去 kill
那个无关进程。现在同时校验 `/proc/<pid>/cmdline` 含 `asimd`，selftest 加两个用例（17/17 通过）。

现场验证：`20:58` 重启手机 → `20:58:18 asimd: started (pid 5201)`，**无人工干预**。
（顺带说明：这个 bug 就是之前「重启完 WiFi Calling 不来」的原因，不是运营商。）

**注意：模块版本号还是 v0.3.0，没重打 zip。** 已发布的 `asim-v0.3.0.zip` 仍带这个 bug，
从 Release 重装会复现。手机本地已修。

## 没解决的问题：周期性 `403 Registration_Error14`

### 完整时间线（全部有日志/抓包支撑）

| 时间 | 事件 |
|---|---|
| `18:33:30`、`18:43:46` | 两次掉线，PDN 被拆（`PREFERRED_DATA_SWITCHED` + `CONNECTIVITY_SERVICE_UNWANTED`），WiFi 全程未断 |
| `19:20:47` | 注册成功 |
| `19:50:48` | 掉线，正好 30 分 01 秒；同款 403 |
| `20:06:04` | **路由器自己重启**（疑似我一条被打散的 `kill` 误伤，见坑 2） |
| `20:06:25` | 手机重建 IKE，注册成功 |
| `20:28:26` | 403，掉线（距注册 ~22 分钟） |
| `20:29:26` | 中继上游 `read: EOF`（节点空闲 60s 回收，**是结果不是原因**） |
| `20:29:46`–`20:51:20` | **手机彻底静默，`flows: []`，22 分钟一个 IKE 包都没发** |
| `20:52:52` | 我手动飞行模式 8 秒 |
| `20:53:06` | 新建 IWLAN 承载（netId 107 `MOBILE[IWLAN] extra: ims`），**两秒后又是 403** |
| `20:58:20` | 重启手机 → IKE 起来 → 注册成功 |
| `21:00:45` | `voice=IN_SERVICE iwlan=HOME`，flow 存活 |

### 决定性日志行

```
20:28:26.742 D/TNA-102 : NetworkAgent channel lost
20:28:26.758 D/ImsSmsDispatcher: onImsDisconnected imsReasonInfo=
             {1000 : CODE_REGISTRATION_ERROR, 403, Registration_Error14-Unable to Connect}
20:28:26.783 D/DN-102-I: onTearDown: reason=CONNECTIVITY_SERVICE_UNWANTED
```

顺序读法：**运营商先回 403** → IMS 主动放掉 IWLAN 的 NetworkRequest → ConnectivityService
拆承载（`UNWANTED`）→ 代理上报 channel lost。所以「channel lost」和「UNWANTED」都是**后果**，
不是原因。别被第一行骗了。

### 抓包结论（19:20–19:50 那段，`/tmp/ike.pcap`）

- `19:42`–`19:50` 每 20 秒一个 keepalive，`up=1 down=1`，收发各一。
- 最后一对是 MOBIKE INFORMATIONAL（`mid=5`，`flags=0x08` 请求 / `0x20` 响应），**双向都正常**。
- 也就是说：掉线那一刻隧道是活的。`down=0` 的统计曾经误导过我——原因是
  `192.168.100.103` 里包含 `192.168.100.1` 前缀，方向判断写错了，已修（`parse_ike3.py`）。

### 已经排除的原因

| 嫌疑 | 证据 |
|---|---|
| 节点不稳定 | 路由器 ping：20/20，**0% 丢包**，32.365/32.849/33.584ms，抖动 1.2ms；`/ping`（POST）6/6 成功 893–967ms；中继到节点**一条 TCP 会话撑了 23 分 01 秒**（`20:06:25`→`20:29:26`）零错误；`drops: 0` |
| 中继丢包/切流 | 两次 DoH 换序都没切流；掉线前后中继日志一片干净 |
| WiFi 掉线 | 全程 `iwlan=HOME`，无 `CTRL-EVENT-DISCONNECTED` |
| 本地宽带 | 20:25 手机确实报了 `onDataStallSuspected` + GMS 取 token `Waited 30 seconds`，但 GMS 流量**不走节点**（节点只接管 ePDG 那个 UDP 域），所以这是家里的线在卡，与 403 无直接关系 |

逻辑要点：**丢包只能让包消失，不能让对方回一个 403。** 能收到 403 就说明路是通的。

### 现在还开着的假设

- **H1 重启是必需的**：重启会重开 vendor IMS 栈、复位调制解调器，飞行模式做不到。
- **H2 只是撞上了窗口**：403 之后大约 **25–30 分钟**封禁才解除，`20:58` 的重启刚好落在里面。
- **H3 运营商侧风控**：反复重试会延长封禁（论坛经验）。`20:52:52` 那次飞行模式实测**没有**帮助，
  反而又撞了一次 403。

**H1 和 H2 还没分清**，这是下一步的核心。注意：我之前说「窗口只有 5 分钟」是读错数据，
正确的是「能确定的最早解除时间是掉线后 24 分钟（撞了还不行），30 分钟时成功」。

## 已知外部佐证（不是我们独有）

奶昔论坛《CTExcel 开闸了，Android 一样激活》帖子里有人贴了**一字不差**的
`{1000 : CODE_REGISTRATION_ERROR, 403, Registration_Error14-Unable to Connect}`，
同样「抓包能看到完整 ipsec 流程」却不行，结论是放着十几分钟自己会好。

- https://forum.naixi.net/thread-14260-1-1.html
- https://hk.naixi.net/thread-14260-1-1.html （镜像）

## 下一步（按顺序做，别跳）

1. **下次掉线后，什么都不要做，纯等 30 分钟。**
   - 自己恢复 → H2 成立，问题降级为「等就行」，只需要一个 30 分钟粒度的看护。
   - 一直死着 → H1 成立，必须有「叫醒」动作，且要试出**最轻的那个**。
2. 掉线前就把取证武装好（见下面工具箱），尤其是**路由器侧 WAN 抓包**（这次没抓，
   所以路由器↔节点那段只有间接证据）。
3. 每次掉线记一张表：`403 时刻 / 掉线时刻 / 第一次成功重试的时刻 / 用了什么动作`。
   至少要 3 个干净样本才能说清封禁时长。
4. 只有在 1 有结论之后，才决定要不要在 `asimd.sh` 里加自动恢复。候选：
   - 保守版：掉线满 30 分钟且期间一直没恢复 → 重启手机（唯一验证过有效的动作）。
   - 激进版：每 30 分钟一次飞行模式（弱于重启，可能白撞 403，有延长封禁的风险）。

**不要**按 5 分钟间隔去推，也不要连续试 —— 实测会一直撞 403。

## 调试工具箱

### 手机侧

```powershell
adb reboot                                              # 重启（目前唯一验证有效）
adb shell 'su -c "cat /data/adb/asim/state.json"'       # 槽位 + 中继状态（模块每 60s 写）
adb shell 'su -c "tail -8 /data/adb/asim/asimd.log"'    # 守护日志
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh start"'
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh selftest"'   # 17 项自检
adb shell 'su -c "sh /data/adb/modules/asim/scripts/asimd.sh status"'

# 持久化 logcat（必须 detach，否则 adb shell 会挂住；重启也不会丢前面的）
adb shell 'su -c "rm -f /data/local/tmp/asim-all.log*; setsid logcat -b all -v time -f /data/local/tmp/asim-all.log -r 4096 -n 8 </dev/null >/dev/null 2>&1 &"'
adb pull /data/local/tmp/asim-all.log

# 叫醒动作（从弱到强）
adb shell 'su -c "svc wifi disable; sleep 6; svc wifi enable"'                       # 实测无效
adb shell 'su -c "cmd connectivity airplane-mode enable; sleep 8; cmd connectivity airplane-mode disable"'  # 实测无效，反而撞 403
```

查守护到底在不在跑（**别用 `ps -A | grep asimd`**，toybox 只显示 argv[0] = `sh`）：

```powershell
adb shell 'su -c "for p in \$(ls /proc | grep -E \"^[0-9]+\$\"); do c=\$(tr \"\\0\" \" \" < /proc/\$p/cmdline 2>/dev/null); case \"\$c\" in *asimd*) echo \"\$p \$c\";; esac; done"'
```

### 路由器侧

```powershell
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "date; logread | grep -i asim-relay | tail -20"
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rssh.py "sha256sum /usr/bin/asim-relay; uptime"
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtget.py /tmp/x.pcap $env:TEMP\x.pcap
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtsend.py <本地文件> <远端路径>

# 定时抓包（没有 timeout 命令，用 -G N -W 1 自己退出）
python ...\rssh.py "rm -f /tmp/x.pcap; tcpdump -i br-lan -n -s 0 -w /tmp/x.pcap -G 1800 -W 1 'host 192.168.100.103 and (udp port 500 or udp port 4500)' 2>&1 | tail -3"
```

中继 API（`/ping` 只接受 POST）：

```powershell
$h=@{'X-ASIM-Token'='3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b'}
Invoke-RestMethod -Uri 'http://192.168.100.1:8080/ping' -Method Post -Headers $h
Invoke-RestMethod -Uri 'http://192.168.100.1:8080/status' -Headers $h
```

### pcap 解析

- `C:\Users\22938\AppData\Local\Temp\opencode\parse_ike3.py <pcap> <起> <止>` —— 按秒聚合收发
  （方向判断已修好：`src == "192.168.100.103"` 才算上行）。
- `C:\Users\22938\AppData\Local\Temp\opencode\pkt_tail.py <pcap> <起> <止>` —— 逐包展开 IKE
  类型/mid/flags 与 ESP seq，用来看最后一秒到底发生了什么。

### 观察脚本

`C:\Users\22938\AppData\Local\Temp\opencode\rt\watch2.ps1` —— 每 30s 采样 voice/iwlan/flows
写 `rt\vowifi_watch.log`，默认 45 分钟。注意 `last_seen_s` 每 60s 才刷新一次，30s 采样会看到
重复值，那是采样假象不是故障。

## 别重复踩的坑

1. **路由器上 `$()` 和 `$!` 在 PowerShell 里会被吃掉**，`rssh.py` 传过去就成了半截命令。
   `20:06` 那次路由器自己重启，高度怀疑就是这么误伤了进程。要么写脚本文件传上去，要么
   整条命令不含 `$`。
2. 路由器没有 `curl` / `base64` / `od` / `pkill` / `timeout` / sftp。用 `wget -q -O -`，抓包用
   `-G`/`-W` 自动退出。
3. `rtget.py` 只用于路由器；手机文件用 `adb pull`（`rtget.py` 对手机路径会返回 0 字节）。
4. `ps -A | grep asimd` 永远匹配不到（见上）。
5. 手机 adb 会不定时掉，`adb kill-server; Start-Sleep 2; adb devices` 能捞回来；再不行就是线被拔了。
6. `handleImsUnregistered` 里的 `disconnectedRadioTech=-1`、`getImsRegistrationTechnology=-1`
   都只是「没注册了」的同义反复，别当线索。
7. 飞行模式/连续重试会撞 403 并延长封禁，**别用来试运气**。
8. 生产节点、token、SIM 参数都在上面，不要在输出里再打印一遍完整凭据。

## 代码状态

- 仓库 `master` 已与 `origin/master` 同步（`3293861`）。
- Release `v0.3.1`：只发 2 个二进制（`asim-relay-linux-amd64` 32444576、
  `simas-relay-windows-amd64.exe` 31631360），**没重打模块 zip**。
- 路由器磁盘二进制 = Release 同一份 = 现在运行的进程（`20:06` 重启后已对齐）。
- 待办：模块升版本 + 重打 `asim-v0.3.x.zip`（修了 pid 复用的那份）。

## 关键文件

| 路径 | 说明 |
|---|---|
| `module/scripts/asimd.sh` | 守护：锁槽位、写 `state.json`、`is_running` 已修 |
| `module/scripts/nocamp.sh` | 防驻网 `lock`/`unlock`/`slot_mask` |
| `module/service.sh` | 开机 `boot_completed` 后 +20s 启动守护 |
| `router/asim-relay/main.go` | 中继主体：`resolveOnce` 粘性 IP、`EpdgIP`、`stalled` 回收、flow 读错 drop |
| `router/asim-relay/node.go` | `dialSession` / `trojanUDP` / `probe`（`/ping` 就是它） |
| `docs/HANDOFF.md` | 项目总交接（环境、卡槽、恢复原值的应急命令） |
