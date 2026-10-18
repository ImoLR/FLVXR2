# 103 fork.53 发布与节点 21 原生落地金丝雀

基线：feature/3.0.27-landing-nodes `6b0527de`；用户 2026-10-18 已批准截图及发布。执行分支 maintenance/3.0.27-fork.53-landing-nodes，沿用当前工作树。证据目录 `/root/flvx-workers/runs/landing-release/`。

仅发布计划 101 第一期和第二期 B；生产面板升级、节点 21 OTA/原生落地及金丝雀、节点 21/25 旧入站清理。节点 25 仅 API 清理；节点 35/36、Custom 发布/接入及其他栈不操作。不给真实用户组授权。不修改 memory 或安装脚本。

- [x] 阅读流程、计划和前序总结；核对干净 feature head，创建发布分支。
- [x] 修复门禁发现的额度协调重复写入：仅周期/继承上限/暂停状态变化时更新，新增回归测试。
- [x] 发布门禁：全量 Go 失败集合不超过既有 15，前端构建，生产一致性副本增量迁移及原表校验，流量上传 reconcile 成本实测。
- [x] 发布 fork.53，CI/Images 通过，核验与 fork.52 相同的 10 项资产。
- [ ] 完整 SQLite、compose/.env、旧镜像标签及 rollback.sh 备份；执行两份保留裁剪。
- [ ] 生产升级、健康/节点上报核验，升级前后各约 10 分钟 CPU/SQLite 写入观察。
- [ ] 节点 21 诊断及 agent/mmwx 基线备份；仅节点 21 OTA，能力及诊断复验。
- [ ] 核对全机监听/FLVX 端口，分离规则与落地段；启用 admin-only 原生落地，核验固定 Core/低权限/资源上限及 mmwx 不变。
- [ ] 备份旧入站 8/2 和节点 21 sing-box；API 删除并 apply，节点 21 停用旧服务，外部端口关闭核验。
- [ ] 临时用户双协议 ≥20 MB 直连、规则 max(T,L)、热更新、连接限制、私网阻断、单击删除与最终计数金丝雀。
- [ ] 清理临时对象/进程/副本；计划 101 FLVX 条目证据、维护/功能分支同步及中文总结。

## 核验记录

启动：HEAD `6b0527de93a2a4a7f88faed8b110063f2e4809cf`，工作树干净；生产镜像 fork.52，数据库卷精确为 `flvx_sqlite_data`。所有重型作业串行，GOMAXPROCS=2，前端 Node 堆 768 MiB，scratch 位于 `/root/tmp-work/landing-release/`。

发布前原始全量失败恰为同一 15 项，前端构建通过。完整生产 SQLite 副本迁移及隔离网络 16365 启动通过；66 个原有业务表行数及原有列 SHA256 全部一致，新增 6 表，node 新增 landing_capable/default_user_quota_bytes，forward 新增 landing_node_id/landing_auto_paused，无删列。sqlite_sequence 仅新增新表序列项，原序列一致。

性能门禁：11 用户/31 规则，按实际路径生成 31 条服务器额度，连续 200 次 reconcile 原中位 16.168 ms / P95 19.244 ms（每次全部额度都 INSERT+UPDATE）；最小修复后中位 0.957 ms / P95 1.828 ms，稳定额度零写入。回归测试在旧源码 overlay 上如期失败；SQLite/PG 落地定向全部通过（repo 29.703s、handler 6.121s、contract 21.671s），含继承上限变更、显式覆盖、周期归零及暂停恢复。证据 reconcile-*.log、landing-focused.log。最终全量复验（含 PG）仍恰为既有 15 项失败，新增/消失均 0，见 backend-final-comparison.json；代码 CI https://github.com/ImoLR/FLVXR2/actions/runs/39131769765 四项全部通过。

升级前 600.07 秒监测：backend cgroup CPU 平均 3.328%（单核=100%），backend /proc write_bytes 239,171 B/s，WAL 120/120 个 5 秒采样间隔发生写入。该 I/O 指标包含进程全部磁盘写入，作为 SQLite 写负载代理，并非 SQL 语句数。证据 cpu-before.json。

只读现场差异：初始节点 28/29 在线，节点 22 原已离线。节点 21 官方 mmw-agent 直接监听 60061/60062；无 /etc/flvxt2、无独立 xray 进程，Helper v0.7.4 active、无接管完成记录。不得为满足旧端口关闭或 xray=1 的预期而操作 mmwx；以实际前后不变作证并在总结明确未满足项。

发布：annotated tag `3.0.27-fork.53` → `814917d6c254969d95bba454822d250e6436bb57`；CI https://github.com/ImoLR/FLVXR2/actions/runs/39132085593、Images https://github.com/ImoLR/FLVXR2/actions/runs/39132087254 全绿。正式 Latest Release 10 项资产与 fork.52 同名；两架构 gost SHA256、compose 镜像 tag、两个脚本固定版本/仓库、离线 ZIP 完整性均通过（release-validation.json）。未修改安装脚本；离线包 offline.sh 沿用 fork.52 原内容。

节点 21 前置证据：6 条隧道 23/26/32/33/43/66 共 27 项诊断全通过（diagnoses-before-ota.json）。SSH agent 回滚点 `/root/flvxx-backup-20261018T134521Z-pre-fork53`，含二进制/配置/unit/restore-agent.sh；旧 sing-box 目录及单元原不存在，记录于 node21-backup.json。
