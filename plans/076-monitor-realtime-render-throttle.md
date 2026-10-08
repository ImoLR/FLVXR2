# 076 监控实时渲染优化与 fork.28 发布

范围：保持每秒实时数据和现有 UI，合并浏览器更新、缓存未变化节点渲染、精简浏览器广播、暂停后台轮询。保持 agent、存储指标和生产节点/隧道配置不变。

- [x] 阅读约束与发布流程，从 fe426246 建立 maintenance/3.0.27-fork.28-monitor-render。
- [x] 实现共享实时批处理、监控卡片/行 memo、可见性及活动页轮询控制；提交。
- [x] 精简广播字段并完成 Go 单测；提交。
- [x] 完成 lint、类型检查、构建、Go 全量失败集合对比；提交结果。
- [x] 生产在线备份副本 + 本地 16365 面板 + 40 节点，旧新版各四种布局 30 秒实测、每秒更新检查、截图与后台暂停验证；提交结果。
- [x] 推送分支并确认 CI Build Check 绿色。
- [x] 发布注释标签 3.0.27-fork.28；确认镜像流水线、Latest、资产集合、脚本固定版本/仓库、gost SHA256。
- [x] 生产升级前备份并保留最近两份；仅升级 backend/frontend。
- [ ] 只读验证生产健康、指标前进、生产监控/节点页实时值、本地公开页实时值、非管理员节点列表 403；生产公开监控保持关闭。
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
- 首轮使用 ≥900 MiB 门槛；第二轮按下面的 ≥750 MiB + 650 MiB scope 门槛执行。

## 首轮资源阻塞记录（第二轮已完成实测）

- 连续等待约 30 分钟，宿主机可用内存约 532–722 MiB，始终未满足 Chromium 启动前 ≥900 MiB 的任务硬约束；未启动 Chromium，未获得性能数字或截图。
- 不停止其它服务、不修改主机内存/交换区配置；未创建 fork.28 标签、未发布、未升级生产，生产保持 fork.27。
- 本轮停止资源等待进程，删除自己的在线备份副本、临时 paneld 和仪表化构建；保留 run 目录下构建/测试/CI 日志和可复现的测量脚本。
- 续跑需重新获取在线 SQLite 副本、旧 fork.27 paneld、构建旧/新版临时计数版本，再运行 `run-bench.py old/new`（隔离网络，先满足内存门槛）。所有未完成发布门禁保持未勾选。

## 第二轮续跑（2026-10-08 UTC）

- [x] 读取发布流程、核对首轮提交与生产 fork.27；仅在临时源码副本中加入计数。
- [x] 重建新版 paneld、提取生产 fork.27 paneld、取得新在线 SQLite 副本；40 模拟节点和公开开关仅在副本配置。
- [x] 重建两份仪表化静态资源，修正测量脚本后完成受限浏览器实测。

本轮门槛替代首轮 ≥900 MiB：先启动隔离网络内的本地 paneld 与模拟器，再检查 MemAvailable ≥750 MiB、无其它 Chromium、无其它 worker 正在执行重任务。每 60 秒记录样本到 `memory-wait-r2.jsonl`，累计最多等待 3 小时。Chromium 使用 headless shell，Node 驱动和浏览器都运行在 `MemoryMax=650M`、`MemorySwapMax=0` 的 transient scope 内；一次一页，旧新版之间关闭浏览器。常驻生产容器只读观察，不停止任何其它服务或 worker，不调整 swap/sysctl。生产公开监控保持关闭，仅在本地副本验证公开页面。

## 浏览器验收结果（2026-10-08 UTC，第二轮通过）

旧版：fe426246 前端 + 生产 fork.27 镜像提取的 paneld。新版：20bd44a9 产品代码（后续提交仅改本计划）。临时源码副本加入 MonitorView render / layout-effect commit 计数，未提交计数代码。独立 network namespace、本地 16365、生产 SQLite 新在线备份副本，40 节点每秒各一条指标、每条含 20 个 forward_metrics 和 20 个 serviceConnections。统一使用递增样本及周期流量字段，Chrome headless shell、单浏览器单页，无 CPU 降速。每场景预热后测约 30 秒；busy = CDP TaskDuration / 实测墙钟时间，长任务来自 PerformanceObserver。

| 场景 | renders/s 旧→新 | commits/s 旧→新 | 主线程 busy % 旧→新 | 长任务 数/总ms 旧→新 | bytes/msg 旧→新 |
|---|---:|---:|---:|---:|---:|
| 1440 grid | 39.84 → 1.37 | 39.80 → 1.30 | 56.90 → 15.52 | 2/315 → 2/314 | 4240.47 → 487.47 |
| 1440 list | 38.26 → 1.37 | 38.26 → 1.30 | 87.97 → 15.52 | 2/319 → 2/346 | 4241.10 → 488.09 |
| 390 grid | 39.68 → 1.43 | 39.68 → 1.30 | 50.06 → 14.60 | 2/270 → 2/371 | 4241.81 → 489.47 |
| 390 list | 39.11 → 1.33 | 39.11 → 1.27 | 83.78 → 15.52 | 2/312 → 2/349 | 4242.41 → 489.20 |

- 四场景 renders/s 降低 96.4%–96.6%，busy 降低 70.8%–82.4%，bytes/msg 降低约 88.5%；新消息的 `forward_metrics` / `serviceConnections` 均为零。长任务数量没有降低（每组均 2 次），总时长如表，未隐藏该结果。
- 新版每场景均观察到 30 次显示变化，最大间隔依次 1021.1 / 1025.6 / 1021.1 / 1030.3 ms。另做 180 秒持续观察：180 次变化，最大 1033.6 ms，原始单节点帧最大间隔 1120 ms，7199 条指标帧无内部统计字段。
- CDP frozen→active 使 document.visibilityState=hidden，排除 5 秒切换/在途响应后，32 秒内 MonitorView 提交数=0、监控轮询=0，WS 继续接收。用焦点模拟恢复 visible 后，128 ms 内发起刷新；按精确 `/monitor/nodes` 请求复核，恢复后 1.5 秒只有 1 次列表刷新（约 126 ms）。
- 节点页签期间隧道请求=0；激活隧道立即有请求，切回节点后观察 61 秒隧道请求=0。断开模拟节点：1.5 秒仍在线，4.5 秒已离线，重连后 1.3 秒已恢复在线。
- 本地 `/node` 与未登录 `/tz` 均可见实时值变化（3.5 秒内分别收到 140 / 139 条指标）。生产公开监控未切换。
- 八张最终截图逐一目视核对，布局相同；四组 40 节点 DOM x/y/width/height 完全一致。桌面截图 1440×1000，手机 390×844。截图目录 `/root/flvx-workers/runs/monitor-render/screens/`：
  - `old-1440-grid.png` / `new-1440-grid.png`
  - `old-1440-list.png` / `new-1440-list.png`
  - `old-390-grid.png` / `new-390-grid.png`
  - `old-390-list.png` / `new-390-list.png`
  - 附加：`new-mobile-node.png`、`new-mobile-tz.png`。
- 原始数据：`/root/flvx-workers/runs/monitor-render/benchmark-{old,new}.json`，各场景 `*-cadence.json`、`*-geometry.json`；自动门禁结果 `/root/flvx-workers/runs/monitor-render-r2/measurement-gate.json`。最终日志 `final-old.log` / `final-new.log`。

### 资源门槛与测量工具修正

累计资源等待约 51 分钟，每分钟采样保存在第二轮 `memory-wait-r2.jsonl`；每次启动前均 ≥750 MiB、无其它 Chromium / worker 重任务，具体数值见 `launches.jsonl`。所有浏览器与 Node 驱动在 `MemoryMax=650M`、`MemorySwapMax=0` scope 内，全部 scope OOM=0；最终 old/new scope 统计见 `scope-old.json` / `scope-new.json`。未停止或调整其它服务/worker、swap、sysctl。

首轮脚本的未跑通部分已修正：DOM 首次加载等待、截图 clip 导致的视口重置（旧新版均会受影响，改普通视口截图）、CDP 恢复可见方法、侧栏与页签同名定位、节点页周期流量字段、公开页清除登录状态后的日志空值。初次临时 npm build 的 tsc 在自行设置的 384 MiB Node 堆限制下退出，改用 Vite 生成仪表化资源；首轮产品 tsc/构建结果与本轮 CI 保持有效。早期一次旧模拟器样本出现约 2 秒显示间隔，未作为通过证据；改用各节点递增样本并记录原始帧后，180 秒观察及最终四场景均通过，未发现需要修改产品代码的缺陷。

## 发布核验

- 测量提交 `4d7600b6` 的 CI Build Check `37771670358` 全部成功。
- 注释标签 `3.0.27-fork.28` 指向 `4d7600b6`，注释内容为同名版本；Build and Push Images `37771971356` 成功。
- Release 为 Latest、非 prerelease，与 fork.27 资产名集合一致（10 个）；全部下载大小/摘要、两份 compose 镜像版本、两份脚本 PINNED_VERSION 与 `REPO=ImoLR/FLVXR2` 通过校验。
- `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.28` 清单均含 linux/amd64 与 linux/arm64。两个 gost SHA256 文件与二进制、offline zip 内的 agent 均一致；只发布资产，不升级生产 agent。
- 证据：首轮目录 `release/verification.json`，第二轮目录 `release-ci.json`、`backend-manifest.json`、`frontend-manifest.json`。

## 生产升级

- 回滚点 `/opt/flvx-svc/rollback/pre-fork28-20261008T120129Z/`：compose/.env 副本、SQLite 在线备份 `gost.db.validated`（366,944,256 B，quick_check=ok）、全表计数、ROLLBACK-METADATA.md；28 节点、53 隧道、25 转发、11 用户。
- 旧 fork.27 镜像保留为 `local/flvxx-{backend,frontend}:pre-fork28-20261008T120129Z`。运行指定 prune-backups.sh 后只保留 pre-fork28 与 pre-fork27；已删除 pre-fork26 备份及其 fork.25 镜像，详见 `prune-backups.log`。
- 安装已校验 fork.28 v6 compose，FLUX_VERSION 改为 3.0.27-fork.28；仅执行 `docker compose pull backend frontend` 和 `up -d backend frontend`。backend healthy、frontend running，待完成只读业务与浏览器验收。
- 回滚：从上述目录恢复 docker-compose.yml/.env，重新 up -d backend frontend 使用 fork.27 镜像；无 schema 变更，常规回滚不恢复数据库，以保留升级后的实时数据。
