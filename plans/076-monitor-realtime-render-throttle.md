# 076 监控实时渲染优化与 fork.28 发布

范围：保持每秒实时数据和现有 UI，合并浏览器更新、缓存未变化节点渲染、精简浏览器广播、暂停后台轮询。保持 agent、存储指标和生产节点/隧道配置不变。

- [x] 阅读约束与发布流程，从 fe426246 建立 maintenance/3.0.27-fork.28-monitor-render。
- [x] 实现共享实时批处理、监控卡片/行 memo、可见性及活动页轮询控制；提交。
- [x] 精简广播字段并完成 Go 单测；提交。
- [x] 完成 lint、类型检查、构建、Go 全量失败集合对比；提交结果。
- [ ] 生产在线备份副本 + 本地 16365 面板 + 40 节点，旧新版各四种布局 30 秒实测、每秒更新检查、截图与后台暂停验证；提交结果。
- [x] 推送分支并确认 CI Build Check 绿色。
- [ ] 发布注释标签 3.0.27-fork.28；确认镜像流水线、Latest、资产集合、脚本固定版本/仓库、gost SHA256。
- [ ] 生产升级前备份并保留最近两份；仅升级 backend/frontend。
- [ ] 只读验证生产健康、指标前进、监控/节点/公开页面实时值、非管理员节点列表 403。
- [ ] 完成计划，提交 docs(plan): mark fork28 rollout complete 并推送；写中文总结。

证据目录：`/root/flvx-workers/runs/monitor-render/`，续跑日志 `/root/flvx-workers/runs/monitor-render-r2/`。重任务串行；第二轮按下述受限 scope 门槛执行。任何门禁不通过均不发布。

## 静态与后端验证

- TypeScript 检查和生产构建通过，主 JS 2,754,803 B（5 MiB 上限的 52.5%）。
- 涉及文件 lint：旧版 8 错误，新版 8 错误，零新增；保留原有无障碍问题，未做范围外清理。
- `go test -p 1 ./internal/ws` 通过；`go test -p 1 ./...` 失败集合恰为预期 15 项，零新增/缺失（fork10 的 19 项减 fork27 修复的 4 项含子测试）。

- [x] 复核 memo 对比包含实际节点字段，避免节点列表刷新或另一节点状态变化导致未变化行重绘；构建及 lint 基线再次通过。

## CI 与实测环境

- CI Build Check：`37756259049`（提交 `7ae1c51b`）全部绿色，包含 frontend/backend/agent/PostgreSQL contract。
- 本地隔离网络、在线 SQLite 备份副本、16365 面板已通过 40 模拟节点冒烟；监控及公开指标接口各返回 40 节点。
- 生产预检：28 节点，fork.27 backend healthy/frontend running；非管理员节点列表仍返回业务码 403。公开监控原本关闭（空列表），保持原设置；公开实时值将在本地副本验证。
- Chromium 性能测量必须等到启动前可用内存 ≥900 MB 且无其它 Chromium；未实测通过前不发布。

## 当前阻塞（2026-10-08 UTC）

- 连续等待约 30 分钟，宿主机可用内存约 532–722 MiB，始终未满足 Chromium 启动前 ≥900 MiB 的任务硬约束；未启动 Chromium，未获得性能数字或截图。
- 不停止其它服务、不修改主机内存/交换区配置；未创建 fork.28 标签、未发布、未升级生产，生产保持 fork.27。
- 本轮停止资源等待进程，删除自己的在线备份副本、临时 paneld 和仪表化构建；保留 run 目录下构建/测试/CI 日志和可复现的测量脚本。
- 续跑需重新获取在线 SQLite 副本、旧 fork.27 paneld、构建旧/新版临时计数版本，再运行 `run-bench.py old/new`（隔离网络，先满足内存门槛）。所有未完成发布门禁保持未勾选。

## 第二轮续跑（2026-10-08 UTC）

- [x] 读取发布流程、核对首轮提交与生产 fork.27；仅在临时源码副本中加入计数。
- [x] 重建新版 paneld、提取生产 fork.27 paneld、取得新在线 SQLite 副本；40 模拟节点和公开开关仅在副本配置。
- [ ] 重建两份仪表化静态资源，修正测量脚本后完成受限浏览器实测。

本轮门槛替代首轮 ≥900 MiB：先启动隔离网络内的本地 paneld 与模拟器，再检查 MemAvailable ≥750 MiB、无其它 Chromium、无其它 worker 正在执行重任务。每 60 秒记录样本到 `memory-wait-r2.jsonl`，累计最多等待 3 小时。Chromium 使用 headless shell，Node 驱动和浏览器都运行在 `MemoryMax=650M`、`MemorySwapMax=0` 的 transient scope 内；一次一页，旧新版之间关闭浏览器。常驻生产容器只读观察，不停止任何其它服务或 worker，不调整 swap/sysctl。生产公开监控保持关闭，仅在本地副本验证公开页面。
