# 107 落地出站与路由（plan 101 第三期，FLVX 侧）

> 编号说明：107 号已被 `107-ota-all-fork55.md`（OTA 记录）占用，按任务分配仍沿用 107，文件名不同。

基线：生产 `3.0.27-fork.55`（`maintenance/3.0.27-fork.55-landing-font-size` 头 `c16969d6`，代码同 `e85f75cf`）；
第二轮变基到生产 `3.0.27-fork.56`（`origin/maintenance/3.0.27-fork.56-quota-log-hotfix` 头 `adf61e3e`，go-backend 与
fork.55 相同，fork.56 只改了节点程序）。
开发分支：`feature/3.0.27-landing-egress`，worktree `/root/projects/flvxx/flvxx-landing-egress`。
发布：**不打 tag、不发布、不升级生产、不 OTA**。用户先看截图并批准（见 ui-add-only-preview-first）。
妙妙屋侧由后续任务 `landing-egress-mmwx` 按契约 `/root/flvx-workers/contracts/landing-egress-v1.md` 实现。

## 已定设计（来自任务书，协调人 + 用户 2026-10-19）

- 只有管理员能配置；用户只看到只读的「出站：…」标签。
- 新管理员标签页「出站与路由」：全局可复用的 **出站模板** 和 **路由方案**。
- 出站模板类型：直连、WARP（自动注册 / 手动填写）、代理（SOCKS5 / HTTP / Shadowsocks 2022+AEAD / VLESS TCP+Reality，可粘贴 ss:// vless:// 导入）、FLVX 落地节点（可选经某条 FLVX 规则 = 隧道链）。封锁为内置。
- 每种出站都有 **IP 版本**：跟随系统 / IPv4 优先 / IPv6 优先 / **强制 IPv4** / **强制 IPv6**；强制必须是真保证（另一族的 IP 字面量也不能出去）。
- 路由方案：有序规则 + 默认出站；规则属性：规则名称、目标域名、目标 IP、目标端口、来源 IP、来源端口、网络、协议（嗅探 http/tls/quic/bittorrent）、适用落地节点；动作 = 出站模板或封锁；同一属性内任一项命中（或），多个属性全部命中（且）。
- 预设：防止送中、测速分流、AI 解锁、抖音/TikTok、流媒体、屏蔽 BT、屏蔽广告、屏蔽中国大陆（3 条：目标 IP geoip:cn、目标域名 geosite:cn、来源 IP geoip:cn → 封锁，插在安全规则后最前，不重复）。
- 「禁止大陆直连」（来源 IP geoip:cn → 封锁）必须豁免全部 FLVX 节点地址（自动更新）+ 管理员「来源白名单」。
- 规则行有 上移/下移/置顶/置底；安全规则（私网/回环/元数据 → 封锁）锁定在第一行，不能移动/删除。
- 内置方案「默认（直连）」= 现在的行为；迁移后所有服务器/节点都在它上面，生产无变化。
- 分配：落地服务器设置新增「出站与路由」区块（路由方案）；节点详情新增管理员「路由方案」覆盖（默认跟随服务器）。
- 共享服务器：妙妙屋未声明支持前，FLVX 照旧发送现有负载，界面显示「该共享服务器暂不支持自定义出站」并禁用选择。
- UI 只加不改；与现有落地页同一套组件、`.flvx-landing-text` 字号、亮/暗、电脑/H5。

## 技术设计（本任务确定，偏离任务书处已注明）

### 1. 引擎能力核验（`mmwxc-core` v1.4.59 = Xray 26.3.27，`ImoLR/Xray-core-mmwx@8f5b6145`）
- API：`ado/rmo/lso`（HandlerService 出站热增删）、`adrules/rmrules/lsrules`（RoutingService）。该分支 `ReloadRules` 为 copy-on-write：`adrules`（不带 `-append`）**原子整体替换**全部规则，构建失败时旧规则原样保留。
- gRPC 服务端默认单条消息上限 4 MiB → 面板按 geo 条目体积限制每台服务器的规则总量（见 §5）。
- freedom `targetStrategy`（`domainStrategy` 别名）语义（`transport/internet/config.go` 策略表）：`UseIP` 双栈随机、失败回落系统解析；`UseIPv4v6`/`UseIPv6v4` 先 A/AAAA 再回落；`ForceIPv4/6` 只查该族、失败即失败。**只对域名目标生效，IP 字面量不受约束**。
- `sendThrough` 绑定本机某族地址后：Go 拨号器对另一族字面量直接报 "no suitable address"，UDP 套接字为该族专用（AF_INET / 绑定非映射 v6 地址时内核置 v6only），DNS 也只查该族（`LookupForIP(…, gateway)`）。
- 路由 `IPOnDemand`：遇到 IP 条件即解析域名 → 第一条安全规则先对**所有**域名目标做私网判定，用户的域名规则无法绕过（`IPIfNonMatch` 下域名规则命中后不再解析，可被 `localtest.me` 一类域名绕过）。妙妙屋 Helper 的 FLVX Core 已用 `IPOnDemand`。
- WireGuard 出站：首次使用才创建 gVisor 网络栈（热添加不占资源），DNS/可达族由接口地址决定；无 CAP_NET_ADMIN → `noKernelTun: true`。
- 嗅探：所有协议嗅探器（http/tls/bittorrent/quic/utp）总会运行并写入 `protocol`，**无需改入站配置**（改入站会导致入站重建、断线）。

### 2. IP 版本的落地方式（保证）
| 选项 | 直连 (freedom) | 代理 / FLVX 落地节点（连上游） | WARP（隧道内） |
|---|---|---|---|
| 跟随系统 | `UseIP`（= 现有默认直连） | 不设（系统解析） | `ForceIP` + v4/v6 两个接口地址 |
| IPv4 优先 | `UseIPv4v6` | sockopt `UseIPv4v6` | `ForceIPv4v6` + 两个地址 |
| IPv6 优先 | `UseIPv6v4` | sockopt `UseIPv6v4` | `ForceIPv6v4` + 两个地址 |
| 强制 IPv4 | `ForceIPv4` + `sendThrough`=本机 IPv4 + `IP_BIND_ADDRESS_NO_PORT` | sockopt `ForceIPv4` + `sendThrough` 本机 IPv4；字面量上游必须是 IPv4 | `ForceIPv4` + **只配 IPv4 接口地址** |
| 强制 IPv6 | 同上 IPv6 | 同上 IPv6 | `ForceIPv6` + 只配 IPv6 接口地址 |
- 本机地址由节点程序探测（UDP connect 到公共地址取本地源地址，不发包），每次调和重新探测；服务器没有该族地址时该出站渲染为黑洞（连接失败，状态里提示）。
- `IP_BIND_ADDRESS_NO_PORT`（customSockopt，TCP）避免显式绑定导致本地端口按目的地共享失效、端口耗尽。
- WARP 端点：自动模式下按服务器连通性选字面量端点（有 IPv4 → `162.159.192.1:2408`，否则 `[2606:4700:d0::a29f:c001]:2408`），与隧道内的 IP 版本无关。

### 3. 「禁止大陆直连」与 FLVX 节点豁免
- 选型：**来源集合差集**。凡带「来源 IP」的规则，节点程序/Helper 把来源列表展开（geoip 从 geoip.dat 读取，CIDR 原样）后减去 `exempt_sources`（面板下发：全部 FLVX 节点 server_ip/IPv4/IPv6/额外 IP/内网 IP，域名在面板解析，+ 来源白名单），以 CIDR 列表写入规则。效果正是「大陆来源 − 豁免 → 封锁」，被豁免的流量继续按后续规则正常分流。
- 为什么不用其他方案：Xray 规则是首条命中、同一字段内只能"或"，没有"且非"；复制规则（豁免副本）会让 geosite 类规则内存翻倍、规则数翻倍；loopback 二次分发每条连接多一层管道拷贝并可能干扰连接计数；nftables 只能做封锁、共享服务器上也没有 FLVX 的 nft。差集方案对两类落地一致、只多一次性 CPU（约 1 万条 CIDR）。
- 语义：来源 IP 规则只对直连客户端生效；经 FLVX 节点转发进来的流量（来源是 FLVX 节点）和来源白名单里的地址不受任何来源 IP 规则影响（界面提示一行说明）。
- 只有规则里用到来源 IP 时才下发豁免列表，节点地址变化才会触发该服务器的修订号递增。

### 4. 下发与热更新
- 面板把模板/方案编译成**服务器级的扁平计划** `egress`：`outbounds[]`（具体出站，WARP 已解析为本服务器的 WireGuard 账号，FLVX 落地节点已解析为 SS2022/VLESS 凭据）+ 有序 `rules[]`（每条带 `inbound_tags`，按节点所用方案分组、适用落地节点求交，公共前缀合并）+ `exempt_sources[]`。默认方案的节点不产生任何规则（落到 Core 默认出站 = 现有直连）。
- 出站 tag = `flvxo-` + 内容哈希：先 `ado` 新出站 → `adrules` 原子替换规则 → `rmo` 不再引用的旧出站，永不出现规则指向不存在出站（Xray 会回落默认直连）的窗口。
- 新 Core 进程（epoch 变化）先应用出站+规则，再加入站，没有无规则的窗口。
- 基础配置变更（加 RoutingService、`IPOnDemand`、显式 policy 超时）只在 Core 下次启动时生效；节点程序探测 `lsrules` 是否可用（每个 epoch 一次）。已在运行旧配置的 Core（目前只有节点 21）**首次启用非默认方案需要重启一次 Core**：界面保存前提示「保存后该服务器的落地连接会短暂中断」并要求勾选确认；未确认时节点程序不重启、不应用出站（继续直连）并上报 `restart_required`。之后的方案修改全部热更新。
- 计划内容哈希存 `landing_server.egress_hash`，变化即递增修订号（与共享服务器 desired 哈希同理），不需要每个修改入口手动递增。

### 5. geo 数据
- Core 发布包不带 geoip.dat/geosite.dat（只有二进制 + checksums）。节点程序按需下载固定版本 `Loyalsoldier/v2ray-rules-dat@202610182205`（geoip.dat 16.6 MB sha256 `010bfc2e…7959`，geosite.dat 11.2 MB sha256 `3691de38…1dd2d`），经同一组 GitHub 镜像 + sha256 校验，只在规则引用 geoip:/geosite: 时下载，已存在且校验通过不重复下载，下载前检查磁盘余量。
- 规则经 `adrules` CLI 构建（CLI 进程读 geo 文件、流式只取所需分类），Core 进程本身不读 geo 文件、只持有被引用的分类。
- 面板内置该版本的分类表（代码与体积），保存时校验分类存在，并限制体积：单条规则 geo 合计 ≤ 2.5 MiB、单服务器计划 ≤ 3 MiB（gRPC 4 MiB 上限内）。`geosite:cn` 1.96 MB（11 万域名）可用；`category-ads-all` 5 MB 超限 → 屏蔽广告预设用 `category-ads`（843 条）。
- 共享服务器：妙妙屋声明 geo 数据可用才允许 geo 规则，否则保存时拒绝并提示。

### 6. 数据模型（SQLite/PostgreSQL 兼容，均有 `TableName()`）
- `landing_outbound`：出站模板（name、type、ip_version、settings JSON 文本、remark、时间戳）。
- `landing_route_profile`：路由方案（name、default_outbound、rules JSON 文本、时间戳）；id=0（不入库）= 内置「默认（直连）」。
- `landing_warp_account`：每台服务器 × WARP 模板一个自动注册账号（device id、token、密钥、地址、reserved、端点、账户类型、错误与退避）。
- `landing_egress_setting`（单行）：来源白名单。
- `landing_server` 增加 `route_profile_id`、`egress_hash`、`egress_restart_ok`、`egress_state`、`egress_error`、`egress_checked_at`；`landing_node` 增加可空 `route_profile_id`（空 = 跟随服务器）；`node` 增加 `landing_egress`（节点程序能力版本）。
- 迁移只加列/加表，默认值 = 内置默认方案，生产行为不变。

### 7. 共享服务器能力协商
- `GET /shares` 每个共享新增 `egress` 对象：`{"versions":[1],"allowed":true,"geoip":true,"geosite":true}`。FLVX 仅在 `versions` 含 1 且 `allowed` 为真时启用选择并在 desired 里附加 `egress`；否则照旧负载 + 界面禁用提示。

## 清单

- [x] 基线：未修改代码的后端全量测试失败集合（完整仓库副本，串行）。
- [x] 计划文档（本文件）。
- [x] 节点程序：计划类型、渲染（freedom/wireguard/socks/http/shadowsocks/vless）、强制 IP 版本、来源差集、geo 下载校验、RoutingService 热应用、首次重启确认、状态上报、能力上报。
- [x] 节点程序测试：黄金 JSON、真实 Core 配置校验、真实 Core 强制 IPv4/IPv6 证明、来源豁免、热更新不重启。
- [x] 后端：模型/迁移、仓储、校验、计划编译（分组/求交/前缀合并/限额/环路检测）、WARP 自动注册（mock 测试）、链接导入、native/共享下发、修订号哈希、用户只读标签。
- [x] 后端测试：SQLite + PostgreSQL、handler 校验（私网上游拒绝、安全规则不可改、被使用的方案/出站不能删）、全量对比基线。
- [x] 前端：「出站与路由」标签页、出站编辑、路由方案编辑（预设/排序/锁定安全规则）、服务器设置区块、节点覆盖、用户标签、共享/旧版本禁用态；build + lint。
- [x] 契约文档 `landing-egress-v1.md`。
- [x] 第二轮：变基到 fork.56；节点程序改用 fork.56 的 atomicfile / RoutineLogger / Backoff；Core 只在文件变化时校验哈希、下载失败退避。
- [x] 资源测量（1 vs 50 节点 RSS/CPU、1000 连接前后 fd/goroutine、日志/磁盘增长）。
- [x] 截图集（电脑 + H5、管理员 + 用户、暗色）+ 发布到预览服务器。
- [x] 推送分支、CI 全绿（最终代码 `b5a4f63a`：run 39256044311 前端/节点程序/后端构建 + PostgreSQL 契约全部成功）、summary.md（final.md 由自动复核生成）。

## 核验记录

- 2026-10-19 基线：`c16969d6` 完整仓库副本（`git archive`，与工作树隔离）`GOMAXPROCS=2 go test -p 1 -count=1 ./...`，失败集合 15 项，与 plan 101 基线完全相同（Backup×5、Federation×4、OpenMigrates×2、RenewalCycle、ServiceMonitor×2、TunnelUpdateAddressInUse）。清单 `/root/flvx-workers/runs/landing-egress/baseline-failures.txt`。只拷 go-backend 时另有 3 项安装脚本测试因缺仓库根文件失败，已改为完整副本重跑。
- 节点程序基线：`go-gost/x` 的 `landing`、`socket`、`service` 全部通过（unix socket 路径需短 TMPDIR `/root/tmp-work/lt`）。

## 第二轮（2026-10-19，第一轮因会话额度中断）

### 变更
- 变基：`feature/3.0.27-landing-egress` 变基到 `origin/maintenance/3.0.27-fork.56-quota-log-hotfix`（`adf61e3e`），无冲突；
  落地文件写入已走 fork.56 的 `atomicfile.WriteFile`。
- 节点程序（`go-gost/x/landing`）：
  - Core 二进制与 geo 文件按 stat 指纹（大小、mtime、inode、ctime）缓存校验结果，只在文件变化后重算 SHA256
    （此前每 10 秒对 30 MiB 的 Core 算一次哈希）。
  - Core / geo 下载失败按 `logsafe.Backoff` 退避：1、2、4、8、16、30 分钟封顶，每个文件单独计时；状态里写明
    「N 分钟后重试」（此前每 10 秒把 4 个镜像全走一遍）。
  - `systemctl set-property` 只在节点程序启动后第一次和限额变化时执行（此前每 10 秒执行并重写 drop-in）。
  - 对账、出站与路由、下载失败经 `logsafe.RoutineLogger` 写日志：首次失败、10 分钟汇总、恢复各一行；等待确认重启不算失败。
  - geoip.dat 只解码目标分类（其它分类读完国家码即跳过）；展开后的来源集合在周期性重渲染之间复用，不再每 5 分钟重读 16 MiB 文件。
  - 稳态对账（没有入站增删）只采样一次计数：每 10 秒 `statsquery` 从 3 次降为 1 次；删除入站前后的采样保留。
- 后端：规则里 `*.example.com` 规范化为 `domain:example.com`，其它通配报错并提示；节点主机名 DNS 失败时保留上次结果，
  1 分钟内不重试（每次失败最多阻塞 2 秒）；新增可选真实 WARP 注册测试。
- 前端：审查第一轮未提交的代码后整理成一个提交；新增管理员服务器卡片上的「出站与路由应用失败 / 待重启生效」标签；
  H5 规则行菜单不再显示无效的上移/置顶；规则编辑提示支持 `*.example.com`；方案列表「默认出站：直连」。
  主包 2,821,057 → 2,830,854 B（+9.8 KB），新标签页按需加载 33,482 B。

- 自审补充修复（同轮）：
  - 出站与路由状态只在变化时写库（原生服务器每分钟同步、共享服务器每次轮询都会重写一行）；节点程序报告 Core 已就绪后，
    「确认重启一次」标志自动清除，之后若再出现需要重启的情况会重新征求确认，而不是静默重启。
  - 规则「适用落地节点」里的节点被用户删除（或到期自动删除）后，保存方案不再报错：已删除的节点自动移除；
    若该规则的节点全部被删，规则自动停用（空范围等于「全部节点」，绝不扩大范围），保存时提示管理员。
  - 删除带原生落地服务器的节点时，一并删除该服务器的 WARP 账号行（设备密钥与令牌）。

### 测试（第二轮代码）
- 后端全量 `GOMAXPROCS=2 go test -p 1 -count=1 ./...`：失败 15 项，与基线集合完全相同（新增 0、修复 0），
  清单 `runs/landing-egress/r2-backend-failures.txt`。
- 节点程序：`go vet` 通过；`landing`（含真实 Core 用例）、`socket`、`service`、`atomicfile`、`logsafe`、`nftables` 全部通过。
- 新测试均先确认「去掉修复就失败」：`TestCoreHashedOncePerFileChange`（6 次 tick 原来哈希 6 次）、`TestCoreDownloadBacksOff`、
  `TestGeoDownloadBacksOffPerFile`、`TestResourceLimitsSetOnlyOnChange`、`TestSourceExpansionReusedAcrossRenders`、
  `TestLandingSteadyTickCLICalls`（旧逻辑 3 次 tick = statsquery×9，新逻辑 ×3）、`TestLandingEgressResolveCachesFailures`、
  `TestLandingEgressRepository`（状态不变不写库、就绪后清除重启确认、已删除节点的规则范围；SQLite + PostgreSQL）、
  `TestLandingDisabledEmptyServerNodeDeletion`（删节点后 WARP 账号行为 0）。
- netns 数据面（root，真实 Core）：强制 IPv4/IPv6、大陆来源封锁与豁免、豁免后仍命中后续规则、UDP 会话族限制全部符合预期
  （`runs/landing-egress/r2-e2e-netns-dataplane.json`）。
- 真实 WARP（新注册的免费设备，测试后删除）：经渲染出的 WireGuard 出站访问 Cloudflare trace，`warp=on`；
  强制/优先 IPv4 → IPv4 出口，强制/优先 IPv6 → IPv6 出口；强制 IPv4 访问不了 IPv6 字面量，强制 IPv6 访问不了 IPv4 字面量。
- 面板侧真实 WARP 注册（`TestRegisterWarpLive`）：免费账号、v4/v6 地址、reserved 正常，删除成功。
- 前端：`tsc --noEmit` 通过，`vite build` 通过；新文件 eslint 0 问题，改动的 4 个旧文件 warning 数与基线相同、0 error。
- 分支 CI（CI Build Check）：见下方推送记录。

### 资源测量（`TestEgressResourceProfile`，真实 Core，一个网络命名空间）

| 状态 | RSS | Go 堆 | fd | 协程 |
|---|---|---|---|---|
| 1 个节点，默认方案 | 29.6 MiB | 3.4 MiB | 10 | 14 |
| 50 个节点，默认方案 | 33.2 MiB | 4.6 MiB | 108 | 161 |
| + 强制 IPv4 出站 + 屏蔽 BT | 33.5 MiB | 4.7 MiB | 108 | 161 |
| + 空闲 WARP 出站 + AI geosite | 32.5 MiB | 4.4 MiB | 108 | 161 |
| + 屏蔽中国大陆（3 条，来源集合减 30 个豁免） | 43.1 MiB | 13.3 MiB | 108 | 161 |
| 1000 个连接同时打开（经强制 IPv4 出站，全部回显成功） | 131.5 MiB | 280.5 MiB | 2108 | 7161 |
| 关闭后 15 秒 | 136.6 MiB | 282.4 MiB | 108 | 161 |
| 关闭后 2 分钟 | 135.3 MiB | 113.5 MiB | 108 | 159 |

- fd 与协程在 1000 个连接关闭后完全回到基线（无泄漏）；堆在强制 GC 后回落，RSS 由 Go 回收器之后逐步归还。
- 屏蔽中国大陆整体下发 0.57 秒；来源规则展开为 10,094 条 CIDR。
- Core 空闲 CPU（50 节点 + 完整方案，30 秒）≈ 0.07%。
- 节点程序每次对账（10 秒一次）：自身 CPU 21 → 12 ms，Core CLI 子进程 CPU 77 → 44 ms，耗时 89 → 50 ms（含测试用 netns 包装开销）。
  计数上报另外每 5 秒执行一次 `statsquery`（plan 101 起的既有行为，未改）。
- 磁盘：落地目录状态文件合计 54 KB，Core 日志整个测试期间 359 字节（只有启动行），节点程序日志 0 行。
- 观察（与出站无关，未改）：一个基本空闲的连接约占 Core 97 KiB RSS；默认 MemoryMax 256 MiB 大约容纳 2,000 个空闲连接，繁忙连接更少。

### 截图与契约
- 截图 46 张（电脑 + H5、管理员 + 用户、暗色、H5 底部视口），发布到
  `http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/landing-egress/`；脚本 `runs/landing-egress/gallery.cjs`。
- 契约 `/root/flvx-workers/contracts/landing-egress-v1.md` 已按最终代码更新（下载退避、稳态零调用、实测数字、真实流量验证）。

### 发布时需要（本任务不发布）
- 面板升级（新表 4 张、新列只加不改，默认值 = 内置默认方案，生产行为不变）。
- 原生落地节点（目前只有节点 21）OTA 到新版本节点程序后才能选择方案；节点 21 的 Core 由旧节点程序以旧配置启动，
  第一次选非默认方案时需要在界面勾选「重启一次落地 Core」（约 1–3 秒中断），之后改方案都是热更新。
- geo 数据只在方案用到 geoip/geosite 时由节点程序下载（约 27 MiB，经镜像 + SHA256）。
- 共享服务器（妙妙屋 server 7）要等 Custom/Helper 实现契约（`landing-egress-mmwx`）后才能选方案，之前界面显示禁用。
