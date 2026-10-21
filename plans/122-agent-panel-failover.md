# 122 — agent 连面板的线路故障切换（「Leikwanhost 沪台IPLC」(48)）→ fork.65

用户 2026-10-21 批准修 48 的面板连接问题（「你这边修复没有问题」），条件：参考妙妙屋官方 agent 的多连接方式自动选择；
改动必须通用，不能影响现在连接正常的机器。时间统一台湾时间（UTC+8）。
工作树 `/root/projects/flvxx/flvxx-panel-failover`，分支 `feature/3.0.27-agent-panel-failover`（从 fork.64 分支头
`bda7e694` 起）。运行目录 `/root/flvx-workers/runs/agent-panel-failover/`。

## 实测（10-21，48 上只读）

- 48 的 IPv4 出口经 IPLC 到台湾（公网 114.35.6.24），路径 MTU 1000（10.6.12.1 回 Frag needed），中途有 MSS 钳制
  （所有连接 mss 948）；IPv6 是国内电信/联通原生。DNS（223.5.5.5 / 8.8.4.4 / 1.1.1.1 / 119.29.29.29）对
  `flvxxht.imgamer.top` 只返回 A（104.21.33.54、172.67.189.12），AAAA 被剥掉；硬编码 CF IPv6 的 443 全部 SYN 超时。
- 10-19 起 agent 日志 126 个会话：**wss 两个 IP 共 24 个会话，没有一个是 45 秒短命**（中位 1–1.5 小时，按小时被 CF
  正常断开）；**ws:80 101 个会话里 84 个恰好 46 秒**（两个 IP 都有），只有 8 个长会话。
- 坏时段（10-21 15:16–16:04、16:31–16:43、18:03–18:15 等）：wss 拨号 SYN 超时或 TLS 握手卡住
  （`read tcp ...:443: i/o timeout`），DNS 偶尔超时；ws:80 能握手，agent 收到 SetMachineProtection / SetQuotaGroups
  两条命令，但它的回复到不了面板（面板日志 `SetQuotaGroups: 等待节点响应超时`），面板 45 秒收不到 pong 就断。
- 根因（agent 侧）：`preferredWSScheme` 粘住 ws —— 坏时段一次 wss 失败后，每次重连都先 ws，而 ws「握手成功」
  掩盖了会话是坏的，于是再也不试 wss；DNS 返回的两个 IP 只在 TCP 层被 Go 轮流尝试，TLS 卡住时不换 IP。
- 好时段（探测 12:45–12:58）：wss×2、ws×2、直连源站 104.145.236.29:63665 全部正常；源站 IPv6 从 48 走国内 IPv6
  是 EOF（42 上 IPv6 直连正常）。直连源站在坏时段的表现由 48 上的定时探测记录（`/root/flvx-probe/loop.txt`，
  10 小时后自停），用来决定要不要做「备用面板地址」。

## 妙妙屋官方 agent 的做法（`/root/projects/mmw-agent`，只读）

- auto 模式每轮都从最高优先级（WS）重新开始，WS 断开就回到循环开头再试 WS，不粘住降级方式；失败依次降到 HTTP 推送、
  pull，退避 5 s→5 min。
- `recovery_url`（源站直连 http）只在 master 是 https 且 5 分钟联系不上时探测，并且要主控回应「进入 HTTP 恢复模式」
  才切换 —— 是面板控制的灾备开关，不是单机线路切换。
- 会话存活：读空闲 5 分钟、应用心跳 30 秒，没有快速截断检测。

## 设计（只改 agent，面板协议不变）

1. **方案顺序不再粘住**：每次拨号都先主方案（wss，地址显式写 http/ws 时为 ws），再备选方案。只有「确定性协议不符」
   才记住（wss 打到明文 HTTP 端口时 TLS 报 `first record does not look like a TLS handshake`），这样直连
   `IP:63665` 的 30/31/34/35/36 仍是 ws 优先，和今天一样。
2. **每个 IP 都试**：第一次尝试与今天完全相同（主机名交给 Go 拨号器，含 Happy Eyeballs）；只有它在 TCP 之后失败
   （TLS/WS 握手卡住、无回应）或 DNS 失败时，才按 IP 逐个固定拨号（同一 Host/SNI），上次好用的 IP 优先，
   遵守 `panel_ip_family`（含 IPv6 自愈）。DNS 失败时用最近成功会话/解析过的 IP（最多 24 小时）。
   HTTP 状态码失败（502/403 等）说明线路通、是服务端回答 → 不换 IP，直接换方案（与今天的尝试次数相同）。
   每轮最多 60 秒；单次尝试仍是 10 秒握手超时；同一时间只有一轮拨号、最多 1 条面板 WS。
3. **截断检测**：连上后立即发一个 WS ping（hello），20 秒内没收到 pong（面板自己的第一个 ping 在 15 秒）→
   这个候选算失败（冷却），换下一个；
   会话中自己的 ping 每 20 秒一次，45 秒收不到 pong 就结束会话（与面板 45 秒对称）；原有 60 秒读超时保留。
   pong 没能持续到 60 秒的会话也算失败（冷却 + 退避增长）；健康会话（开始 60 秒后仍收到 pong）才清零退避、
   记为「上次好用」。面板主动要求的重连（改 IP 方式）不算失败。代理环境（HTTP(S)_PROXY）下不做按 IP 固定拨号。
4. **冷却 = TTL（5 分钟）**：失败的（方案, IP）5 分钟内排到后面；冷却中有条目的方案不再走「交给 Go」那一次，
   直接按 IP 排序（未冷却的先）。
5. **回到主方案**：只有当前会话不在主方案（例如 ws 降级）时，每 5 分钟（±20% 抖动）发一次 HTTPS `GET /flow/test`
   探测主方案；成功就主动结束降级会话、立即按主方案优先重连。连在主方案上的节点没有任何额外后台循环。
6. **退避**：沿用 2 s 起翻倍到 2 min（±25% 抖动）；短命/失败会话也计入。
7. **HTTP 上报（流量/配置）**：https 失败后改用 http 的偏好只保留 5 分钟（之前是整个进程）；确定性 TLS 不符仍永久
   记住。正常节点不变。
8. **兼容**：只用 ping/pong 与已有的 `/flow/test`，旧面板（fork.55–64）全部支持；面板不需要改代码，旧 agent 不受影响。
9. **可选「备用面板地址」**（妙妙屋 `backup_master_urls` / `recovery_url`）：等 48 坏时段的直连实测数据再定；
   只有直连在坏时段明显更好才做（默认空 = 与今天相同），否则在总结里报数字不做。

## 检查清单

- [x] 读记忆、fork.64 发布记录与 OTA 脚本；分支快进到 fork.64 分支头 `bda7e694`。
- [x] 48 只读实测：DNS、各 IP/方案会话、路径 MTU、日志会话统计、面板日志；部署 10 小时定时探测。
- [x] 读妙妙屋官方 agent 源码（auto 模式、recovery_url、存活检测）。
- [x] 设计写入本文件。
- [x] agent：候选生成/排序（方案、IP、冷却、上次好用、DNS 回退、协议记忆）+ 单元测试。
- [x] agent：拨号循环（每轮上限、逐 IP 固定拨号、错误分类）+ 单元测试。
- [x] agent：hello / pong 截断检测、会话结果记账、退避 + 单元测试。
- [x] agent：降级时回主方案探测（只在降级时启用）+ 单元测试。
- [x] agent：HTTP 上报偏好 TTL + 单元测试。
- [x] netns E2E：(a) wss 正常 → 3 次强制断线仍 wss；(b) wss 封 → ws，恢复后 TTL 内回到 wss；
      (c) 两个 IP 封一个 → 很快从另一个连上；(d) ws 只送第一条消息 → 在新上限内放弃；另测 goroutine 不随重连增长。
- [x] 门禁：go-gost/x 全部测试（新代码 -race）；后端 `go test ./...` 与 fork.64 基线按名称对比（已知 15 个）；
      前端 tsc + build。
- [x] 决定备用面板地址（看 48 坏时段数据）：发布前没碰到坏时段；直连 IPv4 与 CF 走同一条 IPLC 出口、直连 IPv6 从 48
      一直 EOF，看不出能帮 48 → 本版不做（默认空的功能也不加），探测数据写进总结。
- [x] 推送、tag `3.0.27-fork.65`、CI + Images 通过、Release 资产与 fork.64 相同。
- [x] 生产面板：回滚目录 `pre-fork65-<TS>`、prune 保留 2 份、升级、健康/在线数/表计数核对。
- [x] 金丝雀「Bestvm HK 年付」(42)：SSH 备份、OTA、方案/IP 与之前相同、1 条面板连接、CPU/RSS、强制断线 ×3。
- [ ] 48：SSH 备份、OTA（面板够不到就 SSH）、观察 ≥30 分钟：诊断 OK、无「下发失败」、拨号次数/小时。
- [ ] 其余节点分批 OTA（跳过 20），前后诊断；36 不改保护设置；26 不改连接方式。
- [ ] 清理 48 上的探测程序；本计划标记完成；中文总结写入运行目录 `summary.md`。

## 证据

- 代码 `27994f47`：`go-gost/x/socket/panel_failover.go`（候选、拨号轮、探测）、`websocket_reporter.go`（run / connect /
  handleConnection / afterSession）、`panelconn.EffectiveMode`、`service/traffic_reporter.go`（http 偏好 TTL）。
- 单元测试（`-race`）：`panel_failover_test.go` 19 项 + `traffic_reporter_test.go` 2 项新增，全部通过；其中
  「健康 wss 线路一轮只拨 1 次」「面板 502 时仍是以前的 2 次尝试且不冷却」「明文端口仍 ws 优先」「5 次强制断线始终 1 条
  会话、goroutine 不增长、不碰 ws」证明正常节点行为不变。旧测试 `TestReconnectsKeepOneProtectionLoop`（面板每次收到第一条
  指标就断开）在新逻辑下会退避，测试里把健康阈值设为 0 保留原意，并等 run() 退出再清理（消除 -race 报告）。
- netns E2E（`panel_failover_e2e_test.go`，`ip netns exec flvxpf122`，/etc/netns hosts 把 panel.test 指到
  127.0.0.2/127.0.0.3，加速参数：单次 4 s、hello 4 s、TTL 8 s），一次通过（108.6 s，-race）：
  (a) 3 次 `ss -K`：每次 0.5 s 内回到 wss；(b) 443 丢 SYN → 4.5 s 后 ws，恢复后 5.0 s 回到 wss（TTL 8 s）；
  (c1) 127.0.0.2 黑洞 → 2.5 s 连上 127.0.0.3；(c2) 127.0.0.2 TLS 握手大包丢弃 → 4.5 s 固定拨到 127.0.0.3，下一轮
  0.5 s 直接 127.0.0.3；(d) 443 封 + ws@127.0.0.2 只放行前 3 个包（connbytes）→ 4 秒 hello 超时放弃，10.5 s 后
  ws@127.0.0.3 健康，恢复 443 后回 wss；全程 goroutine 12 → 12，同时存活的面板连接最多 1 条。
- go-gost/x 全量 `go test ./...` 通过（TMPDIR 需用短路径：landing 测试的 unix socket 路径超 108 字节时报
  `bind: invalid argument`，与本改动无关，换 `/root/t122` 后通过）；go-gost 主模块通过；socket/service/panelconn `-race` 通过。
- 48 定时探测（12:57–13:20 UTC 三轮）：IPv4 五条路径全部正常；direct6 每次 EOF。还没碰到坏时段。
- 后端（`GOMAXPROCS=2 go test -json -count=1 -p 1 ./...`，本分支后端代码与 fork.64 相同）：失败 15 = fork.64 发布时的
  15 个，名称逐个一致，新增 0、消失 0；`go vet` 输出与 fork.64 逐字相同。
- 前端（未改动）：`tsc --noEmit` 通过；`npm run build` 通过，最大文件 `index-*.js` 2,886,593 B（< 5 MiB）。
- Release：发布分支 `maintenance/3.0.27-fork.65-agent-panel-failover` = 本分支 `e7ad9542`；tag `3.0.27-fork.65`
  （注解对象 27aaffef → e7ad9542）。CI Build Check 39608474739 / 39608979770 success。推 tag 没有触发
  「Build and Push Images」（等了 4 分钟无运行记录），改用 `gh workflow run docker-build.yml --ref 3.0.27-fork.65`
  （工作流按 `github.ref_type == tag` 判断，和推 tag 触发等价）→ 39609685207 success（含 Create Release）。
  `validate-release.py`：Latest = fork.65、非草稿/预发布、10 个资产与 fork.64 相同、两份 compose 固定 fork.65 镜像、
  两个安装脚本 PINNED_VERSION/REPO 正确、gost amd64/arm64 sha256 一致、ghcr 前后端镜像 amd64+arm64 都在；
  解压 gost-amd64 确认含 `flvx-hello` / `FLVX-Panel-Probe/1.0`，`-V` = 3.0.27-fork.65。
- 生产面板（21:49–21:50）：回滚点 `/opt/flvx-svc/rollback/pre-fork65-20261021T134948Z`（在线备份 quick_check /
  integrity_check ok、compose/.env、`local/flvxx-*:pre-fork65-…` 镜像、rollback.sh），prune 后保留 pre-fork64 + pre-fork65
  （删 pre-fork63 与 fork.62 镜像）。21:50:33 升级，健康 + `/version.json` = 3.0.27-fork.65-mvi5tfdf；29/29 在线、
  `panelIpFamily` 无变化、12 张表计数与备份一致；面板 RSS 37.6 → 38.7 MiB，日志 0 错误。
- 升级前基线（12 台可 SSH 节点，24 h 内最后一次会话）：42/52/53 wss(IPv4)、21/45/47 wss(IPv6)、48 ws:80、
  30/31/34/35/36 直连 `104.145.236.29:63665` 的 ws（`ota/schemes-before.txt`）。
- 金丝雀 42（21:51–21:58）：SSH 备份 `/root/flvxx-backup-20261021T135106Z-pre-fork65/restore-agent.sh`；面板 OTA 请求 1.9 s、
  重连 6 s；wss→wss（同一 104.21.33.54:443）、面板 socket 2→2、RSS 57.8→56.3 MB、诊断 87/17 项前后相同。
  只重置 agent 自己的面板 socket（按本地端口 `ss -K`）3 次：每次 0.7–0.9 s 内重连 wss、同一 IP、1 次尝试、1 个会话、
  1 个面板 socket；CPU 0.317 % → 0.317 %（fork.64 时 0.30–0.33 %），RSS 57.3→58.9 MB，线程 8、fd 20–21，日志无切换/告警。
- 48（22:01）：SSH 备份同上路径；面板 OTA 请求 2.3 s、重连 4 s。升级前那个 fork.64 进程在 21:50 面板重启时 ws 拿到 502、
  回退到 wss 成功（偶然回到 wss）；fork.65 首次就是 wss（104.21.33.54:443），流量上报改走 https（172.67.189.12:443，
  之前粘在 http:80）。CPU 0.217 %、RSS 56.5 MB、线程 8、fd 12。诊断 5/6 前后相同（唯一失败 = 规则 110 目标
  pqstw.imgamer.top:10015 从 48 本机不通，fork.64 时已记录，线路问题）。
