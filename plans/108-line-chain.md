# 108 线路机隧道：只转 Reality 落地 +「我自己的节点」（经隧道出口链式落地）

基线：生产 `3.0.27-fork.58`（`99cac160`，plan 107 落地出站与路由已发布）。
开发分支：`feature/3.0.27-line-chain`，worktree `/root/projects/flvxx/flvxx-line-chain`。
发布：**不打 tag、不发布、不升级生产、不 OTA**。用户先测试并批准截图（ui-add-only-preview-first）。
运行目录：`/root/flvx-workers/runs/line-chain-r3/`（最终日志、截图、summary.md / final.md）；保留 r2 基线证据。
临时目录：`/root/tmp-work/line-chain/`（不放 /tmp；/tmp 是内存盘）。

## 已定设计（用户 2026-10-19，见任务书）

1. 「线路机」是**节点**开关（管理员在节点新建/编辑里设置，节点卡片显示标签）。隧道的任一**入口**节点是线路机 ⇒ 线路机隧道。
2. 线路机隧道上的规则目标（后端强制，管理员也一样）：不能手填 IP:端口；「我的落地节点」只能选 VLESS Reality Vision；「落地服务器（自动建节点）」协议锁定 VLESS Reality Vision；新增「我自己的节点」（链式）。已有规则在隧道变成线路机隧道后不中断，只标「不合规」并给管理员计数。
3. 「我自己的节点」：用户粘贴自己的节点链接 → 面板解析校验 → 从隧道出口节点探测可达 → 在**每个出口节点**上（没有落地程序就自动安装）建一个只监听 127.0.0.1 的 VLESS Reality Vision 节点，出站 = 用户的节点 → 规则目标 = 127.0.0.1:端口。多出口时每个出口一份完全相同的副本（同端口、同 UUID/密钥/shortId/SNI）。用户看不到这些节点，规则行有「复制链接」（地址 = 隧道入口，同「转发版」）。
4. 诊断/测速/流量/暂停/恢复与普通规则一样；诊断最后一步「出口→目标」的目标换成用户节点地址。
5. 计费：链式规则按普通规则计（入口计量 × 隧道倍率）；隐藏的链式节点不再按落地计费、不计入落地节点数上限；计入规则数（MaxRules）。

## 技术设计（本任务确定）

### 数据模型（SQLite/PostgreSQL 兼容，新模型都有 `TableName()`，不用 jsonb/serial）
- `node.line_machine`（bool，默认 false）：线路机开关。
- `node.landing_chain`（int，默认 0）：节点程序上报的链式能力版本（新节点程序 = 1）。
- `landing_server.chain_only`（bool，默认 false）：为链式规则自动创建的出口落地（不是公开落地服务器，用户永远看不到/用不了；管理员看到「链式」标签）。在该节点「启用为落地」会转成公开落地。
- `forward_chain`（新表）：一条链式规则一行。user_id、forward_id（创建完成前为 0）、tunnel_id、state（creating / active / failed）、op（进行中的操作说明）、progress、last_error、port（所有出口相同的 127.0.0.1 端口）、Reality 参数（uuid、private/public key、short_id、dest、server_names）、upstream（解析后的出站 JSON，含密钥，接口不回传）、upstream_protocol/host/port/summary（展示与诊断用）、upstream_hash（去重）、idem_key（幂等键，(user_id, idem_key) 唯一，空值不参与）、prev_upstream（改链接时的回滚点）、时间戳。
- `forward_chain_node`（新表）：链式节点在某个出口上的副本。chain_id、landing_server_id、node_id、port、state（active / pending）；(landing_server_id, port) 唯一。
- **不复用 `landing_node` 表**：落地节点的列表、数量上限、计费、到期/额度策略、链接、共享推送、`MatchLandingTarget` 全部原样不动，链式副本不会混进去。副本只在下发期望状态时加入（节点程序 id = 2^40 + 副本 id，tag 仍是 `flvxl-<id>`），节点程序上报的副本计数在入账前丢弃（与普通落地节点 id 不重叠）。
- 端口：链式副本端口与该节点的落地节点、规则端口段、转发/隧道/部署端口互斥（`landingFreePort`、`LandingPortInUse`、`ValidateNodeLandingPortRange` 都把副本算进去）；所有出口取同一个空闲端口。

### 节点程序（go-gost/x/landing）
- 期望状态 `Node` 新增 `listen`（只允许空 = 0.0.0.0 或 `127.0.0.1`）和 `chain`（该节点专用出站）；有 `chain` 的节点必须监听 127.0.0.1、协议必须是 VLESS Reality Vision。
- 渲染：链式出站 tag `flvxc-<id>-<hash>`；路由规则顺序 = 安全规则（锁定第一）→ 每个链式节点一条「inboundTag → 链式出站」→ 服务器方案规则。复用 107 的热更新流程（先加出站 → 原子替换规则 → 删旧出站），不重启 Core；链式出站失败则该节点不对外服务（fail closed）。
- 出站类型扩展：VLESS（tcp/ws/grpc + none/tls/reality）、VMess（AEAD，tcp/ws/grpc + tls）、Trojan（tcp/ws/grpc + tls）、Shadowsocks（含 SS2022）、SOCKS5、HTTP/HTTPS 代理、Hysteria2（Core 26.3.27 支持，含 salamander）。TUIC 不支持（Core 没有 TUIC 出站），明确报错。跳过证书验证（allowInsecure）Core 已在 2026-06-01 后移除，此类链接明确拒绝（可用 pinSHA256）。
- 拨号：目标是回环地址时不绑定出口网卡（节点设置了「出口网卡名或IP」时，出口拨 127.0.0.1 仍然可用）。
- 能力上报 `landing_chain: 1`；首次安装 Core 前检查磁盘剩余 ≥ 150 MiB、内存可用 ≥ 128 MiB，不够就明确报错。

### 面板后端
- **线路机强制**（单条创建/修改、批量换隧道、备份导入、API；管理员同样）：见上。批量换隧道遇到链式规则 → 拒绝（请在编辑里单独换隧道）。联邦共享：线路机节点不能新建共享给其他面板（对方面板决定目标，无法保证）。
- **不合规**：线路机隧道上目标不是 Reality 落地节点也不是链式的规则，列表里标 `lineNonCompliant`，管理员规则页显示计数。
- **链式创建（异步，带进度）**：请求内同步完成：链接解析 + 字段长度限制 → 地址校验（拒绝内网/回环/链路本地/CGNAT/组播/未指定/云元数据、面板自身地址、任何 FLVX 节点地址；域名解析后任一 IP 命中即拒绝）→ 隧道/用户权限与规则数检查 → 出口检查（有出口、出口都不是线路机、在线、节点程序支持链式、非远程节点、非节点 36、资源不过低）→ 去重（同用户同隧道同一节点链接 = 拒绝并说明已有规则名）→ 限流 → 从每个出口 TCP 探测用户节点（UDP 协议如 Hysteria2 跳过并提示）→ 写入 `forward_chain`（creating）+ 每个出口的副本行（必要时创建 chain_only 落地服务器）→ 返回 chainId。后台驱动：逐个出口下发（首次自动安装 Core，最长等 5 分钟，进度写入 progress）→ 全部确认后创建规则（目标 127.0.0.1:端口，gost 模式）→ active。任何一步失败：删除规则、副本、链式行并重新下发出口（落地程序保留），记录失败原因（失败记录保留 1 小时用于幂等去重，之后清理）。
- **安全**：幂等键（前端每次提交生成，后端 (user_id, idem_key) 唯一，重复提交返回同一结果）；每用户单飞（同一用户同时只能有一个链式操作，第二个返回「正在创建，请稍候」）；限流（非管理员每用户 10 分钟内最多 5 次链式创建/改链接，全局同时最多 3 个后台链式操作）；按钮提交中禁用；普通规则创建也带幂等键（内存去重 10 分钟），防止「落地服务器（自动建节点）」连点建出多个节点。
- **对账器**（启动时 + 每 2 分钟一轮，批量、无每项协程）：creating 超过 10 分钟且不在内存驱动中 → 回滚；有 prev_upstream 超过 10 分钟 → 回滚到旧链接；failed 超过 1 小时 → 删除；没有规则的 active 链 / 没有链的副本 → 清理并重新下发。
- **删除不留残留**：所有删规则路径（单条、批量、强制删除、远程节点删除）、删用户、删隧道、隧道编辑改出口（新出口建副本、旧出口删副本）、节点删除（chain_only 落地服务器无副本时随节点删除；有副本则拒绝）、节点改为线路机（承载副本时拒绝）、孤儿清理 API（plan 098，新增链式孤儿检测与清理）。出口离线时数据库先删除，期望状态在节点重连后的每分钟全量下发中收敛（节点程序删除期望状态里没有的入站）。
- **改链接**：校验 + 探测后热更新所有出口副本的出站，用户链接不变；任一出口失败回滚到旧链接。**换隧道**：新出口必须已准备好（没有落地程序时先触发安装并提示约 1 分钟后再保存），建新副本 → 改规则 → 删旧副本。
- **诊断**：链式规则的「出口→目标」改为用户节点地址（解析后的 IP），其余步骤不变。
- **计费**：链式规则 `landing_node_id = 0`，按普通规则计费；副本计数在入账前丢弃，不计落地、不计落地节点数。

### 前端（只加不改）
- 节点编辑：「线路机」开关 + 说明；节点卡片「线路机」标签。
- 规则弹窗：线路机隧道隐藏「手动填写」，「我的落地节点」只列 Reality 节点，「落地服务器」协议锁定 Reality，新增「我自己的节点」（粘贴链接 → 解析摘要「协议 · 地址:端口」→ 提交后进度「正在准备出口…」→ 成功/失败提示）。普通隧道不变。
- 规则行：链式规则目标列显示「我的节点 · 协议 · 地址:端口」，加「复制链接」按钮（多入口每个入口一条）；不合规规则显示「不合规」标签；管理员规则页显示「线路机不合规 N 条」。
- 落地页（管理员）：chain_only 服务器显示「链式」标签和链式节点数。

## 清单

- [x] 读记忆/任务书/AGENTS.md，核对 worktree（`feature/3.0.27-line-chain` @ `99cac160`）与 plan 编号 108。
- [x] 基线：未修改代码的后端全量测试失败集合（完整仓库副本，串行）。
- [x] 节点程序：listen/chain 字段、出站类型扩展、链式路由规则与热更新、回环不绑网卡、能力上报、安装前资源检查。
- [x] 节点程序测试：黄金 JSON、真实 Core 配置校验（每种出站）、热增删不重启、回环拨号。
- [x] 后端模型与迁移、仓储方法（端口互斥、副本、链式行、孤儿检测）。
- [x] 后端链接解析与地址校验（独立包，单元测试覆盖每种协议与畸形输入）。
- [x] 后端线路机强制（所有建/改规则路径）+ 不合规标记 + 节点开关 API + 联邦共享保护。
- [x] 后端链式创建/状态/解析接口、异步驱动与回滚、幂等/单飞/限流、对账器。
- [x] 后端改链接、换隧道、所有删除路径、隧道编辑、节点删除、孤儿清理、诊断目标替换、复制链接、计费。
- [x] 后端测试：每条路径一个测试（强制、删除零残留、多出口副本一致、回滚、诊断、计费）；全量 `go test` 对比基线。
- [x] 前端：节点开关/标签、规则弹窗、规则行、管理员计数、落地页标签；build + lint。
- [x] 本机端到端（一次性进程）：客户端 → gost 入口 → gost 出口 → 本机 Core Reality 入站 → 本地 SS/SOCKS「用户节点」→ 测试 HTTP 服务。
- [x] 资源测量：1 vs 50 链式节点 Core RSS/CPU，1000 连接前后 fd/goroutine，日志/磁盘增长。
- [x] 截图集（电脑 + H5、管理员 + 用户、暗色弹窗）+ 发布到预览。
- [x] 推送分支、CI 全绿、summary.md / final.md。

## 核验记录

### 第三轮续作（2026-10-20，台湾时间）

- 接续 `747ab207`，没有重写已完成功能，没有发布/生产/OTA 操作。
- 基线证据：`/root/flvx-workers/runs/line-chain-r2/logs/baseline-backend.log` 和 `baseline-failures.txt`（15 个失败名称，含子测试）。
- 已完成实现：节点程序 `de2ab72b`；模型/解析 `73d5d71a`；后端 `c5b48dc0`、`747ab207`；前端 `64585de1`；测试/本地验收 `b7b21a28`、`8e4dacdb`、`8877b042`。
- 原有 C1–C7 本地验收已全过：r2 `logs/chain-lab-results.json`。包含双出口真实流量、重复拒绝、计费、改链接 Core PID 不变、单删/批删无残留。r3 将加强资源稳定值与未覆盖的清理路径证据。
- 截图旧报错已定位到采集脚本：`modeTo()` 的 `.first()` 误点导航「分组」，模拟 `/group/user/list` 缺少 `userNames`，在旧分组页报错；改为规则页按钮 `.last()`，补齐模拟响应，产品代码无需为此修改。r3 记录所有 console error/pageerror，不过滤网络报错。

- r3 后端全量：`GOMAXPROCS=2 go test -p 1 ./...`，失败名称（含子测试）仍是基线的 15 个，无新增、无缺项；对照 `r3/logs/backend-baseline-comparison.json`。新增并通过：`TestChainRapidConcurrentSubmit`（16 个同时提交只建 1 条规则/2 份副本）、`TestChainOfflineExitDeleteConverges`（离线副本在恢复在线的全量下发后归零）、`TestChainUserPolicyPausesWithoutDeleting`（到期/停用暂停，恢复链接不变，最终删除清空）。
- r3 节点程序：`FLVX_LANDING_TEST_BINARY=... FLVX_LANDING_GEODIR=... GOMAXPROCS=2 go test -p 1 -v ./landing ./internal/net/dialer` 通过（`logs/agent-core-tests.log`），覆盖黄金 JSON、每类出站/面板解析结果的真实 Core 校验、Core PID 不变的热增删、回环目标忽略出口网卡。首次命令误用了空包 `./dialer` 且漏传 geo 数据目录，属于测试环境错误，修正后通过。
- r3 数据库：本机一次性 PostgreSQL 18 容器（仅 127.0.0.1:26592、192 MiB 上限、tmpfs 数据、测试后已移除），`TestForwardChainLifecycleBothDialects` 的 SQLite/PostgreSQL 两项通过（`logs/postgres-chain.log`）；没有连接生产数据库。
- r3 加强验收发现并修复：链式规则的强制删除原先沿用旧行为，仅删数据库/Core，入口 GOST 服务残留。现在仅对链式规则先尽力清理规则服务再删除；普通规则原行为保留。所有常规删除路径的单元用例增加入口 DeleteService 断言；链式定向测试通过（`logs/chain-targeted-after-fix.log`）。本地 C8 会用真实服务再核验。
- r3 C8 真实复验通过（`logs/chain-delete-results.json`）：强制删除、删隧道、删用户后 `forward_chain` / 副本 / 规则行、两台 Core 的链式入站/出站、GOST 规则服务全部为 0；最后删除无副本的出口节点，chain_only 服务器行也为 0。临时进程/网络命名空间已自动清理。

### r3 实测资源（双出口，每个出口各一台 Core）

证据：`/root/flvx-workers/runs/line-chain-r3/logs/chain-lab-results.json`，C1–C7 全通过；修复后 C8 见单独的 `chain-delete-results.json`。

| 项目 | 出口 A | 出口 B |
|---|---:|---:|
| 1 条链式规则 Core RSS | 34,752 KiB | 35,260 KiB |
| 50 条链式规则 Core RSS | 35,016 KiB | 36,400 KiB |
| 增加 49 份副本的 RSS 增量 | 264 KiB（5.4 KiB/份） | 1,140 KiB（23.3 KiB/份） |
| 1 / 50 条时空闲 CPU（各测 5 秒） | 0.2% / 0.2% | 0.2% / 0.0% |
| 1 / 50 条时 fd | 8 / 57 | 8 / 57 |
| 1 / 50 条时 goroutine | 12 / 61 | 12 / 61 |
| 1 / 50 条落地状态文件大小 | 3,428 / 91,560 B | 3,428 / 91,560 B |
| 1000 连接前 / 回收后 fd | 57 / 57 | 57 / 57 |
| 1000 连接前 / 回收后 goroutine | 61 / 59 | 61 / 59 |
| 压测回收后的 RSS | 42,992 KiB | 44,836 KiB |

- 1000 次连接（50 并发）耗时 24.3 秒，0 失败；使用 5 次空闲采样最小值比较，两个 Core 的 fd/goroutine 均不高于压测前基线。RSS 保留约 5–7 MiB Go 堆缓存，不把 RSS 当作连接泄漏指标。
- 20 条保持连接：Core 合计增加 40 fd；用户上游恰好 20 条连接。每条连接额外成本 2 fd，即出口与回环 Core 间的一对 socket，无额外上游连接。
- 压测与回收期间状态文件增长 0 B；入口/出口 A/出口 B 的 agent 日志增长 264,370 / 684,779 / 686,131 B（已有 GOST info 连接日志），面板 4,190 B。测试 direct 模式 Core stdout 为 `/dev/null`，不将其误报为生产 journald 的实测；生产 Core 配置仍为 warning、无 access log，并沿用现有 systemd 资源限制。
- 验收脚本已扩展 C6 严格回收断言、C8 删除路径检查，并在每项结束即保存结果，避免中断丢证据。
- r3 界面补漏：仅在线路机目标区套用既有 `.flvx-landing-text`，使新增目标/粘贴说明符合落地字号要求；普通隧道不受影响。`npm run build` 通过。ESLint 对照本功能涉及的 9 个文件：基线/当前错误均 25，新增错误 0（原有 6 文件 + 新增 3 文件，见 `logs/frontend-eslint-comparison.json`）；全仓扫描还有其他未改文件的历史错误，不做无关清理。
- r3 截图完成：当前构建重新采集桌面 34 张、H5 26 张，管理员/用户、亮色/暗色弹窗、解析/进度/各类错误、复制链接、Reality-only 筛选与诊断均覆盖。逐张人工查看，H5 长列表另看原图；`screens/visual-review.md` 记录 60 张图片及校验值，`screens/errors.json` 为 `[]`，全部页面无横向溢出。图片内节点、版本与错误为合成数据，不代表已发布。
- 预览已发布至 `http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/line-chain/index.html`。目录、错误记录、代表截图和人工记录 HTTP 均为 200，`mmwx-preview` 原本在线，无需重启；新建 symlink，原位置不存在。记录见 `logs/gallery-publication.json`。
- 产品代码提交 `c6c744af` 已推送，CI Build Check 全绿：`https://github.com/ImoLR/FLVXR2/actions/runs/39389334183`。
- 最终后端复核：`GOMAXPROCS=2 go test -p 1 ./...` 完整运行，退出码 1；15 个失败名称与基线完全相同（含子测试），无新增。证据 `logs/backend-full-confirmed.log`、`.exit`、`backend-final-baseline-comparison.json`。前一次外层工具会话退出 143，因此另跑此次完整确认，没有将中断当成通过。
- 截图文档提交 `61d2524f` 已推送，CI 全绿：`https://github.com/ImoLR/FLVXR2/actions/runs/39390648765`。中文 `summary.md` / `final.md` 已写入 r3 运行目录，最终收尾提交的 SHA 与 CI 留在报告和 `logs/ci-final.json`。
- 已清理本任务 `/root/tmp-work/line-chain/`（逻辑大小 575,689,638 B）；必要实验日志、基线对照、测量结果和截图已留在 r3。任务 demo 进程、网络命名空间和 PostgreSQL 容器均无残留，记录 `logs/cleanup.json`。无生产/真实节点写入、无 tag/release/OTA、未改其他 worktree 或记忆文件。
