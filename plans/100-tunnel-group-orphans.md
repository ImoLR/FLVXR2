# 100 隧道、节点和分组授权孤儿记录修复

发布：`3.0.27-fork.51`。基线：`0b5e0074`（fork.50 分支头）。仅修改面板后端与测试，不改 agent、不 OTA、不改变有效业务记录。

- [x] 核对生产版本 fork.50、流程与 worktree，保留主 checkout 原状。
- [x] 审核所有隧道、节点、用户授权删除路径及数据库引用，记录基线。
- [x] 修复级联与一次性幂等清理，补充 SQLite/PostgreSQL 回归测试。
- [x] 完成后端全量测试（失败集合不超过基线 15）和相关包 go vet；生产副本 dry-run。
- [x] 推送分支，CI 通过，发布 fork.51；核验 Images 和全部 release assets。
- [x] 生产标准备份与保留两份，验证精确数据回滚。
- [x] 升级生产并执行启动清理。
- [x] 验证零孤儿、用户权限/分组成员相同、29 节点上报，删除自建数据库副本。
- [x] 完成中文总结并提交发布记录。

## 核验记录

生产 `/opt/flvx-svc/.env` 起始版本为 `3.0.27-fork.50`。独立 worktree 为 `/root/projects/flvxx/flvxx-tunnel-group-orphans`，分支 `maintenance/3.0.27-fork.51-tunnel-group-orphans`。

审核：隧道所有删除入口汇入 DeleteTunnelCascade；缺失两代分组关联、列表关联与 tunnel_metric 清理。DeleteUserTunnel 是现行裸删 grant 主体的路径；用户级联已有 grant 清理，三种分组撤权均先检查剩余 grant，保留语义并补回归。节点原实现删除链路和转发端口但保留隧道主体，改为拒绝有业务引用的节点，并清理无引用节点的分组入口、身份、标签和指标。配置修订、部署日志属于历史保留。

fork.50 本机全量测试基线共 15 个失败测试/子测试（清单及日志在 worker run 的 baseline.log）。生产只读初查：23 条 grant、5 条 node_identity、15 条 tunnel_metric 孤儿；4 个隧道组和 4 个用户组，29 个节点。正式 dry-run 将以一致性副本和代码同源 SELECT 为准。

实现与定向验证：SQLite/PostgreSQL 18 的级联、每类缺失引用、有效行不变、共享 grant 保留均通过；SQLite 事务失败回滚和实际启动迁移通过。HTTP 覆盖单删/批删/含转发删除、用户单删/批删、撤权、联邦清理和远程节点删除，普通节点在用拒删及空闲删除均通过。启动清理不增加 schema，只写入 `tunnel_group_orphan_cleanup_v1=done`。

一致性生产副本 dry-run 与真实 Repository.Open 迁移结果一致：group_permission_grant=23，tunnel_metric=15，node_identity=5，其余目标表=0。15 条指标来自已删除隧道「拼车hinet1 NB沪港IPLC」，上报节点「Nobrand IPLC 沪港」仍存在，节点不会被删除。SQL/逐行清单保存在 worker run 中；副本验证 schema、权限、分组成员、冻结质量历史与配置/部署历史不变。

发布门禁：`go test ./...` 完成，15 项失败集合与 fork.50 完全相同、0 新失败；相关包 `go vet` 通过。新测试 SQLite、PostgreSQL 18 均通过（含共享授权保留、删除/迁移失败完整回滚），无 schema 变化。升级前初始读数：用户隧道权限 143 对；节点 29/29 在线；隧道组 4、用户组 4。

生产回滚点已建立：`/opt/flvx-svc/rollback/pre-fork51-20261016T083644Z`（fork.50，在线 SQLite quick_check=ok，compose/.env、本地镜像 tag、rollback.sh）。43 行选择性 undo 已在副本验证可恢复，再次清理后恢复干净状态。指定 prune 脚本执行完毕，保留 pre-fork51 与 pre-fork50，移除 pre-fork49（旧 fork.48 回滚点及对应镜像）。生产仍是 fork.50，等待 Images/Release 资产完成再升级。

分支 CI `38873472540` 全部成功；annotated tag `3.0.27-fork.51` 指向 `0ccb002a`，Images run `38873710248` 正在运行。

Images `38873710248` 全部通过。fork.51 已发布为正式 Latest；10 个资产名称与 fork.50 相同，compose 的 fork.51 镜像、安装脚本 PINNED_VERSION/仓库、两架构 gost SHA256、offline zip 完整性均通过。切换前再次只读确认：43 条待清理记录与备份/dry-run 完全一致，143 对权限不变。

生产 2026-10-16 08:47Z（台湾 16:47）切换 fork.51。启动 summary：grant=23、tunnel_metric=15、node_identity=5，其余=0，实际 ID 集合与 dry-run/备份相同；之后 12 个目标表孤儿均为 0。后端 healthy、前端 nginx/version.json 正常；29/29 在线且全部有切换后的新上报，agent 版本逐节点不变。143 对 user_tunnel dump 完全一致，SHA256 `46acfebb1719f122890c98b78ab13880f613e6cd9fdf30fa5c0480efb3b409e3`；4+4 分组 API、成员/入口选择、有效 grant/身份、业务主体集合、schema 与历史哈希不变。冻结 tunnel_quality 356600 行保持原样。数据库 quick_check=ok。自建 PostgreSQL 容器及匿名测试卷、生产副本 dry-run.sqlite 已删除；未进行 OTA，未改动主 checkout 或其他栈。

最终中文报告：`/root/flvx-workers/runs/tunnel-group-orphans/summary.md`，含根因与各入口、完整清理 ID 清单、门禁、CI/Release、备份、权限/分组/节点核验、所有破坏性操作和撤销方法、建议（未实施）及还没解决。遗留 15 项基线测试失败未扩大；已丢失的历史主体名称不猜测。

## r2 / fork.52：节点删除 released WG 资源回归

基于 `45670715`，分支 `maintenance/3.0.27-fork.52-node-delete-wg-released`。仅修改节点运行资源引用条件与同事务 released 行清理，不改其他引用、清理规则或 marker，不 OTA。

- [x] 阅读流程与上一轮证据，确认生产起始版本 fork.51，建立指定分支。
- [x] 修复 released 资源阻止节点删除，补齐 SQLite/PostgreSQL 三类回归并验证。
- [x] 完成后端全量测试，与上一轮 15 项失败集合对比，完成 repo go vet。
- [ ] 推送并验证 CI，发布 fork.52，核验 Images、10 件资产及版本 pin。
- [ ] 生产在线备份、旧镜像 tag 与回滚脚本，执行仅保留两份备份。
- [ ] 升级 fork.52，核验健康、29/29 在线、权限与 4+4 分组不变、marker 与不重复清理。
- [ ] 清理自建测试资源，保存中文总结与证据，提交完成记录。

定向测试 SQLite、PostgreSQL 18 全部通过：released-only 可删除且清理该节点资源；active 引用拒删，节点及已有级联表逐行不变；其他节点 released 行不变。SQLite 父节点删除失败时 released 资源亦完整回滚。repo go vet 通过。

全量 `go test ./...` 仍为与 r1 完全相同的 15 项失败，无新增；日志与逐项比较在 `tunnel-group-orphans-r2/final-tests.log`、`test-comparison.json`。实现提交 `fe0e6048`，分支 CI `38877171403` 四项全绿；fork.52 annotated tag 指向该已通过 CI 的实现提交。
