# 085 — IPv6 RA 转发兼容与节点状态

基线：`a73c864550740f1e6beaee719fada84ad6a251c2`（fork.35 收尾已推送，生产 healthy）；分支 `maintenance/3.0.27-fork.36-ipv6-ra-compat`。仅修复 agent 开启 IPv6 forwarding 时的 RA 兼容，附加管理员节点状态与三角提示；不改转发规则、出口探测、安装脚本或 CI。

- [x] 阅读规则与发布记忆，验证 fork.35 前置条件并创建分支。
- [ ] Agent：accept_ra 1→2、RA 探测/分级/上报、非 Linux 空实现与单元测试。
- [ ] 面板：附加字段、change-only 持久化、管理员列表与保存保留测试。
- [ ] 前端：节点卡片/列表三色 TriangleAlert、详情与时间。
- [ ] netns E2E：旧行为过期、新行为保持超过两周期、accept_ra=0 错误。
- [ ] Go build/vet/测试/race/双架构；后端全量失败集对照 fork.35。
- [ ] 生产在线副本迁移/列表差异门禁；前端 build/tsc/lint 基线与桌面/手机截图。
- [ ] 范围复核、推送、HEAD CI 全绿，发布 fork.36 并验证资产。
- [ ] 备份与保留两个、升级生产面板、健康/指标/数据不变验收。
- [ ] 仅节点 48：升级前版本/隧道/规则诊断，OTA fork.36、RA 状态与升级后诊断（必要时回滚）。
- [ ] 清理自启进程/临时副本，中文总结，收尾提交并推送。

证据目录：`/root/flvx-workers/runs/ipv6-ra-compat/`；TMPDIR/GOTMPDIR 使用其 `t/`。所有重任务前检查 MemAvailable ≥750 MiB，单任务串行；本机真实网络命名空间不执行网络/sysctl 变更。
