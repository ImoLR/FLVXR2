# 075 — Agent 出站探测与逐跳自定义连接 IP

范围：按任务约定实现 additive 出站探测、逐跳 literal IP 覆盖，发布 `3.0.27-fork.27` 并升级生产面板；仅节点 47 做 agent OTA canary。不改生产节点/隧道配置，不主动重新部署生产隧道。

运行目录：`/root/flvx-workers/runs/egress-detect-connect-ip/`；TMPDIR/GOTMPDIR 使用其 `t/`，重型进程串行。

- [x] 阅读 plan 074、仓库规则与发布记忆，从 `7af40d9b` 建立指定分支。
- [x] Agent TCP v4/v6 探测、异步缓存与 SystemInfo 字段；注入 dialer 单测、相关包测试和构建。
- [x] Panel 探测列、change-only 更新、API/备份与 additive 有效出站；相关单测。
- [x] ConnectIP 运行时共享选择、诊断/探测/best-exit、校验与更新清空；修复两项既有失败。
- [x] 节点探测展示、隧道自定义 IP 输入与读写；类型检查及生产构建。
- [x] 在线生产副本：fork.26 与新逻辑全部相邻跳对比为 0；记录假设全部 detected dual 的变化。
- [ ] 本地隔离 paneld:16365 + 遥测 harness + 配置/诊断验证；1440/390 节点与隧道表单截图。
- [ ] 后端全量测试：基线仅减少指定两项及其子测试；CI Build Check 成功。
- [ ] 注解标签、Build and Push Images 成功；Latest/资产集/compose/安装脚本/sha256 校验。
- [ ] 生产回滚点与备份保留清理；升级 backend/frontend。
- [ ] 生产只读健康/指标/列默认/API 权限/JS 验证。
- [ ] 仅节点 47 OTA；确认在线、指标、forward 102 诊断与探测值，或记录失败及回滚。
- [ ] 中文总结、清理本任务大型临时文件；提交完成计划并推送。

## 行为与应用路径

自动出站 = 入口地址族 ∪ 最新非空探测族；手动 v4/v6/dual 优先。探测不移除入口能力，不触发自动重新部署。现有隧道保存/重新部署、节点重连的节点范围配置下发会重读节点记录并使用新结果；诊断及质量/路径探测也读取节点记录。显式逐跳 v4/v6 与 lan 语义保持 fork.26。

自定义连接 IP 覆盖目标地址选择，目标端口保持原值；仅单节点中继跳或单出口可填写，必须为 literal IPv4/IPv6。共享选择函数供链配置、两类诊断、质量/路径探测及 best-exit 使用。

## 验证与发布记录

实施时逐项补充。

- Agent `go test -p 1 ./socket` 与 `go build -p 1 .` 通过；覆盖 v4/v6/dual/unknown、逐目标 fallback、3s deadline、上报省略与取消。

- 后端定向测试通过：additive/manual/no-detection 矩阵、API/备份往返、重复上报与重连仅 change-only 更新、literal IP/多节点校验、创建/更新/清空、自定义链配置/探测/best-exit。两个原基线测试及普通/流式诊断子测试均通过。

- 前端 `tsc --noEmit` 与 `npm run build` 通过，主 JS 2,753,064 B，PWA 5 MiB 余量 2,489,816 B。

- 在线生产副本：quick_check=ok；28 节点、53 隧道、169 chain_tunnel 行，139 个相邻跳组合。提取 fork.26/当前 Go 函数比较，无检测数据差异 **0**；全部 detected=dual 的假设变化也为 **0**。生产非空 connect_ip 为 0。详见 gate-input.json / gate-result.json / dial_gate.go。
