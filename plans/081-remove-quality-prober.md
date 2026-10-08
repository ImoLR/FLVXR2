# Plan 081 — 移除隧道质量探测并发布 fork.33

起点：`2936f770`，分支：`maintenance/3.0.27-fork.33-remove-quality-prober`。
范围仅限后台质量探测、对应监控 API/UI 和死代码；保留最优策略、诊断共用函数、流量趋势、监控权限、TunnelQuality 模型和 AutoMigrate。无 agent、依赖、安装脚本、DB 结构/业务数据改动。
运行证据：`/root/flvx-workers/runs/remove-quality-prober/`；所有临时文件放 `t/`。重任务串行，启动前 MemAvailable ≥750 MiB。

- [x] 读取规则和流程、核对起点与基线、创建分支及计划。
- [x] 删除后台质量探测、专用测试、API 和不再使用的仓储方法；提交。
- [x] 删除隧道监控质量 UI/API 类型，保留非质量功能；提交。
- [x] 完成删除/保留/范围门禁、后端 build/vet/全量测试及 handler race；提交。
- [x] 前端 build、tsc/lint 基线比较及 PWA 包体检查；提交。
- [x] 推送并确认 HEAD CI 绿色，记录生产 fork.32 ≥30 分钟各节点 TCP 基线，再推送 annotated tag。
- [x] 验证镜像工作流、Latest 正式发布、资产集、固定版本及 SHA256；提交。
- [x] 创建在线 DB/配置/镜像回滚点，验证 quick_check/计数并执行保留两份备份清理；提交。
- [x] 安装 fork.33 v6 compose、更新版本、pull/up，验证健康、API、指标推进及 schema 不变；提交。
- [x] 完成 ≥30 分钟生产观察、质量表停止写入、404、JS/日志验证及全部节点 TCP 前后对比。
- [x] 写中文总结，完成计划并提交 `docs(plan): mark fork33 rollout complete`、推送。

后台：ListEnabledTunnelIDs 仅探测器使用，仓储文件整体删除；专用 TestCustomConnectIPQuality 删除，其余诊断/connect-IP 测试保留。共用函数引用记录于 shared-callers.log。后台任务计数 12→11，避免退出等待已删除任务；监控路径仅匹配 metrics，删除的质量路径返回 404。

前端：移除质量轮询、历史条、延迟/丢包 KPI、质量趋势和质量派生状态列；保留隧道列表/卡片、流量图及其独立时间范围、监控权限卡片。

后端门禁：指定 grep 无命中（exit 1）；变更仅任务范围内 10 文件。build/vet 通过；GOMAXPROCS=2、-p 1 全量测试与 fork.32 相同 15 项失败，无新增；handler race 无数据竞争，仅两个既有失败。详见 scope-gate.log、gates.json、baseline-comparison.json、race.log。

前端门禁：npm run build（含 tsc）通过；主 JS 2,739,492 B，距 5 MiB 上限 2,503,388 B。ESLint 无 --fix 基线比较：错误 96→96、警告 3529→3529，无新增错误；移除产生的 import 排版警告已修正并重新构建验证。

发布：b0496fce 的 CI Build Check 37839682526 四项全成功；打标前已记录生产 fork.32 19:52:19Z—20:22:19Z（30 分钟）tcp_conns，面板自 16:41Z 连续运行。annotated tag 3.0.27-fork.33 指向该 HEAD，已推送。

发布资产：Build and Push Images 37840030023 成功；fork.33 于 2026-10-08T20:49:06Z 发布，为 Latest、非 prerelease。十个资产与 fork.32 一致，v4/v6 compose 镜像及两安装脚本固定 fork.33/ImoLR/FLVXR2，两架构 gost SHA256 一致。

回滚点：`/opt/flvx-svc/rollback/pre-fork33-20261008T205006Z`，包含 compose/.env、在线 gost.db.validated（quick_check=ok）、全表计数/结构、fork.32 镜像本地标签与 ROLLBACK-METADATA.md；node/tunnel/forward/user=26/53/25/10。已运行 prune-backups.sh，仅保留 pre-fork32/pre-fork33，删除 pre-fork31 备份目录/标签及 fork.30 镜像。

生产升级：后端 2026-10-08T20:50:36Z、前端 20:50:42Z 启动 fork.33，后端 healthy、重启 0；25 节点持续上报，管理员节点/隧道/转发列表 26/53/25 均 HTTP 200/code=0。138 项表/索引定义一致。质量/质量历史接口均 404；生产 JS 2,741,523 B，不含质量趋势/质量 API，流量趋势、隧道监控、监控权限与最优出口存在。旧后端停止时质量表 356,600 行、max(timestamp)=1791492633772，已启动至少 30 分钟观察。

完整观察：20:50:56Z—21:21:01Z（1805.061 秒），42 次检查均 healthy、指标新鲜、质量表 356,600 行/max timestamp 1791492633772/max ID 24775928 不变；138 项 schema 仍一致。30 分钟观察窗错误/探测器日志均 0，升级启动时两条节点 23/32 配额未连接提示与 fork.32 完全相同，无新增错误。前后各 ≥30 分钟全节点表见 metrics-table.md：入口 2/23/32 为 515.1→309.6、246.2→76.8、207.6→54.0；出口/中继多数下降，未用于隧道节点多数仅小幅波动，节点 24 无上报。

完成：中文总结已写入 `/root/flvx-workers/runs/remove-quality-prober/summary.md`，含完整门禁命令输出、测试基线、发布/部署事实、全部 26 节点前后均值与角色、噪声说明、删除清单和回滚、用户检查点及保留历史质量表的后续建议。测试/构建/观察进程结束，无临时生产 DB 副本；未运行 Chromium。最后提交推送后核对 HEAD CI。
