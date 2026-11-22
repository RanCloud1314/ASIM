# VoWiFi 周期性掉线 — 排错交接

> 交接时间：2026-11-22 23:10（第二次交接；首版 21:10）。这是一个**专门排错**的上下文，
> 代码改动已落地并推送，剩下的是「403 为什么周期出现、怎么恢复」。读这一份就够，不用翻聊天记录。
>
> **本次新增的关键结论**：出口已经用同一节点做过 A/B 对照排除掉了；403 精确落在
> **注册后 30 分 01 秒**，是第 30 分钟那次定时重注册本身被拒；失败那一秒抓包里
> **没有丢包/重传/重协商**。现在唯一没查过的链路是「路由器 → 节点」。

## 2026-11-22 23:3x 更新：数据面已换成 mihomo

A/B 的结论是「同出口、mihomo 能过、asim-relay 过不去」，所以不再继续修自研转发：

- 路由器装了 mihomo v1.19.34，TUN `asim0` 只接管 `31.94.76.0/24`；
- dnsmasq 的 `address=/epdg.../192.168.100.1` 已删除，手机拿到真实 IP；
- `asim-relay` 只留 API，节点保存时生成 mihomo 配置并热重载；
- 23:37 手机重启后 CTExcel `PS=HOME`、IMS PDN `CONNECTED`，
  mihomo `/connections` 显示 `192.168.100.103 -> 31.94.76.10:500` 走 `ASIM-UK`。

**判卷未完成**：23:41 起每 30 秒采样 `voice/iwlan`，23:45 因用户正在使用、主动叫停
（当时 `voice=IN_SERVICE iwlan=HOME`，稳定约 8 分钟）。第 30 分钟那次重注册**没有观察到结论**。
有结论前不要宣布修好；如果仍然 403，H4（上游会话形态）排除，问题转运营商侧；
**不要**切回 relay 试运气。下次观察直接从手机侧 `voice/iwlan` + mihomo `/connections` 看，
不需要再挂抓包。

部署/回滚步骤见 `router/mihomo/README.md`。

## 一句话现状

中继（v0.3.1）和手机模块（asimd pid 修复）都已修好并经现场验证；**剩下的唯一问题是
CTExcel/EE 在注册满 30 分钟时的定时重注册上回 `403 Registration_Error14`，掉线后手机
自己不重试，一直躺到有人手动重启**。已确认与出口节点无关（同节点 Clash 能过、中继过不去），
剩下最可疑的是中继的上游会话形态（IKE/ESP 各占一条 TCP、空闲会被回收重开）。

## 现象（用户原话）

「手机用一段时间后无服务/无法拨号。」典型节奏：注册成功 → **20–40 分钟** → 突然
`OUT_OF_SERVICE`，不能打也不能接 → 重启手机立刻恢复。已抓到的三次里，两次是**精确的
30 分 01 秒**（见下文），一次是 22 分钟（`20:28:26`，可能被前序重试污染）。

## 环境

| 项 | 值 |
|---|---|
| 手机 | 小米 12S Pro，`adb` 序列号 `994e779c`，KernelSU root |
| 卡槽 | slot 0 = Lebara（联通，**不许动**）、slot 1 = CTExcel `subId=1` `orig=54151` `mask=32768`（GSM-only） |
| 手机 IP | `192.168.100.103`（wlan0） |
| 路由器 | OpenWrt 25.12.6 x86_64，LAN `192.168.100.1`，API `http://192.168.100.1:8080`，token `3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b` |
| 中继二进制 | `/usr/bin/asim-relay`，sha256 `22a7c2ee…beba4bff`（= Release v0.3.1 同一份） |
| 节点 | **现在** `pokemon888-go.pikaholo.com:56007`（= Clash 里的「英国02」，22:12 由 API 热切并落盘）；旧值 `:56006` → `36.248.14.233`。trojan，SNI `iosapps.itunes.apple.com`，insecure，密码与旧节点相同 |
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
从 Release 重装会复现。手机本地已修；`v0.4.0` 的 zip 已包含修复。

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

### 出口已排除：同节点 A/B 对照（21:14 Clash / 22:15 中继）

2026-11-22 晚做了唯一一次变量干净的对照：把路由器中继的上游从 `56006` 换成
**`56007`（Clash 里同一个「英国02」节点）**，`password`/`sni`/`insecure` 全部相同，
于是「服务器、凭据、出口」三项两边完全一致，差别只剩**传输实现**（mihomo vs asim-relay）。

| | A 组：PC 热点 + Clash | B 组：路由器中继 |
|---|---|---|
| 节点 | pokemon888:56007 | 同一个 |
| 注册 | `21:14:39` | `22:15:55.6` |
| 30 分钟处的重注册 | `21:44:45`（30 分 06 秒）**成功**，会话活到 47+ 分钟 | `22:45:55`（30 分 00 秒）**回 403** |
| 结论 | 同一出口能过 | 同一出口过不去 |

配套事实：

- 中继本轮：注册 `22:15:55.6` → 403 `22:45:56.6`，**正好 30 分 01 秒**；
  更早那次 `19:20:47` → `19:50:48` 也是 **30 分 01 秒**。这不是「封 30 分钟」，
  而是**第 30 分钟那次定时重注册本身就被拒**。
- Clash 路径当时的公网出口是 `38.175.216.135`（Cogent，ipinfo 标洛杉矶），
  47 分钟没变 → 「出口 IP 中途轮换」这条假设两边一起被否掉。
- 每次上线 IMS PDN 地址都不同（`2a00:23ee:f100:3820…` vs `…f400:3891:…`），
  不存在「旧绑定残留」。
- 中继 `drops: 0`，`/ping` 通（1278 ms），flow 咬住 `31.94.76.5`。

### 403 那一秒的逐包证据（22:45:53–22:45:57）

```
22:45:53  UP  MOBIKE INFORMATIONAL mid=4  →  DN 正常应答        隧道健康
22:45:55  UP  ESP 1176 / 1176 / 280                            ← 手机发出重注册（SIP over TCP，约 2.3 KB，按包长推断）
22:45:56  DN  ESP 152 / 152 / 152 / 552                        ← 552 那包就是 403
22:45:56.6  Android 记录 403 Registration_Error14
22:45:56.68 承载被拆（CONNECTIVITY_SERVICE_UNWANTED，后果）
```

**没有重传、没有丢包、没有 IKE 重协商**（两次 MOBIKE INFORMATIONAL 都在毫秒级被应答）。
所以这一秒的失败不是传输问题，是 EE 收到完整重注册后主动拒绝。

### 剩下的唯一差异：上游会话形态

代码层面两边差在这里（`main.go` / `node.go`）：

| | mihomo | asim-relay |
|---|---|---|
| 上游会话 | 连接池，全程复用 | **一个 UDP flow 一条 TCP 会话**（`runFlow` 里 `dialSession`） |
| 一个 IKE 会话占几条 | 1 | **2 条**：`client:500` 和 `client:4500` 各开一条（`/status` 里能看到两个 flow） |
| 回收 | 基本不主动拆 | `idleTimeout=180s` 无上行即拆；读错立刻拆；`stalled` 25 秒只发不收也拆 |
| 拆完 | 同一张 NAT 表里续 | `dropFlow` → 下一个包**重开新会话**，外层 IP:端口变 |

注意：**403 那一刻我们并没有拆会话**（手机源端口全程 `44474`，上游收发正常）。
所以不能再把「拆会话」直接当成这一刻的成因；能确定的只是「同出口、不同会话形态，
EE 只拒了后者」，而差异全在上面这张表里。

### 现在还开着的假设

- **H1 重启是必需的**：重启会重开 vendor IMS 栈、复位调制解调器，飞行模式做不到。
- **H2 只是撞上了窗口**：403 之后大约 **25–30 分钟**封禁才解除。被动削弱——如果只是封禁窗口，
  同节点的 Clash 那轮不该在 30 分 06 秒刷新成功后一路活到 47 分钟。
- **H3 运营商侧风控**：反复重试会延长封禁（论坛经验）。`20:52:52` 那次飞行模式实测**没有**帮助，
  反而又撞了一次 403。
- **H4（新的主假设）上游会话形态被 EE 判异常**：IKE 走一条 TCP、ESP 走另一条，ePDG 看到
  **两个不同的外层端点**；加上会话会被 180s 空闲回收后重开，跟真 NAT 的姿态不同。
  这能解释「同一出口、Clash 行、中继不行」，但解释不了「为什么精确是 30 分 01 秒」，
  除非 EE 的重注册判定还额外比对了外层地址的一致性。

**H1 和 H2 还没分清**，这是恢复策略的核心。注意：我之前说「窗口只有 5 分钟」是读错数据，
正确的是「能确定的最早解除时间是掉线后 24 分钟（撞了还不行），30 分钟时成功」。

H4 与恢复策略正交：查 H4 看**路由器 WAN 抓包**，查 H1/H2 看**掉线后什么都不做要等多久**。

## 已知外部佐证（不是我们独有）

奶昔论坛《CTExcel 开闸了，Android 一样激活》帖子里有人贴了**一字不差**的
`{1000 : CODE_REGISTRATION_ERROR, 403, Registration_Error14-Unable to Connect}`，
同样「抓包能看到完整 ipsec 流程」却不行，结论是放着十几分钟自己会好。

- https://forum.naixi.net/thread-14260-1-1.html
- https://hk.naixi.net/thread-14260-1-1.html （镜像）

## 下一步（按顺序做，别跳）

> 数据面已换成 mihomo。下面第 1 条只在要单独回答 H4 时才需要；正常判卷看手机侧和
> mihomo `/connections` 就够。第 2 条的代码改动已不再适用（旧转发路径不再收流量）。

1. **先补上唯一没抓过的链路：路由器 WAN 口 → 节点。** 下一轮测试时同时挂：

   ```sh
   # 路由器：busybox tcpdump；过滤器写进文件再用 -F，别在 PowerShell 里拼引号/括号
   echo "tcp port 56007" > /tmp/nodef.txt
   tcpdump -U -i eth1 -n -s 0 -w /tmp/node-%Y%m%d-%H%M.pcap -G 600 -W 60 -F /tmp/nodef.txt
   ```

   它一次性回答三件事：那条 TCP 会话在 30 分钟前**有没有被掐/重连**（本地端口变没变）、
   重连发生在第几分钟、重连前后有没有重传卡顿、与 403 差多少秒。

   - **30 分钟前重连过** → H4 成立：把 500/4500 合到一条会话、加主动心跳、在预期被掐前挑空档重连。
   - **会话全程没动** → 403 与传输无关，回到内层 IPsec 的 SA 重建那条线。

2. **代码上无论结论如何都该修的两处**（都还没动）：
   - IKE(500) 与 ESP(4500) 现在各占一条上游 TCP，ePDG 看到两个外层端点；trojan 帧自带目的
     地址，一条会话完全可以同时承载两者 → **合并成一个 flow**。
   - `idleTimeout=180s` 会在「只收不发」的阶段拆会话；至少 4500 这条要放宽或加主动心跳。

3. **恢复策略先别自动化。** 下次掉线后什么都不要做，纯等 30 分钟：
   - 自己恢复 → H2 成立，问题降级为「等就行」，只需要一个 30 分钟粒度的看护。
   - 一直死着 → H1 成立，必须有「叫醒」动作，且要试出**最轻的那个**。
   - 只有在 3 有结论之后，才考虑在 `asimd.sh` 里加自动恢复（保守版：掉线满 30 分钟且期间
     一直没恢复 → 重启手机；激进版：定期飞行模式，弱于重启且可能白撞 403）。

4. 每次掉线记一张表：`403 时刻 / 掉线时刻 / 第一次成功重试的时刻 / 用了什么动作`，
   并且**同时**记中继的上游会话端口（`netstat -tn | grep 56007`；busybox 可能不出结果，
   见坑 11）。至少要 3 个干净样本才能说清封禁时长。

**不要**按 5 分钟间隔去推，也不要连续试 —— 实测会一直撞 403。

## 已采用：路由器上直接用 mihomo 内核（2026-11-22）

A/B 已经把「出口」排除、把差异收窄到**传输实现**。所以没有继续改 asim-relay，而是
在路由器上跑 mihomo（TUN），把 ePDG 的 UDP 500/4500 交给它，asim-relay 只保留业务 API。
实施细节见 `router/mihomo/README.md`，设计与计划见
`docs/superpowers/specs/2026-11-22-mihomo-data-path-design.md`。

- 优点：mihomo 是唯一被证明能撑过 30 分钟刷新的实现，直接拿它当参照/兜底，不用先猜我们哪行写错。
- 代价：路由器要有足够内存跑 mihomo + 规则；`domain`/`epdg_ip` 那套逻辑得换个方式接。
- 注意：这条路线**不会**告诉我们 asim-relay 错在哪，只在「能用」这个目标上更快。

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
python C:\Users\22938\AppData\Local\Temp\opencode\rt\rtsend.py <远端路径> <本地文件>

# 手机侧抓 IKE/ESP（-G 600 每 10 分钟切一个文件，403 落在哪段要按时间找）
adb shell 'su -c "rm -f /data/local/tmp/ike-*.pcap; setsid tcpdump -U -i wlan0 -n -s 0 -w /data/local/tmp/ike-%Y%m%d-%H%M%S.pcap -G 600 -W 60 \"udp and (port 500 or port 4500)\" </dev/null >/data/local/tmp/tcpdump.out 2>&1 &"'

# 定时抓包（没有 timeout 命令，用 -G N -W 1 自己退出）
python ...\rssh.py "rm -f /tmp/x.pcap; tcpdump -i br-lan -n -s 0 -w /tmp/x.pcap -G 1800 -W 1 'host 192.168.100.103 and (udp port 500 or udp port 4500)' 2>&1 | tail -3"

# 抓路由器 -> 节点那一段（唯一没查过的链路，下一轮必抓）
python ...\rssh.py 'echo "tcp port 56007" > /tmp/nodef.txt; rm -f /tmp/node-*.pcap; tcpdump -U -i eth1 -n -s 0 -w /tmp/node-%Y%m%d-%H%M.pcap -G 600 -W 60 -F /tmp/nodef.txt >/dev/null 2>&1 &'
```

中继 API（`/ping` 只接受 POST）：

```powershell
$h=@{'X-ASIM-Token'='3f9c1c7a5b2e4d6f8a0b1c2d3e4f5a6b'}
Invoke-RestMethod -Uri 'http://192.168.100.1:8080/ping' -Method Post -Headers $h
Invoke-RestMethod -Uri 'http://192.168.100.1:8080/status' -Headers $h
```

### pcap 解析

- `C:\Users\22938\AppData\Local\Temp\opencode\parse_ike3.py <pcap> <起> <止> [手机IP]` —— 按秒聚合收发。
  **第 4 个参数必给**：手机走路由器直连时两端都是 `192.168.x`（手机 `192.168.100.103` ↔ 路由器
  `192.168.100.1`），不给就会把双向都算成上行。Clash 那轮对端是 `198.18.x`，可以不给。
- `C:\Users\22938\AppData\Local\Temp\opencode\pkt_tail.py <pcap> <起> <止> [手机IP]` —— 逐包展开 IKE
  类型/mid/flags 与 ESP seq，用来看最后一秒到底发生了什么。第 4 个参数同上。
- Windows 侧 tshark：`D:\Program Files\Wireshark\tshark.exe`（已有，别重装 Wireshark）。

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
9. 手机 `tcpdump -G 600` 每 10 分钟切文件；403 那一段落在哪个文件要按时间找
   （本轮 22:45 的证据在 `ike-20261122-223551.pcap`，`…221539.pcap` 只到 22:25）。
10. 路由器 `tcpdump` 带括号/引号的过滤器经过 PowerShell→ssh 会被拆坏，写进 `/tmp/x.txt`
    再 `-F` 才稳；同一条命令里不要出现 `$`（见坑 1）。
11. 路由器 busybox `netstat -tn | grep 56007` 本轮**无输出**，不能当成「没有会话」的证据；
    要看上游会话就抓 WAN 口。另外不要同时开两个 tcpdump 写同一个文件（会毁 pcap）。
12. 手机侧抓包和 `logcat` 是两个进程，停监控要**分别 kill**；重启手机后它们不会自己回来。

## 代码状态

- Release `v0.4.0` 发 3 件：`asim-v0.4.0.zip`（模块，含 asimd pid 复用修复）、
  `asim-relay-linux-amd64`（32391328）、`asim-relay-windows-amd64.exe`（31581184）。
- 路由器上跑的是 23:36 那版二进制（同源代码，`version` 字段还是 `0.3.1`），**没有跟着重装**：
  重装会触发 mihomo 热重载、拆掉当前 ePDG 会话；等下次维护窗口或出问题时再对齐。
- **线上配置变更（重要）**：`/etc/asim/relay.json` 的 `node.port` 已于 22:12 通过
  `PUT /config` 热切到 **56007**，并已落盘（重启不会回退）。`56006` 只在文档历史里出现。
- 旧的自研转发路径仍留在代码里（`listen_addr` 500/4500），但 dnsmasq 不再指向路由器，
  所以没有流量。原先计划的「500/4500 合并成一条上游会话」「4500 心跳」**不再需要**，
  除非要退回自研转发。

## 取证产物（本轮，2026-11-22 22:15–23:00）

| 路径 | 说明 |
|---|---|
| 手机 `/data/local/tmp/ike-20261122-221539/222551/223551/224554.pcap` | wlan0 上 500/4500 全量，含注册握手 |
| 手机 `/data/local/tmp/asim-all.log` | 持久 logcat（含 22:45:56 的 403 原文） |
| 手机 `/data/local/tmp/asim-all.log.1..8` | logcat 轮转历史 |
| 路由器 `/tmp/ike-relay-20261122-2216/2226/2236.pcap` | br-lan 侧（手机↔中继），缺 22:16 前的握手 |
| Windows `%TEMP%\p1.pcap`、`p2.pcap`、`p3.pcap` | 从手机拉下来的副本（22:15 / 22:35 / 22:45） |
| Clash 那轮（21:14–22:01）产物 | `%TEMP%\vowifi\tun*.pcap`、`ics*.pcap`；手机侧已被本轮同名文件覆盖 |

监控已于 23:0x 全部停止（手机 tcpdump + logcat、路由器 tcpdump、Windows tshark 均无残留进程）。

## 关键文件

| 路径 | 说明 |
|---|---|
| `module/scripts/asimd.sh` | 守护：锁槽位、写 `state.json`、`is_running` 已修 |
| `module/scripts/nocamp.sh` | 防驻网 `lock`/`unlock`/`slot_mask` |
| `module/service.sh` | 开机 `boot_completed` 后 +20s 启动守护 |
| `router/asim-relay/main.go` | 中继主体：`resolveOnce` 粘性 IP、`EpdgIP`、`stalled` 回收、flow 读错 drop |
| `router/asim-relay/node.go` | `dialSession` / `trojanUDP` / `probe`（`/ping` 就是它） |
| `module/scripts/relay.sh` | 手机→路由器 API 包装（`router\|setrouter\|status\|get\|put\|ping`） |
| `%TEMP%\opencode\rt\rssh.py` / `rtget.py` / `rtsend.py` | 路由器 SSH / 取文件 / 送文件 |
| `%TEMP%\opencode\parse_ike3.py` / `pkt_tail.py` | pcap 分析（第 4 参数传手机 IP） |
| `docs/VOWIFI-DEBUG.md` | 本文档 |
| `docs/HANDOFF.md` | 项目总交接（环境、卡槽、恢复原值的应急命令） |
