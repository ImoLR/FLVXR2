# Plan 080 — 移除隧道整条路径延迟并回退 fork.28 监控修改

分支：`maintenance/3.0.27-fork.32-revert-latency`，起点 `54dea5bd`（fork.31）。目标发布 `3.0.27-fork.32` 并升级生产面板。

范围：撤销 fork.23/24 A+B/30/31 路径延迟 UI、API、探测逻辑及相关测试；撤销 plan 076 的全部监控页面和 WS 广播修改。保留 fork.22 分组选择器、fork.24 移动卡片、fork.25–27 非延迟改动（尤其 connect_ip）、fork.29 计费和双向标签，以及 fork.10 服务监控权限。历史计划不改动；不涉及 agent、安装脚本、依赖、数据库结构或业务数据。

## 检查清单

- [x] 阅读仓库规则和两份流程记忆，确认 fork.31 起点、15 项失败基线，创建分支与计划。
- [x] 移除路径延迟，恢复原质量探测器并保留 connect_ip 及其适用测试；提交。
- [x] 恢复 fork.27 监控页面及原浏览器 WS 广播；提交。
- [x] 完成全部五项等价检查并保存命令、输出；提交。
- [x] 单进程完成后端 build、vet、完整测试及 handler/ws race，与 fork.31 基线比较。
- [x] 完成前端 build、tsc/lint 基线比较和 5 MiB 包体门禁；提交验证结果。
- [x] 推送分支，确认 HEAD 的 CI Build Check 成功；创建并推送 annotated tag。
- [x] 确认镜像工作流成功、Latest 正式发布、资源集/版本固定/哈希正确；提交。
- [x] 记录升级前 30 分钟 tcp_conns；创建生产在线备份和旧镜像标签，验证后运行保留两份的清理脚本；提交。
- [x] 安装 fork.32 v6 compose、更新 FLUX_VERSION、拉取并重建 backend/frontend，确认健康且无结构变化；提交。
- [ ] 完成 API/前端/原始 WS 广播验证、至少 5 分钟日志及质量更新检查、至少 10 分钟连接数对比。
- [ ] 写中文总结（含破坏性动作与回滚方法、用户检查点、建议未实施），提交 `docs(plan): mark fork32 rollout complete` 并推送。

运行记录：`/root/flvx-workers/runs/revert-latency/`。所有临时文件（TMPDIR/GOTMPDIR）使用该目录下 `t/`；重任务前检查 MemAvailable ≥750 MiB，同时最多运行一个重任务。所有提交包含指定 Co-Authored-By。

## 实施记录

- 路径延迟 UI/API/权限特例、Repository 辅助方法、会话和调度器及专用测试已移除。原质量探测器逐字恢复 fork.22，仅为出口解析加回 fork.27 的 `outNodes[0].ConnectIP`。保留并适配 `TestCustomConnectIPQuality`，有/无中继均验证原探测器直接探测自定义出口 IP；定向测试通过。
- `forward.tsx` 反向应用 90753d66、6d838b4d，保留移动卡片和双向标签；API/select/auth/路由及可见性契约恢复 fork.22。`IsNodeConnected` 无其他调用，连同测试删除。
- 六个监控相关文件逐字恢复 fork.27；两个 hooks 无范围外引用，已删除。恢复 WS 广播及测试，继续仅剥离 quotaGroups，保留 forward_metrics/serviceConnections。fork.10 管理员服务监控限制保持。

## 等价检查

五项全通过，命令及原始输出：`/root/flvx-workers/runs/revert-latency/equivalence.log`。除逐文件 diff 外，独立从 fork.22 重放白名单提交构造预期文件：Repository 仅 513805cb/02f454f0/b0049ee0，forward 仅 bd6a4488/d69ebcd8/944f1c7d/2f9b850d/2efbd1d4/60caa2ea，WS 仅 b0049ee0；均逐字相同。生产模型、agent、脚本、依赖相对起点无改动。

## 后端验证

`GOMAXPROCS=2`、`-p 1` 串行执行；build/vet 通过。`go test ./... -count=1` 失败集合恰为 fork.31 的 15 项，无新增/消失。handler/ws 全包 race 无竞争报告；handler 仅两个既有失败（NonTLSProtocol_NoNodelay、AdvancesOverdueAnchorTimes），ws 通过。原始日志及比较 JSON 在运行目录。

## 前端验证

`npm run build`（含 tsc）通过；主 JS `index-KGi1s1kq.js` 为 2,751,994 B，距 5 MiB 上限 2,490,886 B。不带 --fix 的全量 ESLint 与从 54dea5bd 导出的源码比较：错误 96→96，新错误 0；警告 3657→3529。未修改格式、依赖或增加前端测试。

## 发布启动

HEAD `c576029d` 的 CI Build Check [37808506936](https://github.com/ImoLR/FLVXR2/actions/runs/37808506936) 四项全成功。annotated tag `3.0.27-fork.32` 指向该 HEAD，已推送，等待镜像发布工作流。

## 发布资产验证

[Build and Push Images 37808887224](https://github.com/ImoLR/FLVXR2/actions/runs/37808887224) 成功；[fork.32 Release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.32) 于 2026-10-08T16:39:38Z 发布，为 Latest、非 prerelease。十个资产与 fork.31 相同；v4/v6 compose 均固定 fork.32，两安装脚本 PINNED_VERSION/REPO 正确，两架构 gost SHA256 一致。

## 生产回滚点

升级前 30 分钟 tcp_conns 已保存。在线备份 `/opt/flvx-svc/rollback/pre-fork32-20261008T164109Z/` 含 compose/.env、`gost.db.validated`（quick_check=ok）、全表计数、旧 fork.31 镜像本地标签和 ROLLBACK-METADATA.md；node/tunnel/forward/user = 26/53/25/10。已运行 prune-backups.sh，保留 pre-fork31 + pre-fork32，删除 pre-fork30 目录、其两个本地标签和 fork.29 镜像。正常回滚只需恢复 compose/.env 及 fork.31 镜像，不恢复 DB。

## 生产升级

已安装经过验证的 v6 compose、更新 FLUX_VERSION 并 pull/up backend/frontend。后端于 2026-10-08T16:41:46Z、前端 16:41:52Z 启动 fork.32，无重启，后端 healthy。管理员 node/tunnel/forward 列表 HTTP 200/code=0；25 节点上报、node_metric 推进、53 隧道有质量数据。138 项表/索引定义与升级前逐字一致。延迟 GET（管理员、63666）为 HTTP 404 / `404 page not found`；生产 JS 2,754,025 B，测速文案和路径接口消失、grouped 选择器及双向标签存在。继续十分钟观察和 WS 实测。
