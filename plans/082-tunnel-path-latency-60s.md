# 082 — 隧道全路径延迟（60 秒，RST 关闭）

基线：`58909738`；发布：`3.0.27-fork.34`。按 2026-10-09 已批准设计实现；仅测入口→中继组→出口，不包含出口→公网，不恢复旧质量探测器。结果仅在内存保存，依赖 083 的入口权限过滤。

- [x] 阅读项目规则、发布记忆和 fork.33 失败基线，建立专用分支。
- [x] 实现每 60 秒后台轮次、在线节点对去重、并发上限、失败对 10 秒后仅重试一次、分层最短完整路径与过期快照。
- [x] 实现登录用户可用的延迟路由及完整路由真实 JWT 权限测试，非管理员仅获得允许入口的数值。
- [x] 规则对话框开关控制 60 秒轮询；分组选择器可选延迟行和关闭态显示。
- [x] agent 仅修改 tcpPingHost 成功连接 SetLinger(0)，添加 Linux reset/TIME_WAIT 测试。
- [ ] 后端 build/vet/全量测试/handler race、agent build/socket 测试、前端 build/tsc/lint 对照基线；检查范围和 5 MiB 限额。
- [x] netns E2E：约 50 ms、失败单次重试、恢复、RST 无 TIME_WAIT，保存原始数字。
- [ ] 检视桌面明暗、390 手机、用户 3、多入口及分组勾选截图。
- [ ] 分支推送及 HEAD CI 通过；记录至少 30 分钟 fork.33 tcp_conns 基线。
- [ ] 注释标签和发布资产验证、生产备份及保留两份、升级面板、仅 node 47 OTA。
- [ ] 生产两轮延迟/用户 3 权限/日志/JS/30 分钟后 tcp_conns 验证，中文总结并提交部署完成。

门禁新增失败即停止发布，提交并推送 WIP。证据和临时文件：`/root/flvx-workers/runs/entry-groups-latency/`（临时文件仅 `t/`）。禁止更改 tcp_conns、监控页、最优逻辑、安装脚本、依赖和其他 agent 代码。

## WIP 门禁停止（2026-10-09）

新增功能定向测试、完整路由 JWT 测试、后端 build/vet、前端 build 均通过；lint 与 fork.33 同为 96 个错误，无新增。全量 Go 测试为 26 项失败（含父/子测试），原基线 15 项 + 新增 11 项。按任务硬门禁停止后续验证、标签、发布、生产升级和 OTA，保留 WIP 分支。入口权限实现及现有契约兼容性尚未完成。

新增失败与原始日志：`/root/flvx-workers/runs/entry-groups-latency/baseline-comparison.json`、`new-failures.log`；完整状态见该目录 `summary.md`。生产保持 fork.33，未执行生产回填。

第二轮 netns E2E：真实 fork.34 agent，20+30 ms → 50.672932 ms；暂停中继后一次重试（命令起点间隔 13.997559 s，扣除 4 s 超时为 9.997559 s），整路径 timeout，终止/重启后 50.563708 ms。入口/中继侧探测目标 TIME_WAIT=0；所有进程/netns 已清理。r2/e2e-results.json 和原始命令/ss 日志留存。
