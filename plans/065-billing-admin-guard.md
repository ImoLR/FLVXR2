# 065 — Billing admin guard / 3.0.27-fork.17

修复 fork.16 暴露的 13 个计费管理接口权限缺口。基于 `bb92200f`，仅在指定 handler 首部添加 `ensureAdminAccess`，不改动上一轮许可证移除、用户接口、前端、代理、依赖或数据库结构。

## 任务清单

- [x] 核对工作区、AGENTS.md、发布/自主执行记忆；创建 fork.17 分支与本计划（保留未跟踪的 plans/048-*）。
- [x] 为 billing.go / payment.go / order.go 指定的 13 个 handler 添加管理员守卫并提交。
- [x] 添加契约测试：13 路由普通用户 403、写请求数据库不变；管理员读取及兑换码/折扣码/支付配置写入成功，并提交。
- [x] 完成后端 build、受影响包 vet、完整 go test；与上一轮 19 个失败项比较，新增失败为零。
- [x] 推送分支并确认 CI Build Check 成功；创建并推送 annotated tag `3.0.27-fork.17`；确认 Build and Push Images 成功及发布资产校验。
- [x] 创建生产回滚目录、compose/.env 副本、SQLite 在线备份与校验、镜像本地标签及回滚说明。
- [x] 安装校验后的 v6 发布资产、更新 FLUX_VERSION 并升级面板。
- [x] 只读验证生产健康、所有升级前在线节点指标推进、管理员 4 路由成功及普通用户 6 读取路由 403。
- [x] 写中文总结 `/root/flvx-workers/runs/remove-license-r2/summary.md`，完成本计划、提交 `docs(plan): mark fork17 rollout complete` 并推送。

## 验证与发布记录

生产仅允许读取 API；JWT 在内存生成，不记录密钥/token、不变更用户资料。失败集基线：`/root/flvx-workers/runs/remove-license/candidate-go-test.failnames`。

- 实现变更严格为 13 个函数首部各 3 行守卫；移除新增守卫后，三个文件与 bb92200f 字节一致。独立审查确认 helper 使用 JWT role_id（0 为管理员）。
- 新增 `TestBillingAdminPermissions`：14 子测试覆盖 13 路由（支付配置新增/更新各一次），使用有效请求和预置记录；普通用户 code=403 且四个相关表全行列快照不变，管理员同请求 code=0，写请求实际生效。连同上一轮两项定向测试全部通过（0.256s），日志位于运行目录 `billing-contract-test.log`。
- `go build ./...`、`go vet ./internal/http/handler ./tests/contract` 成功；`TMPDIR` / `GOTMPDIR` 均位于 `/root/flvx-tmp/remove-license-r2`。完整 `go test -json ./...` 退出 1，但失败集合与 round 1 的 19 项完全一致（新增 0、消失 0），新权限测试通过。证据：运行目录 `go-test-comparison.json`、`candidate-go-test.failnames` 和完整 JSON 日志。
- 独立审查通过：实现范围、契约有效请求/快照断言、预备的生产在线备份和只读验证脚本均无阻断问题。
- [CI Build Check 37201950251](https://github.com/ImoLR/FLVXR2/actions/runs/37201950251) 四项成功；注释标签 `3.0.27-fork.17` 指向通过 CI 的 `7319fe6b`，已推送。
- [Build and Push Images 37202120077](https://github.com/ImoLR/FLVXR2/actions/runs/37202120077) 成功；Release 于 2026-10-04 12:36:29 UTC 发布，非预发布且 Latest。10 项资产名与 fork.16 相同，v4/v6 compose 的两个面板镜像均固定 fork.17，两份脚本 PINNED_VERSION / REPO 正确，amd64/arm64 GOST SHA256 均通过。证据：运行目录 `release-verification.json` 与 `release-assets/`。

## 回滚原则

升级前保存 `/opt/flvx-svc/rollback/pre-fork17-<UTC TS>/`。正常回滚恢复 compose/.env 并拉取 `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.16` 后重建对应容器；无 schema 变更，不恢复数据库以免丢失升级后的正常数据。

- 生产回滚点 `/opt/flvx-svc/rollback/pre-fork17-20261004T123236Z/` 已建立：compose/.env 副本、`gost.db.validated` 在线备份（quick_check=ok）、62 张表行数及 schema、两个当前 fork.16 镜像的本地标签与 `ROLLBACK-METADATA.md`。备份时 22 个节点在线；生产容器仍为 fork.16。
- 生产已安装校验后的 v6 compose（与旧文件仅镜像版本不同），`.env` 仅替换 FLUX_VERSION 为 fork.17；升级前再次确认 22 个在线节点基线未变。仅执行 `docker compose pull backend frontend && docker compose up -d backend frontend`，两个面板容器重建成功。
- 2026-10-04 12:38 UTC 完成升级，12:38:51 UTC 只读验证通过：backend healthy、frontend HTTP 200；22 个原在线节点均有重启后的新指标。管理员指定 4 路由 code=0；使用现有用户 ID 3 / role_id=1 在内存签发的 JWT，指定 6 个管理读取路由均 code=403（API envelope，HTTP 200）。未调用任何生产写接口、未变更用户密码或资料，schema 与备份完全一致。证据：运行目录 `production-verification.json`。
- 中文总结已写入 `/root/flvx-workers/runs/remove-license-r2/summary.md`，包含文件改动、测试对比、发布/CI、备份/回滚、生产验证、破坏性操作及撤销方法、用户检查和未实施建议。
