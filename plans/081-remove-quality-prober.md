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
- [ ] 创建在线 DB/配置/镜像回滚点，验证 quick_check/计数并执行保留两份备份清理；提交。
- [ ] 安装 fork.33 v6 compose、更新版本、pull/up，验证健康、API、指标推进及 schema 不变；提交。
- [ ] 完成 ≥30 分钟生产观察、质量表停止写入、404、JS/日志验证及全部节点 TCP 前后对比。
- [ ] 写中文总结，完成计划并提交 `docs(plan): mark fork33 rollout complete`、推送。

后台：ListEnabledTunnelIDs 仅探测器使用，仓储文件整体删除；专用 TestCustomConnectIPQuality 删除，其余诊断/connect-IP 测试保留。共用函数引用记录于 shared-callers.log。后台任务计数 12→11，避免退出等待已删除任务；监控路径仅匹配 metrics，删除的质量路径返回 404。

前端：移除质量轮询、历史条、延迟/丢包 KPI、质量趋势和质量派生状态列；保留隧道列表/卡片、流量图及其独立时间范围、监控权限卡片。

后端门禁：指定 grep 无命中（exit 1）；变更仅任务范围内 10 文件。build/vet 通过；GOMAXPROCS=2、-p 1 全量测试与 fork.32 相同 15 项失败，无新增；handler race 无数据竞争，仅两个既有失败。详见 scope-gate.log、gates.json、baseline-comparison.json、race.log。

前端门禁：npm run build（含 tsc）通过；主 JS 2,739,492 B，距 5 MiB 上限 2,503,388 B。ESLint 无 --fix 基线比较：错误 96→96、警告 3529→3529，无新增错误；移除产生的 import 排版警告已修正并重新构建验证。

发布：b0496fce 的 CI Build Check 37839682526 四项全成功；打标前已记录生产 fork.32 19:52:19Z—20:22:19Z（30 分钟）tcp_conns，面板自 16:41Z 连续运行。annotated tag 3.0.27-fork.33 指向该 HEAD，已推送。

发布资产：Build and Push Images 37840030023 成功；fork.33 于 2026-10-08T20:49:06Z 发布，为 Latest、非 prerelease。十个资产与 fork.32 一致，v4/v6 compose 镜像及两安装脚本固定 fork.33/ImoLR/FLVXR2，两架构 gost SHA256 一致。
