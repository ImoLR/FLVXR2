# 101 落地节点：FLVX 自有落地 + 妙妙屋共享服务器

基线：`3.0.27-fork.52`（生产），分支头 `3143042f`（`maintenance/3.0.27-fork.52-node-delete-wg-released`）。
开发分支：`feature/3.0.27-landing-nodes`，worktree `/root/projects/flvxx/flvxx-landing`。
发布：**用户先看截图并批准后**才发布（见 [UI 规则](#f-前端只加不改)）。本计划的开发任务一律不打 tag、不发布、不动生产和节点。

设计由用户在 2026-10-18 的长讨论中逐条确定，下面"已定设计"是约束，worker 不得改动；"技术规格"是协调人给出的实现规格，细节可在不违背约束的前提下调整，偏离处要在总结里说明。

## 已定设计（用户决定，2026-10-18）

1. **落地归 FLVX 管**：入站、出站、路由全部在 FLVX 设置。两种落地：
   - **FLVX 自有落地**：FLVX 节点（装了 FLVX 节点程序的服务器）启用为落地，由 FLVX 节点程序管理落地 Core。
   - **妙妙屋共享服务器**：妙妙屋（mmwx-custom）把整台服务器共享给 FLVX，那台机器不装 FLVX 节点程序，由妙妙屋已有的 Helper 替 FLVX 运行一个独立的落地 Core。妙妙屋只设共享上限、只显示汇总。
   - 两边都装了的机器归 FLVX 自己管，默认不共享；妙妙屋共享这类机器时只提示，FLVX 已在该机器启用落地时才禁止共享。
2. **FLVX 的节点和妙妙屋的节点完全分开**：不在官方妙妙屋里建节点，不混进妙妙屋的 Core/配置/节点列表。
3. **引擎**：我们的改版 xray Core（`mmwxc-core`，和妙妙屋 External 用的同一个），两种落地都用它。
4. **协议（第一版）**：SS2022 + VLESS Reality Vision，参照妙妙屋 Custom 的入站模板（`/root/projects/mmwx-custom/frontend/src/xray-manager.tsx` 的 `defaultInbound` / `defaultInboundForUser`，约 268–299 行）。
5. **节点由用户按需创建**：用户看到可用的落地服务器，在「落地」页建节点，或在规则里直接选"我的落地节点"/"落地服务器（自动建节点）"。节点限制（连接数、在线 IP、流量上限、有效期、倍率）只有管理员能改。
6. **计费**（见 [B](#b-计费与服务器流量限额)）：落地像隧道一样有"流量计算 单向/双向"和"倍率"，默认双向；规则指向外部落地 → 只算规则（隧道）；指向面板落地节点 → 只算落地，规则那段不计入用户总流量，但费率按隧道和落地中**较贵**的算；直连 + 经规则一起用 → 落地节点总量算一次。落地流量计入用户的同一个总流量。
7. **服务器流量限额**（取消"共享组"）：给"用户 × 服务器"设流量限额，凡经过这台服务器的该用户流量都算（这台服务器上的落地节点 + 入口/中转/出口经过它的规则）。
8. **用户总连接数**照旧只管规则（隧道）侧；落地节点的连接数按节点 / 服务器 / 妙妙屋共享上限各层单独限制，不并入用户总连接数。
9. **有效期**：节点有效期默认"跟随用户"；实际到期 = min(用户到期, 节点自身有效期, 服务器可用期限)；到期停用，宽限 7 天内续期原样恢复，超过自动删除并释放端口。
10. **默认值**：每个用户最多 5 个落地节点；每人在每台服务器最多 3 个；FLVX 自有落地默认端口段 30000–39999（可按服务器改）；落地按用户组授权可见（像隧道）。
11. **旧 sing-box"部署"功能**：新功能上线后隐藏旧入口；生产节点 21、25 上两个闲置 SS 入站在发布时清掉。后端代码和表保留。
12. **UI 只加不改**：主页的流量显示不动；可以新增页面/区块/标签，不改现有界面元素；做完给用户看截图（电脑 + 手机 H5），批准后才发布。
13. **妙妙屋端**：Custom 左侧导航新增「FLVX 共享」页：共享的服务器卡片 → 展开按 FLVX 用户列出节点（端口、协议、`已用 / 上限 GB`、连接数、状态、到期）；可切换"按服务器/按用户"；共享设置（端口段、总流量+重置日、总连接数、共享到期、资源上限）；暂停/取消共享。FLVX 节点不放进「节点管理」。
14. **测试**：用户少的 US 和 JP。JP = Breadcloud JP（FLVX 节点 21 = 妙妙屋服务器 11）测 FLVX 自有落地；US 年付（妙妙屋服务器 7 = FLVX 节点 35，FLVX 在它上面没有任何隧道/规则）测妙妙屋共享，FLVX 不在节点 35 启用落地。
15. **禁区**：FLVX 节点 36「Breadcloud JP2」（178.214.214.173）就是妙妙屋生产主机，**绝不能启用落地**（代码里对该节点直接拒绝启用，并在发布前核实）。
16. 套餐、订阅目录（合并节点/外部订阅，类似 Sub-Store）、出站/路由模板都是以后的事，本计划第一、二期不做。

## 技术规格

### A. 数据模型（建议；必须 SQLite/PostgreSQL 兼容，新模型都有 `TableName()`，不用 jsonb/serial，后端 handler 不直接调 `repo.DB()`）

- `landing_server`：id、source（`native`/`shared`）、node_id（native 时为 FLVX 节点 id）、remote_server_id（shared 时为妙妙屋服务器 id）、name、public_host（链接里用的地址，native 默认取节点公网地址，可改）、port_min/port_max、flow_type（1 单向 / 2 双向，默认 2）、traffic_ratio（默认 1.0）、max_connections（整台总连接数，0 不限）、默认值（新节点的 max_connections / max_online_ips / traffic_limit / 有效期模式与天数）、max_nodes_per_user（默认 3）、protocols（启用的协议模板）、reality 默认 dest/server_names、available_until（可空）、core_memory_mb/core_cpu_percent（默认 256 / 50）、status、时间戳。
- `landing_server_user_group`：landing_server_id + user_group_id（可见授权）。
- `landing_node`：id、user_id、landing_server_id、name、protocol（`ss2022` / `vless_reality_vision`）、port、inbound_tag（形如 `flvxl-<id>`）、凭据字段（SS2022：method、server_key、user_key；VLESS：uuid、flow、reality private_key/public_key、short_id、dest、server_names）、max_connections、max_online_ips、traffic_limit_bytes（0 不限）、flow_type/traffic_ratio 覆盖（可空）、expiry_mode（`follow_user` / `fixed`）、expires_at（可空）、status（active / paused_quota / paused_server_quota / paused_expired / disabled）、expired_at（宽限计时）、up_bytes/down_bytes（原始累计）、billed_in/billed_out、counter_epoch + last_seen 累计值（增量去重）、时间戳。
- `landing_user_setting`：user_id、max_nodes（默认 5）。
- `user_server_quota`：user_id、server_kind（`node` / `landing`）、server_ref_id、limit_bytes、used_in/used_out（按计费后口径）、period_key、paused、时间戳。节点（FLVX 服务器）新增"每个用户默认限额"字段（0 不限）；共享服务器的默认值放在 landing_server。
- `forward` 新增 `landing_node_id`（可空，0 表示无）：由规则窗口选落地生成的规则会写入；手填目标按地址匹配同一用户的落地节点时也写入。

### B. 计费与服务器流量限额

记号：T = 隧道倍率 × 隧道流量计算（单向 1 / 双向 2）；L = 落地倍率 × 落地流量计算（节点覆盖优先，否则取服务器）。上行/下行分别计，与现有 `in = up×系数`、`out = down×系数` 口径和取整一致（现有公式见 `go-backend/internal/http/handler/flow_ingest.go`）。

1. 规则目标不是面板落地，或是**别人的**落地节点 → 与现在完全一样。
2. 规则目标是**本用户自己的**落地节点 → 规则自身计数（forward in/out）、隧道授权计数（user_tunnel）照常累计；计入用户总流量和日/月额度的只有 `up×max(0,T−L)` / `down×max(0,T−L)`（隧道不比落地贵时为 0）。
3. 落地节点入站总流量（Core 入站计数，包括未认证连接，不能只算认证用户）→ 计入用户总流量和日/月额度：`up×L`、`down×L`；同时累计到 landing_node。
4. 由此：直连 = L；经规则 = max(T, L)；同一份流量不会重复计入用户总流量。
5. 一条规则里只要有一个目标是面板落地节点，就不允许再放其他目标（保存时校验）。手填目标匹配规则：host:port 与落地节点的公网地址/节点 IP（v4/v6/额外 IP）完全一致即视为指向该落地节点；出口节点就是落地机器时 127.0.0.1/::1 也算。
6. 用户总流量用完、用户到期、用户禁用 → 规则（现有逻辑）和落地节点一起停；重置/续期后一起恢复。

**服务器流量限额**（每用户 × 每服务器，"服务器" = FLVX 节点或共享服务器）：
- 普通规则：这份流量计入用户总流量的量，同样记到规则路径上的**每一台**服务器（入口 = 实际上报的入口节点；中转、出口按隧道链路；同一跳多台轮询时按台数均分——中转/出口的 relay 服务按隧道共用，看不到用户，只能估算）。
- 指向本人落地的规则：路径每台服务器记 `via×max(T,L)`；落地服务器记"落地入站总量×L + via×max(0,T−L)"。
- 直连落地：只记落地服务器（×L）。
- 超额：暂停该用户在这台服务器上的落地节点，以及路径经过这台服务器的规则（多入口规则第一版整条暂停）；到用户流量重置日或管理员调高限额后恢复。
- 周期跟随用户的流量重置日（现有 `resetMonthlyFlow`）。
- 与现有限额（用户总流量、隧道授权流量、规则流量上限、节点流量上限）并存，先到先停。
- 生产参考（2026-10-18 只读）：43 条隧道倍率全为 1（42 双向、1 单向）；14 个多节点中转层分布在隧道 5/10/11/30/31/33/60/62/63/64，上面只有 4 条规则；没有多目标规则。

### C. 有效期

- 节点有效期模式：`follow_user`（默认，服务器设置可改为"创建后 N 天" / "固定日期" / "永久"，新节点创建时带上）或 `fixed`（具体时间）。管理员可在节点详情改；用户不能改。
- 实际到期 = min(用户 exp_time, 节点 fixed 到期, 服务器 available_until)。
- 接入现有定时任务 `runResetAndExpiryJob`（`go-backend/internal/http/handler/jobs.go`）：到期 → 停用（从 Core 移除入站，记录 expired_at）；7 天内续期 → 原端口原凭据恢复；超过 7 天 → 删除节点、释放端口；到期前 3 天在「我的节点」标"即将到期"。
- 给用户续期时：`follow_user` 的节点自动跟随；有独立有效期的节点在续期界面提示是否一并延长（新增提示，不改原续期界面结构）。

### D. 节点程序里的落地 Core（FLVX 自有落地）

- 二进制：`ImoLR/mmwx-custom`（公开仓库）release `v1.4.59` 的 `mmwxc-core-linux-amd64` / `mmwxc-core-linux-arm64`，sha256 取自同一 release 的 `checksums.txt`；面板里固定版本 + sha256，经现有 GitHub 镜像（`githubMirrorPrefixes`，`go-backend/internal/http/handler/upgrade.go`）下载，回退直连；sha256 不符拒绝安装。
- 安装位置：节点程序目录下 `landing/flvx-landing-core`（**文件名不得含 "xray"**：妙妙屋 Helper 的 `countXrayProcesses` 按名字数 xray 进程，多一个会让妙妙屋 External 判定异常），配置 `landing/config.json`，期望状态 `landing/desired.json`。
- systemd 单元 `flvx-landing-core.service`：专用低权限用户 `flvx-landing`，`MemoryMax`/`CPUQuota` 取服务器设置（默认 256M / 50%），`NoNewPrivileges=yes`、`ProtectSystem=strict`、`ReadWritePaths=<landing 目录>`、`LimitNOFILE=65535`；端口 ≥1024。
- 基础配置：日志 warning、不记访问日志；`api`（HandlerService、StatsService）只监听 127.0.0.1；stats + policy（statsInboundUplink/Downlink）；Core 控制 socket（`MMWXC_CORE_CONTROL_SOCKET`，仅属主可访问）；出站 `freedom`(direct) + `blackhole`(block)；路由首条强制阻断私网/回环/链路本地/元数据/组播等目的地址（显式 CIDR：0.0.0.0/8、10.0.0.0/8、100.64.0.0/10、127.0.0.0/8、169.254.0.0/16、172.16.0.0/12、192.168.0.0/16、198.18.0.0/15、224.0.0.0/4、240.0.0.0/4、::1/128、fc00::/7、fe80::/10、ff00::/8，不依赖 geoip.dat）。
- 期望状态：面板下发完整期望状态（revision + 入站列表 + 连接限制），节点程序落盘后与 Core 当前状态做差异，经 gRPC `AddInbound`/`RemoveInbound` **热更新，不重启进程**；节点程序或 Core 重启后按落盘状态恢复。停用/超额/到期 = 从 Core 移除该入站，期望状态里标记，便于恢复。
- 连接限制：经 Core 控制 socket 下发，字段与 Helper 一致（参考 `/root/projects/mmwx-custom/cmd/mmwxc-helper/core_client.go`、`models.go` 的 `connectionSettings`：`global_total_limit_enabled`/`max_global_total_connections` 对应服务器总连接数；`ports[{inbound_tag,max_total_connections,max_inbound_online_ips}]` 对应节点限制）。
- 流量：StatsService 按入站读累计上下行（不 reset），连同 Core epoch（进程启动标识）随现有流量上报一起上报；面板按"累计 − 上次"算增量，epoch 变化视为从 0 开始；入账走现有原子批次（`repo.FlowBatch` / `ApplyFlowBatch`），重复上报不重复计。
- 入站模板（照妙妙屋）：
  - SS2022：`protocol=shadowsocks`，`method=2022-blake3-aes-128-gcm`，服务器 `password`=随机 16 字节 base64，`clients=[{password: 随机 16 字节 base64, email: "flvxl-<id>"}]`，`network="tcp,udp"`，sniffing `{enabled, destOverride:[http,tls], routeOnly:true}`。
  - VLESS Reality Vision：`decryption=none`，`clients=[{id: uuid, email: "flvxl-<id>", flow: "xtls-rprx-vision"}]`，`streamSettings={network: tcp, security: reality, realitySettings: {dest: "www.lovelive-anime.jp:443"（服务器可改）, serverNames: ["www.lovelive-anime.jp"], privateKey: x25519, shortIds: [""]}}`。
- 节点 36 拒绝启用落地（硬编码保护 + 测试）。

### E. 链接

- SS2022：按 SIP002 对 2022 加密的规定（userinfo 不做 base64，百分号编码 `method:serverKey:userKey`），和妙妙屋/官方订阅生成的 SS2022 链接格式对照一致。
- VLESS：`vless://<uuid>@<host>:<port>?encryption=none&flow=xtls-rprx-vision&security=reality&sni=<sni>&fp=chrome&pbk=<publicKey>&sid=<shortId>&type=tcp#<name>`。
- 直连链接用落地服务器 public_host + 节点端口；"经规则转发"版本把 host:port 换成规则的入口地址（隧道入口地址/入口节点）+ 规则入口端口，每个入口一条。
- 第一版提供：复制链接、二维码。订阅链接/订阅目录以后做。

### F. 前端（只加不改）

- 新侧边栏「落地」（管理员和用户都可见，内容不同），H5 同样加入口：
  - 管理员：「落地服务器」（卡片：来源、在线、计费标签、流量/连接/节点数；设置抽屉：计费、服务器总连接数、每人节点上限、端口段、新节点默认值、协议模板、可见用户组、可用期限、资源上限）、「全部节点」（表格 + 节点详情：每项"设置 / 实际生效 / 来源"）。
  - 用户：「可用落地」（卡片 + 创建节点弹窗：名称、协议模板，端口自动）、「我的节点」（状态、`已用 / 上限`、连接数、到期、直连链接、经规则链接、二维码、用于新规则、删除）。
- 「节点」页每个节点加"启用为落地"操作（新增按钮/菜单项，不改原布局）；节点编辑里新增"每个用户默认限额"。
- 用户编辑弹窗新增「落地」区块：可建节点总数、服务器流量限额列表（服务器 / GB / 已用）。
- 规则弹窗目标处新增选项："我的落地节点" / "落地服务器（自动建节点）"，默认仍是原来的手动填写，原有输入框和布局不变。
- 主页：**原有流量显示不改**；只允许另加一个独立的小卡片显示"落地流量"或"其中 规则 / 落地"。
- 旧 sing-box"部署"入口隐藏（节点页按钮）；TLS 模板页如只服务于旧部署功能则隐藏菜单（只隐藏，不删代码）。
- 截图（电脑 + H5）：所有新页面/弹窗/区块，以及主页、规则弹窗默认状态、节点页、用户编辑弹窗的前后对比，证明原有部分未变。

### G. 安全

- 只有授权用户组能看到/使用落地服务器；后端对每个接口做权限校验（普通用户只能操作自己的节点）。
- 端口只在服务器端口段内分配，不与该节点规则端口段、已有落地节点、隧道监听端口冲突。
- 路由强制阻断内网/回环/元数据目的地址（D），任何配置不得去掉。
- 共享服务器上 FLVX 只是"租户"：所有约束由妙妙屋 Helper 独立校验，不信任 FLVX 输入（见 H）。
- 日志不打印凭据（沿用 plan 096 的 logsafe 约定）。

### H. FLVX ↔ 妙妙屋 Custom 接口 v1（契约，第二期双方都按此实现）

- 基础：`https://mmwxc.imgamer.top/api/custom/flvx/v1`，`Authorization: Bearer <FLVX 接入令牌>`（妙妙屋「FLVX 共享」页生成，只显示一次，Custom 只存哈希）；JSON；错误 `{"error":{"code","message"}}`。只有 FLVX → Custom 一个方向，Custom 不回调 FLVX。
- `GET /shares` → `{shares:[{server_id, name, hosts:{ipv4, ipv6, domain}, status: active|paused|revoked, port_min, port_max, traffic_limit_bytes, traffic_used_bytes, reset_day, max_connections, connections, share_until(ms|null), core:{state: running|stopped|installing|error, version}, protocols:["ss2022","vless_reality_vision"], dual_install: bool}]}`
- `PUT /shares/{server_id}/desired` → 请求 `{revision(单调递增), nodes:[{id(FLVX 落地节点 id 字符串), owner:{id,name}, name, protocol, port, enabled, max_connections, max_online_ips, expires_at(ms|null), ss2022:{method, server_key, user_key}, vless:{uuid, flow, reality:{dest, server_names[], private_key, short_ids[]}}}]}`；全量、幂等；Custom 校验（端口在共享端口段且不与官方入站冲突、协议在允许列表、无重复）后由 Helper 差异热更新。成功 `200 {accepted_revision}`，校验失败 `422 {errors:[{node_id, field, message}]}`。
- `GET /shares/{server_id}/status` → `{applied_revision, core:{state, epoch}, nodes:[{id, state: active|error|removed, error?, up_bytes, down_bytes(自节点首次应用起的累计值，Helper 持久化，Core 重启不归零), connections}], share_traffic_used_bytes, collected_at}`
- `PUT /landing-hosts` → `{native_landing_hosts:[...], flvx_node_hosts:[...]}`：FLVX 报告自己的节点地址（含域名解析结果），Custom 据此在共享时提示（两边都装）或禁止（FLVX 已在该机启用落地）。
- 轮询：FLVX 每 60 秒拉 status；期望状态变化即推送，另每 10 分钟全量重推一次防漂移。
- 计数：累计、单调；变小视为重置（从 0 起算）。

## 分期与清单

### 第一期：FLVX 自有落地（任务 `landing-native`，FLVX 仓库）

- [x] 读本计划，核对基线 fork.52、worktree 与分支；记录全量测试失败基线。
- [ ] 数据模型与迁移（SQLite + PostgreSQL），节点 36 保护。
- [ ] 后端接口：管理员（启用为落地、服务器设置、全部节点、节点覆盖、用户落地设置、服务器流量限额、节点默认限额）、用户（可用落地、建/删节点、我的节点、链接）；权限校验。
- [ ] 计费与服务器流量限额接入流量入账（含路径归属、轮询均分），超额暂停/恢复。
- [ ] 有效期：跟随用户/固定，到期停用、7 天宽限、自动删除、续期恢复；与用户到期/禁用/总流量用完联动。
- [ ] 规则：目标可选我的落地节点 / 落地服务器（自动建节点）；混合目标校验；手填匹配。
- [ ] 节点程序：Core 下载校验安装、systemd 单元、期望状态热更新、连接限制、流量上报、重启恢复。
- [ ] 前端（只加不改）+ H5；隐藏旧部署入口。
- [ ] 单元 / 契约测试（SQLite + PG），全量 `go test` 失败集合不超过基线。
- [ ] 本机端到端（真实 mmwxc-core + 客户端）：SS2022 与 VLESS Reality Vision 直连与经规则、计数与计费、限额暂停/恢复、连接限制、热添加不断已有连接、到期与恢复。
- [ ] 截图集（电脑 + H5，含原有页面前后对比）。
- [ ] 推送分支（不打 tag、不发布、不动生产/节点），中文总结。

### 第二期 A：妙妙屋 Custom（任务 `flvx-share`，mmwx-custom 仓库）

- [ ] Custom：共享配置、接入令牌、接口 v1（H）、双装检测。
- [ ] Helper：第二个 Core 实例（`mmwxc-flvx-core`，名字不含 xray，低权限，资源上限），校验并热更新期望状态，持久化累计计数，连接/总流量上限执行，暂停/取消；不影响妙妙屋自身 Core（SingleCore/接管/漂移检测不受影响）。
- [ ] UI：左侧「FLVX 共享」页 + 服务器卡片小标签（只加不改）。
- [ ] 测试 + 截图；不发布。

### 第二期 B：FLVX 接入共享服务器（任务 `landing-shared`，FLVX 仓库，依赖第一期和第二期 A）

- [ ] 管理员配置妙妙屋地址和接入令牌；同步共享服务器为 landing_server（source=shared），显示妙妙屋上限（只读）。
- [ ] 在共享服务器上建节点 → 推期望状态；拉 status → 同一套计费 / 服务器限额 / 有效期；报告 landing-hosts。
- [ ] 测试（对接一个本地 Custom 或契约假服务）+ 截图；不发布。

### 发布与金丝雀（用户批准截图之后，另派任务）

- [ ] Custom 发布 + 仅服务器 7（US 年付）升级 Helper 并开启共享；FLVX 发布 fork.53；仅节点 21（JP）OTA 并启用落地；清理节点 21、25 的旧 sing-box 闲置入站。
- [ ] 两台各建 SS2022 与 VLESS Reality Vision 测试节点，真实连通、计费、限额、到期、热添加不断线；妙妙屋自身用户与 External Core 不受影响；节点 36 未启用。

### 第三期（以后）

- [ ] 出站 / 路由模板：WARP、走 FLVX 隧道、分流规则。
- [ ] 订阅目录、套餐（用户以后决定）。

## 核验记录

2026-10-18：工作树、开发分支及基线 `bbfa2996`（代码基线 `3143042f`）确认。修改前启动 `GOMAXPROCS=2 go test -p 1 ./...`，失败集合 15 项，完整日志与清单：`/root/flvx-workers/runs/landing-native/baseline.log`、`baseline-failures.json`。全过程只开发、不发布；重型作业串行，临时文件仅使用 `/root/tmp-work/landing-native/`。
