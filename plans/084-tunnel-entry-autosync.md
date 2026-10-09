# 084 — 隧道编辑入口地址自动同步

基线：`35a70493`（fork.34）；分支 `maintenance/3.0.27-fork.35-tunnel-entry-autosync`。仅编辑入口集合变化时按增删节点更新地址，保留自定义地址；无 schema、回填、agent 或依赖变更。

- [x] 阅读项目规则、发布记忆，确认现有入口收敛路径和范围，创建分支。
- [x] 实现后端地址增量同步及保留节点自定义端口地址，补仓储和 handler 回归测试。
- [x] 编辑表单同步入口地址，创建模式和其他黄色同步提示不变。
- [x] 后端 build/vet/全量测试基线 15/race；前端 build/lint 基线 96/包体积门禁。
- [x] 生产在线副本 old/new 无编辑零差异、入口不变保存地址字节一致；核查 diff 范围。
- [x] 推送分支，HEAD CI 通过，发布 fork.35 并验证 Latest/资产/固定版本。
- [x] 生产备份、裁剪保留两个、升级面板，验证健康/指标及历史地址端口零差异。
- [ ] 生产临时用户/隧道/规则增删入口与 diagnose、只读浏览器截图，清理及计数/引用验证。
- [ ] 中文总结、最终计划提交及推送。

证据及临时目录：`/root/flvx-workers/runs/tunnel-entry-autosync/`；所有重任务串行且开始前 MemAvailable ≥750 MiB。

发布前门禁：build/vet 通过；全量 Go 与 fork.34 同为 15 项失败，new/removed=[]；handler race 无 DATA RACE，仅 2 项既有断言失败。前端 build 通过，lint 96→96，无新增，主 JS 2,742,702 B。隔离网络运行生产在线副本：53 条隧道地址、25 条规则显示地址、25 行 forward_port、10 用户可见数据零差异；隧道 78 不改入口保存 in_ip 字节一致。额外共享地址保留测试通过。

发布：HEAD `9bbc4d55` 的 CI Build Check `37912692105` 全绿；注释标签 `3.0.27-fork.35`，Build and Push Images `37912930106` 全绿。Release 于 2026-10-09 09:59:35Z 发布为 Latest、非预发布；10 项资产与 fork.34 相同，v4/v6 镜像及两安装脚本版本固定、amd64/arm64 SHA256 验证通过。

生产升级：备份 `/opt/flvx-svc/rollback/pre-fork35-20261009T100049Z/`，quick_check=ok、63 表计数及旧镜像标签/回滚说明齐全。保留 pre-fork34/pre-fork35，删除 pre-fork33 及 fork.32 旧镜像。安装 v6 compose、FLUX_VERSION=fork.35，backend healthy、frontend 正常；升级前后历史隧道地址、规则地址、端口、用户可见数据及 schema 零差异，节点指标继续推进。未升级任何 agent。
