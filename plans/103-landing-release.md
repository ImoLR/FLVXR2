# 103 fork.53 发布与节点 21 原生落地金丝雀

基线：feature/3.0.27-landing-nodes `6b0527de`；用户 2026-10-18 已批准截图及发布。执行分支 maintenance/3.0.27-fork.53-landing-nodes，沿用当前工作树。证据目录 `/root/flvx-workers/runs/landing-release/`。

仅发布计划 101 第一期和第二期 B；生产面板升级、节点 21 OTA/原生落地及金丝雀、节点 21/25 旧入站清理。节点 25 仅 API 清理；节点 35/36、Custom 发布/接入及其他栈不操作。不给真实用户组授权。不修改 memory 或安装脚本。

- [x] 阅读流程、计划和前序总结；核对干净 feature head，创建发布分支。
- [ ] 发布门禁：全量 Go 失败集合不超过既有 15，前端构建，生产一致性副本增量迁移及原表校验，流量上传 reconcile 成本实测。
- [ ] 发布 fork.53，CI/Images 通过，核验与 fork.52 相同的 10 项资产。
- [ ] 完整 SQLite、compose/.env、旧镜像标签及 rollback.sh 备份；执行两份保留裁剪。
- [ ] 生产升级、健康/节点上报核验，升级前后各约 10 分钟 CPU/SQLite 写入观察。
- [ ] 节点 21 诊断及 agent/mmwx 基线备份；仅节点 21 OTA，能力及诊断复验。
- [ ] 核对全机监听/FLVX 端口，分离规则与落地段；启用 admin-only 原生落地，核验固定 Core/低权限/资源上限及 mmwx 不变。
- [ ] 备份旧入站 8/2 和节点 21 sing-box；API 删除并 apply，节点 21 停用旧服务，外部端口关闭核验。
- [ ] 临时用户双协议 ≥20 MB 直连、规则 max(T,L)、热更新、连接限制、私网阻断、单击删除与最终计数金丝雀。
- [ ] 清理临时对象/进程/副本；计划 101 FLVX 条目证据、维护/功能分支同步及中文总结。

## 核验记录

启动：HEAD `6b0527de93a2a4a7f88faed8b110063f2e4809cf`，工作树干净；生产镜像 fork.52，数据库卷精确为 `flvx_sqlite_data`。所有重型作业串行，GOMAXPROCS=2，前端 Node 堆 768 MiB，scratch 位于 `/root/tmp-work/landing-release/`。
