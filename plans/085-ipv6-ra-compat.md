# 085 — IPv6 RA 转发兼容与节点状态

基线：`a73c864550740f1e6beaee719fada84ad6a251c2`（fork.35 收尾已推送，生产 healthy）；分支 `maintenance/3.0.27-fork.36-ipv6-ra-compat`。仅修复 agent 开启 IPv6 forwarding 时的 RA 兼容，附加管理员节点状态与三角提示；不改转发规则、出口探测、安装脚本或 CI。

- [x] 阅读规则与发布记忆，验证 fork.35 前置条件并创建分支。
- [x] Agent：accept_ra 1→2、RA 探测/分级/上报、非 Linux 空实现与单元测试。
- [x] 面板：附加字段、change-only 持久化、管理员列表与保存保留测试。
- [x] 前端：节点卡片/列表三色 TriangleAlert、详情与时间。
- [x] netns E2E：旧行为过期、新行为保持超过两周期、accept_ra=0 错误。
- [x] Go build/vet/测试/race/双架构；后端全量失败集对照 fork.35。
- [x] 生产在线副本迁移/列表差异门禁；前端 build/tsc/lint 基线与桌面/手机截图。
- [x] 范围复核、推送、HEAD CI 全绿，发布 fork.36 并验证资产。
- [x] 备份与保留两个、升级生产面板、健康/指标/数据不变验收。
- [x] 仅节点 48：升级前版本/隧道/规则诊断，OTA fork.36、RA 状态与升级后诊断（必要时回滚）。
- [x] 清理自启进程/临时副本，中文总结，收尾提交并推送。

证据目录：`/root/flvx-workers/runs/ipv6-ra-compat/`；TMPDIR/GOTMPDIR 使用其 `t/`。所有重任务前检查 MemAvailable ≥750 MiB，单任务串行；本机真实网络命名空间不执行网络/sysctl 变更。

Agent 定向单测通过（accept_ra 假 proc、分级/30分钟/写失败、上报省略）；完整构建/race/netns 在后续门禁执行。

面板定向单测通过：旧 agent 默认空、状态/详情 change-only、重连不改时间、管理员可见/广播隐藏、节点保存不清空。

netns E2E 通过：RA 每10秒发送、前缀/路由寿命60秒；旧 forwarding=1/accept_ra=1 在75秒时地址/默认路由均过期；真实 enableIPForwarding 后 accept_ra=2，warn→ok，保持140秒；accept_ra=0 + 动态 ULA/RA 路由报 error。临时 namespace/veth 已删除；第一次仅 RA 发送器绑定等待的夹具失败，改用 nodad 后完整通过。证据 netns-e2e.log / netns-result.json。内核语义依据：https://docs.kernel.org/networking/ip-sysctl.html#conf-interface。

回滚限制核实：现有 currentPanelAgentVersion 拒绝与面板不同的请求版本（任务说明中的“接受 version 即可回退”不成立）；未修改升级逻辑，收尾记录此限制。

Go 门禁通过：go-gost/go-gost-x build、socket/nftables vet/test/race、CGO=0 Linux amd64/arm64 与 Darwin 空实现编译；后端 build/vet、全量 go test 与 fork.35 均为同一15项既有失败，new/removed=[]。节点48在本任务尚未OTA时先离线后恢复，实时版本已变为 fork.35，升级前以实时快照记录。

副本门禁：隔离 netns 内旧/新 paneld 迁移生产在线副本，仅3个 node.ipv6_ra_* 新列，全部默认空/0；63表计数一致，26节点列表除此之外零差异；副本已删除。前端 build/tsc 通过，lint 96→96 无新增，主 JS 2,744,378 B <5MiB。只读 Playwright 在650MiB scope 内完成16张桌面/手机(H5)卡片/列表截图（绿/黄/红与空值），均为16px三角，详情与时间可点击查看；写 API 拦截。screens/ 与 screenshots.json 留证。范围复核仅19个任务相关文件含计划085。

发布（第2轮，Claude）：tag `3.0.27-fork.36` → 66abb842；CI Build Check 37918825994 成功；Build and Push Images 37919037787 全绿（GOST/后端/前端/Create Release 成功，Update GOST Binaries 与 fork.35 一样 skipped）。Release 2026-10-09T10:57:55Z 为 Latest、非 prerelease；资产集与 fork.35 完全相同（v4/v6 compose、gost-amd64/arm64 + sha256、install.sh、panel_install.sh、offline-amd64/arm64.zip 内含 offline.sh+flvx_agent）；compose 镜像 `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.36` 可拉取；两脚本 `PINNED_VERSION="3.0.27-fork.36"`、`REPO="ImoLR/FLVXR2"`；gost sha256 校验通过（amd64 4c52d1d2…）。

生产：回滚点 `/opt/flvx-svc/rollback/pre-fork36-20261009T105912Z/`（compose/.env、在线 `gost.db.validated` quick_check=ok、表计数、`local/flvxx-*:pre-fork36-20261009T105912Z` = fork.35 镜像、ROLLBACK-METADATA.md）；prune 后保留 pre-fork36 + pre-fork35（删 pre-fork34 及 fork.33 镜像）。10:59:20Z 换 v6 compose、FLUX_VERSION=3.0.27-fork.36、仅 pull/up backend+frontend；healthy、restarts=0、前端 200；node 新增 ipv6_ra_status/detail/checked_at 三列，26 节点均为 ''/''/0；节点/隧道/规则配置表零差异，列表 26/53/25；表计数与备份一致（仅 node_metric、tunnel_metric 自然增长）；25 个在线节点 metric 前进（24 长期离线）；重连 redeploy 全部 failed=0。

节点 48：升级前 agent `3.0.27-fork.35 (debian/amd64)` 在线，隧道 75/78，规则 110/113；节点上备份 `/root/flvxx-backup-20261009T110101Z/`（flvxx 二进制 sha256 64af66d9…、flvxx.service、config.json、gost.json）。11:01:16Z OTA `{"id":48}` → 7.6 秒内以 `3.0.27-fork.36 (debian/amd64)` 重连（二进制 sha256 = 发布 gost-amd64）。RA：`ipv6RaStatus=ok`，`ipv6RaDetail=eth0 accept_ra=2（原本即为 2）`，`ipv6RaCheckedAt=1791543681123`（11:01:21Z）。主机只读：all.forwarding=1，all/default/eth0 accept_ra=2（lo=1），全局 IPv6 2408:820c:750b:dc64:…:2813/64 + 240e:b8f:70f:ab04:…:2813/64，默认路由 via fe80::be24:11ff:fefe:1f92 proto ra；journal 无「IPv6 RA 兼容」行（仅 1→2 时打印，本次原本即为 2）；unit 未改（ExecStart 无 -C），flux_agent.service 未动。诊断：110/113 前后均 TCP 连接成功（~36 ms，0 丢包），无回退；其他节点版本未变。注：面板重启后约 1 分钟时的一次 110 诊断为「等待节点响应超时」（重连 redeploy 期间的瞬时现象），OTA 前复测已成功。证据 `/root/flvx-workers/runs/ipv6-ra-compat-r2/`。
