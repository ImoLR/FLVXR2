# 127 落地默认防滥用规则 + Reality 防盗

基线：`origin/maintenance/3.0.27-fork.68-mieru-hint`（`eb047757`，生产 fork.68）。
开发分支：`feature/3.0.27-landing-abuse-defaults`，worktree `/root/projects/flvxx/flvxx-abuse-defaults`。
发布：本计划只构建 + 截图。用户 2026-10-22 晚批准（防盗默认开、防滥用 23/135-139/445 TCP+UDP 都封），
由 plan 128 发布为 `3.0.27-fork.69`（分支 `maintenance/3.0.27-fork.69-abuse-defaults`）。
运行目录：`/root/flvx-workers/runs/landing-abuse-defaults/`（日志、截图、summary.md）。临时目录 `/root/tmp-work/abuse/`。

## 已定设计（用户 2026-10-22）

1. 每个落地默认封 **TCP 25**（SMTP）和 **BT**（嗅探 `bittorrent`）；管理员可按路由方案关闭，用户看不到开关。
2. 新预设「防滥用」（默认不加）：TCP `465,587,2525` + `23,135-139,445`；说明写清只影响用邮件客户端经代理发信，
   收信（IMAP 993/143、POP3 995/110）和网页邮箱不受影响。不封 22 / 3389。
3. FLVX 出站与路由和妙妙屋自己的 Xray 路由分开（不同 Core），不同步。
4. 行为式扫描检测另开计划。
5. Reality 防盗：所有 FLVX VLESS-Reality 入站（原生落地节点、线路机链式节点）的 `dest` 改指本机辅助入站，
   只放行该节点 `serverNames` 的 SNI 到真实目标，其余封锁；默认开，管理员按服务器开关；链接逐字节不变。

## 技术设计（本任务确定）

### A. 默认防滥用规则（面板编译，原生/共享都不需要升级节点程序）
- 编译进计划：两条规则紧跟安全规则（计划规则的最前面），作用于「开启默认规则」的全部节点：
  `{"name":"默认：屏蔽 SMTP 25","port":"25","network":"tcp","outbound":"block"}`、
  `{"name":"默认：屏蔽 BT","protocol":["bittorrent"],"outbound":"block"}`（Xray 同一规则内各条件是「且」，所以分两条）。
- 开关：`landing_route_profile.abuse_defaults_off`（默认 false = 开；只加列）；内置「默认（直连）」恒为开。
  API 字段 `abuseDefaults`（省略 = 保持原值 / 新方案为开），用户接口不返回。
- 去重：方案开启默认规则时，方案里与默认规则条件完全相同的封锁规则（如「屏蔽 BT」预设）编译时跳过，不重复下发；
  界面上该预设显示「已包含在默认规则中」。
- 原生落地的就绪门槛：只有 Core 已有 RoutingService（`egress_ready`）或管理员已确认「重启一次」时才下发默认规则；
  否则保持今天的负载（不会触发 restart_required → 节点被暂停），界面提示「默认防滥用规则待生效：需重启一次落地 Core」。
- 共享服务器：妙妙屋声明 `egress.versions ∋ 1 && allowed` 时随计划下发（契约 v1 已支持 port/network/protocol，
  Custom/Helper 无需改动）；否则不下发，显示现有「暂不支持自定义出站」。
- 线路机链式节点：旧节点程序把链式规则排在方案规则前，默认规则对链式节点无效；新节点程序支持规则字段
  `builtin: "abuse"`（只能封锁；排在链式规则前；只被 builtin 规则覆盖的节点在规则应用失败时不暂停），面板只对能力
  `landing_guard ≥ 1` 的节点程序把链式节点 tag 加入默认规则并带 `builtin`；妙妙屋 Custom 严格解析 JSON，永远收不到该字段。
- 嗅探：FLVX 原生入站（SS2022、VLESS Reality、链式）和妙妙屋 Helper 渲染的全部入站（含 AnyTLS/Snell/Mieru）都开了
  `sniffing.enabled`，Xray 在开启嗅探时运行全部协议嗅探器并写入 `protocol`，BT 规则可匹配。限制：加密 BT（MSE/PE）和
  DHT 无法识别；实测 Core（8f5b614）的 uTP 嗅探器把时间戳与 Unix 微秒全值比较，真实 uTP（UDP 上的 BT）永远识别不了。
  （plan 129 更新：新版 Core `b01adef`（Custom v1.4.74，Xray #6667）已能识别 uTP，节点程序固定到该版本后
  「默认：屏蔽 BT」也拦 UDP 上的 BT；加密 BT 和 DHT 仍识别不了。）

### B. Reality 防盗（需要新节点程序）
- 每个 Reality 节点（按 dest + serverNames 分组，同组共用）一个辅助入站：`dokodemo-door` 监听 `127.0.0.1:<端口>`，
  目标 = 真实 dest，嗅探 tls + routeOnly；两条路由（排在安全规则之前，只匹配该辅助入站）：SNI ∈ `full:<serverNames>` →
  direct，其余 → block。Reality `dest` 改为 `127.0.0.1:<端口>`。
- 辅助端口：面板分配并入库（`landing_node.guard_port` / `forward_chain_node.guard_port`，同组同端口），在落地端口段
  （链式专用服务器：1024–65535）内从高往低找，避开规则端口段、转发/隧道/部署端口、落地节点和链式端口、同机妙妙屋共享端口；
  所有端口冲突检查（落地建节点、链式端口、转发端口、规则端口段校验、LandingPortInUse）都把它算进去；节点删除即释放（行删除），
  关闭开关时清零；稳态同步不查询不写库。节点程序只删除自己的 `flvxg-` 入站，没有节点再用的辅助入站最后删除。
- 不用 unix socket（无端口）的原因：读 Core 源码确认 dokodemo 在 unix 监听时把目标网络设成 UNIX，freedom 拨号会报 unknown network（REALITY 本身支持 unix dest）。
- 节点程序：辅助入站先于 Reality 入站添加；只有 Core 有 RoutingService 且规则已应用时才启用，否则照旧直连 dest
  （fail-open，状态里提示）；辅助入站添加失败同样回退。能力上报 `landing_guard: 1`。
- 被防盗的 Reality 入站加 `TCP_DEFER_ACCEPT`（5 秒，customSockopt）：REALITY 在 accept 时就连 dest（=防盗入站），
  而防盗只有 Core 固定的 200 ms 嗅探窗口；推迟到 ClientHello 第一批字节到达再 accept，晚到的 ClientHello 也在窗口内。
  实测：不加时 ClientHello 晚 0.5 秒 30/30 失败，加后 0.5 秒 / 1.5 秒都 0/30 失败；剩余情况见核验记录。
- 开关：`landing_server.reality_guard_off`（默认开），管理员在服务器设置里切换；共享服务器 FLVX 不建 Core，
  需妙妙屋实现（契约说明）。

## 清单

- [x] 读记忆/任务书/AGENTS.md，核对 worktree 与 plan 编号 127；查生产（只读）各落地的能力与状态。
- [x] 基线：未修改代码的后端全量测试失败集合（完整仓库副本 `eb047757`，串行）：15 项，`runs/landing-abuse-defaults/logs/baseline-failures.txt`。
- [x] A 后端：方案开关列 + 编译默认规则（就绪门槛、去重、链式 builtin 标记）+ API 字段 + 黄金测试（`407ee188`）。
- [x] B 节点程序：builtin 规则排在链式规则前且不暂停节点、Reality 防盗渲染/对账/回退、能力上报 + 测试（`9cac99ec`）。
- [x] B 后端：服务器开关列、能力列、辅助端口分配与冲突检查、下发、状态 + 测试（`9cac99ec`）。
- [x] 前端：内置默认卡片、方案编辑器锁定行 + 开关、「防滥用」预设 + 说明、服务器设置防盗开关与提示；tsc/lint/build（`e760e207`、`ce048ca2`）。
- [x] 本地 E2E（真实 Core，netns）：25 封 / 443 通 / BT 握手封 / 587 默认通、加预设后封；防盗开/关吞吐与首字节延迟、
      错误 SNI 探测被封、正确 SNI 拿到真实证书；晚到 ClientHello、丢包长 RTT；资源（RSS/CPU、规则数/计划大小）。
- [x] 共享服务器协议嗅探核验（plan 121 实验 Core + mihomo：SS2022/AnyTLS/Snell v4/v5/Mieru TCP/UDP）。
- [x] 后端全量对比基线；节点程序测试（含真实 Core、plan 107 netns）；PostgreSQL 落地测试；前端 tsc/lint/build。
- [x] 截图（电脑 + H5、亮 + 暗，前后对比）+ index.html（34 张，0 个页面错误），并放到预览服务器。
- [x] 推送分支、CI 全绿、summary.md、计划完成。

## 核验记录

- 生产（只读，2026-10-22）：落地服务器 1「Breadcloud JP」原生（节点 21）`egress_ready=0`（Core 仍是 fork.58 前的配置），
  0 个落地节点；服务器 2「Breadcloud US 年付」/3「Boil Hinet 马年」/4「Dmit T1」（embedded）共享，均声明 egress v1、
  allowed；全库 1 个落地节点（服务器 3，SS2022）；0 个路由方案；0 条链式规则；节点 21/34/47 `landing_egress=1, landing_chain=1`。
- 基线（`eb047757` 完整副本，`GOMAXPROCS=2 go test -p 1 -count=1 ./...`）15 项失败；本分支最终代码同命令 15 项，名称完全相同
  （`runs/landing-abuse-defaults/logs/{baseline,final}-failures.txt`），新增 0。
- PostgreSQL（临时 schema）：落地 / 出站 / 链式 / 防盗 / 默认规则测试全部通过（`logs/landing-pg.log`）。
- 节点程序：`go vet` 通过；`landing`（39 项含真实 Core）、`socket`、`service` 通过；plan 107 的 netns 数据面测试通过。
- 前端：`tsc --noEmit` 通过；改动文件 eslint 0 error，warning 8 个与基线相同；`vite build` 通过，主包 2,898,968 B。
- CI（CI Build Check：前端 / 节点程序 / 后端 / PostgreSQL 契约）：`39754455837`（`61312aea`）全绿。
- E2E（`go-gost/x/landing/abuse_netns_test.go`，真实 Core `8f5b614`，三个 netns，`logs/e2e-final.json`）：
  - 默认规则：经 SS2022 / Reality 节点访问目标 TCP 25 被封，443、465、587 通；BT 握手被封、同端口普通数据通过。
    加「防滥用」预设的节点：465/587/2525/23/445 被封，443、8081 通。
  - 防盗：错误 SNI、无 SNI、晚到的错误 SNI 探测全部断开；正确 SNI（含晚 0.5 秒）拿到真实目标证书；未防盗节点把错误 SNI
    转给了目标（目标日志看到 `www.example.com` 一次 = 被偷的路径）；关闭防盗后辅助入站被删、探测恢复转发。
  - 已认证用户（防盗开 vs 关，各 600 次新连接 + 6 次 256 MiB 下载）：首字节延迟 p50 2.86–3.00 ms vs 2.35–2.37 ms
    （+≈0.5 ms），Core CPU 每条新连接 2.07–2.10 ms vs 1.63 ms（+≈0.45 ms），吞吐 480–512 vs 444–550 MiB/s（无差别），0 失败。
  - 丢包：单程 40 ms 和 100 ms 延迟 + 客户端 3 % 丢包，各 1000 次（防盗 / 不防盗各一半）新连接 0 失败，延迟分布相同。
  - 晚到的 ClientHello：整包晚 0.5 秒 / 1.5 秒：0/30 失败（`TCP_DEFER_ACCEPT` 之前是 30/30 失败）；
    ClientHello 头 300 字节先到、其余晚 0.15 秒 0/30，晚 0.5 秒 30/30 失败（不防盗只是慢）——剩余的已知代价。
  - 空闲：防盗开/关 RSS 34.8 / 36.0 MiB，线程 13 / 13；开启瞬间 fd 19→38（REALITY 一次性经防盗探测目标握手长度），
    测试结束回到 14，无泄漏。
- 共享服务器协议（`runs/landing-abuse-defaults/extra-sniff/results.json`）：SS2022、AnyTLS、Snell v4、Snell v5、Mieru TCP、
  Mieru UDP 六种入站全部：TCP 25 被封、443 通、TCP BT 握手被封、同端口普通数据通过；UDP 上的 uTP 都没被识别
  （Core 的 uTP 嗅探器时间戳比较有误，见契约 §10）。
- 计划大小：生产同款（1 个节点、内置默认）0 → 2 条规则 / 251 B；50 个节点、一半用 10 条规则的方案：10 → 12 条 / 3,438 → 4,711 B。
