# 085 — IPv6 RA 转发兼容与节点状态

基线：`a73c864550740f1e6beaee719fada84ad6a251c2`（fork.35 收尾已推送，生产 healthy）；分支 `maintenance/3.0.27-fork.36-ipv6-ra-compat`。仅修复 agent 开启 IPv6 forwarding 时的 RA 兼容，附加管理员节点状态与三角提示；不改转发规则、出口探测、安装脚本或 CI。

- [x] 阅读规则与发布记忆，验证 fork.35 前置条件并创建分支。
- [x] Agent：accept_ra 1→2、RA 探测/分级/上报、非 Linux 空实现与单元测试。
- [x] 面板：附加字段、change-only 持久化、管理员列表与保存保留测试。
- [x] 前端：节点卡片/列表三色 TriangleAlert、详情与时间。
- [x] netns E2E：旧行为过期、新行为保持超过两周期、accept_ra=0 错误。
- [x] Go build/vet/测试/race/双架构；后端全量失败集对照 fork.35。
- [x] 生产在线副本迁移/列表差异门禁；前端 build/tsc/lint 基线与桌面/手机截图。
- [ ] 范围复核、推送、HEAD CI 全绿，发布 fork.36 并验证资产。
- [ ] 备份与保留两个、升级生产面板、健康/指标/数据不变验收。
- [ ] 仅节点 48：升级前版本/隧道/规则诊断，OTA fork.36、RA 状态与升级后诊断（必要时回滚）。
- [ ] 清理自启进程/临时副本，中文总结，收尾提交并推送。

证据目录：`/root/flvx-workers/runs/ipv6-ra-compat/`；TMPDIR/GOTMPDIR 使用其 `t/`。所有重任务前检查 MemAvailable ≥750 MiB，单任务串行；本机真实网络命名空间不执行网络/sysctl 变更。

Agent 定向单测通过（accept_ra 假 proc、分级/30分钟/写失败、上报省略）；完整构建/race/netns 在后续门禁执行。

面板定向单测通过：旧 agent 默认空、状态/详情 change-only、重连不改时间、管理员可见/广播隐藏、节点保存不清空。

netns E2E 通过：RA 每10秒发送、前缀/路由寿命60秒；旧 forwarding=1/accept_ra=1 在75秒时地址/默认路由均过期；真实 enableIPForwarding 后 accept_ra=2，warn→ok，保持140秒；accept_ra=0 + 动态 ULA/RA 路由报 error。临时 namespace/veth 已删除；第一次仅 RA 发送器绑定等待的夹具失败，改用 nodad 后完整通过。证据 netns-e2e.log / netns-result.json。内核语义依据：https://docs.kernel.org/networking/ip-sysctl.html#conf-interface。

回滚限制核实：现有 currentPanelAgentVersion 拒绝与面板不同的请求版本（任务说明中的“接受 version 即可回退”不成立）；未修改升级逻辑，收尾记录此限制。

Go 门禁通过：go-gost/go-gost-x build、socket/nftables vet/test/race、CGO=0 Linux amd64/arm64 与 Darwin 空实现编译；后端 build/vet、全量 go test 与 fork.35 均为同一15项既有失败，new/removed=[]。节点48在本任务尚未OTA时先离线后恢复，实时版本已变为 fork.35，升级前以实时快照记录。

副本门禁：隔离 netns 内旧/新 paneld 迁移生产在线副本，仅3个 node.ipv6_ra_* 新列，全部默认空/0；63表计数一致，26节点列表除此之外零差异；副本已删除。前端 build/tsc 通过，lint 96→96 无新增，主 JS 2,744,378 B <5MiB。只读 Playwright 在650MiB scope 内完成16张桌面/手机(H5)卡片/列表截图（绿/黄/红与空值），均为16px三角，详情与时间可点击查看；写 API 拦截。screens/ 与 screenshots.json 留证。范围复核仅19个任务相关文件含计划085。
