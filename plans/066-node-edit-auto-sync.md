# 节点编辑自动同步与 fork.18 发布

基线：`2d0676e3`；分支：`maintenance/3.0.27-fork.18-node-auto-sync`。
范围仅面板：节点地址快照同步、受影响运行时下发、编辑反馈；无 schema、agent、安装脚本或依赖变更。

- [x] 阅读仓库规则及发布记忆，创建指定分支。
- [x] 实现事务内入口地址 token 重写与单元测试。
- [x] 实现节点变更分类、受限并发运行时同步、WG 路径及失败反馈，完成 handler 测试。
- [x] 完成前端同步数量与失败提示，并通过构建。
- [x] 完成 Go build/vet/全量测试，与基线失败集合对比。
- [x] 完成隔离 netns E2E 与生产数据库在线备份副本的无变化编辑验证。
- [x] 推送分支并确认 CI Build Check 成功。
- [x] 发布 annotated tag `3.0.27-fork.18`，验证发布工作流及全部资产。
- [x] 创建生产回滚点，升级 backend/frontend，完成只读健康验证。
- [x] 严格复核并事务修复节点 23 的七条隧道入口地址，通过列表 API 验证。
- [x] 完成中文总结、最终计划提交并推送。

发布门禁：任何新增失败或必要 E2E 未通过即停止发布，保留提交并推送工作分支。
生产仅修改指定七条隧道的 `in_ip`；升级前备份 compose、.env、在线 SQLite 快照并保留旧镜像。

验证记录：新增 repository/handler 测试通过；最终 netns E2E 验证出口换 IP 自动拨号、入口及规则列表同步、自定义域名保留、名称编辑零下发，监听修改同步 gost+nftables 且 gost 规则仅下发一次，两类流量均通。生产库副本节点 23 未变化编辑返回零同步，49 条 tunnel 与 26 条 forward_port 全行未变化。

最终本地门禁：Go build 通过；修改包 vet 通过；新增 handler race 测试通过；npm build 通过。全量 Go 测试与精确基线 `2d0676e3` 以及历史清单均为同一 19 个失败，无新增/缺失。日志与比较结果保存在 `/root/flvx-workers/runs/node-auto-sync/`。

CI Build Check：`37279826585`，提交 `51319508`，前端/后端/agent 构建及 PostgreSQL 合约测试全部成功；发布标签固定在该已验证提交，后续计划进度为文档提交。

发布：tag `3.0.27-fork.18` → `51319508`；"Build and Push Images" `37280149449` 成功；release 2026-10-05T08:05:57Z 发布，非 prerelease、为 Latest，10 个资产与 fork.17 一致，v4/v6 compose 镜像为 `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.18`，两个安装脚本 `PINNED_VERSION=3.0.27-fork.18`、`REPO=ImoLR/FLVXR2`，gost amd64/arm64 sha256 校验一致。

生产：回滚点 `/opt/flvx-svc/rollback/pre-fork18-20261005T080650Z`（compose/.env 副本、`gost.db.validated` quick_check=ok、ROLLBACK-METADATA.md）+ 本地镜像 `local/flvxx-{backend,frontend}:pre-fork18-20261005T080650Z`。08:07Z 升级 backend/frontend 至 fork.18，backend healthy，22 个在线节点 node_metric 均在重启后更新（离线 3 个与升级前相同），tunnel/forward 列表 API code 0。

数据修复：08:08:44Z 单事务将隧道 25/26/28/29/39/42/43 的 `in_ip` 由 `103.177.163.158` 改为 `163.53.55.158`（复核：入口节点仅 23，节点 23 IPv4=163.53.55.158，rowcount=7）；列表 API 中七条隧道及其 10 条规则均显示 `163.53.55.158`，其它隧道 `in_ip` 与备份一致。证据在 `/root/flvx-workers/runs/node-auto-sync-r2/`。
