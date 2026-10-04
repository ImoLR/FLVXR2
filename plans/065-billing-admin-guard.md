# 065 — Billing admin guard / 3.0.27-fork.17

修复 fork.16 暴露的 13 个计费管理接口权限缺口。基于 `bb92200f`，仅在指定 handler 首部添加 `ensureAdminAccess`，不改动上一轮许可证移除、用户接口、前端、代理、依赖或数据库结构。

## 任务清单

- [x] 核对工作区、AGENTS.md、发布/自主执行记忆；创建 fork.17 分支与本计划（保留未跟踪的 plans/048-*）。
- [x] 为 billing.go / payment.go / order.go 指定的 13 个 handler 添加管理员守卫并提交。
- [ ] 添加契约测试：13 路由普通用户 403、写请求数据库不变；管理员读取及兑换码/折扣码/支付配置写入成功，并提交。
- [ ] 完成后端 build、受影响包 vet、完整 go test；与上一轮 19 个失败项比较，新增失败为零。
- [ ] 推送分支并确认 CI Build Check 成功；创建并推送 annotated tag `3.0.27-fork.17`；确认 Build and Push Images 成功及发布资产校验。
- [ ] 创建生产回滚目录、compose/.env 副本、SQLite 在线备份与校验、镜像本地标签及回滚说明；安装 v6 发布资产并升级面板。
- [ ] 只读验证生产健康、所有升级前在线节点指标推进、管理员 4 路由成功及普通用户 6 读取路由 403。
- [ ] 写中文总结 `/root/flvx-workers/runs/remove-license-r2/summary.md`，完成本计划、提交 `docs(plan): mark fork17 rollout complete` 并推送。

## 验证与发布记录

待各步骤完成后记录。生产仅允许读取 API；JWT 在内存生成，不记录密钥/token、不变更用户资料。失败集基线：`/root/flvx-workers/runs/remove-license/candidate-go-test.failnames`。

## 回滚原则

升级前保存 `/opt/flvx-svc/rollback/pre-fork17-<UTC TS>/`。正常回滚恢复 compose/.env 并拉取 `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.16` 后重建对应容器；无 schema 变更，不恢复数据库以免丢失升级后的正常数据。
