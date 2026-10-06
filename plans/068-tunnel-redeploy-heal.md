# 068 — 节点重连隔离重部署与自愈（fork.20）

## 范围与约束

仅修改面板后端；节点重连只替换本节点配置，全量重部署按下游优先逐节点尽力完成。创建/更新保持严格失败回滚语义。无 agent、前端、依赖、数据库结构或安装脚本变更。生产仅升级面板及按诊断结果执行任务 P。

## 任务清单

- [x] 读取仓库规范与发布记忆，从 `58d2b972` 创建 fork.20 分支。
- [x] 核对运行时调用链及 federation 行为，实现节点隔离、隧道互斥和全量尽力重部署。
- [x] 实现重连去抖/串行、失败队列及退避自愈，增加结果日志。
- [x] 增加 fake sender 测试并通过新测试的 race 检查。
- [x] 串行执行 build、相关包 vet、全量测试，对照基础提交失败集合。
- [x] 完成本地 netns E2E：重连抖动、入口离线重部署/恢复、双出口部分离线。
- [x] 推送分支并确认 CI Build Check 通过。
- [x] 发布注解标签 `3.0.27-fork.20`，确认镜像流水线与所有发布资产。
- [x] 备份生产 compose、环境、在线 SQLite 与镜像，记录回滚步骤。
- [x] 升级生产面板，验证健康、指标、列表 API 与重连日志。
- [ ] 执行任务 P：诊断全部活跃 type-2 隧道，仅重部署符合条件的故障隧道并记录前后结果。
- [ ] 写中文总结，提交 `docs(plan): mark fork20 rollout complete` 并推送。

## 实施及验证记录

- 运行时替换按出口→逆序中继→入口进行；每节点完成删除和添加后才处理下一节点，汇总所有错误并记录失败二元组。
- 同一隧道的重连、重部署、创建/更新与删除共用互斥。创建在未提交事务中取得新 ID 时使用 TryLock，避免等待持锁查库操作形成死锁。
- 本地节点重连只重建本节点运行时及本节点转发端口（含 nftables/WG 规则的节点过滤），不释放或改写 federation 绑定。全量重部署及 remote 重试沿用远端 release/reserve/apply 协议，但限定当前远端节点并保留其他绑定。
- 去抖常量为 10 秒；运行中重连合并为一次后续运行。失败队列每 60 秒检查，失败退避依次为 60/120/240/480/600 秒，成功及过期成员关系移除。
- 创建/更新既有离线/超时延迟下发行为与严格错误回滚均保留，不扩大 best-effort 语义。

构建与测试使用 `/root/flvx-workers/tmp-go`，重型任务串行。生产回滚基线为 fork.19；本计划不更改 schema。

- 新增 16 组 `TestTunnelRedeploy` 测试（含子测试）；定向测试通过，最新 `-race` 通过（19.879s），无竞争报告。测试额外覆盖已删除隧道重试移除、迟到离线通知、多入口 gost/nftables 转发过滤及最新 federation 端口只读恢复。

- 本地 Go 1.24.4：`go build -p 1 ./...` 与 `go vet -p 1 ./internal/http/handler` 均通过；当前与 `58d2b972` 均运行 `go test -p 1 ./... -count=1`，失败集合完全一致（19 项，与历史清单一致），新增失败 0。门禁记录：`/root/flvx-workers/runs/tunnel-redeploy-heal/gates.json`。
- 全量测试揭示 node_sync 原有“转发失败抑制隧道失败”分支与新的尽力部署冲突；已修复为运行时失败时汇总至隧道错误，保留成功规则计数，原有兼容性测试恢复通过。

- 最终新增测试及 node_sync 回归测试 `-race` 通过（21.580s）。
- E2E 最终通过，记录目录 `/root/flvx-workers/runs/tunnel-redeploy-heal/e2e/run-20261006T064640Z/`：
  - (i) `ss -K` 触发 5 次入口 WebSocket 重连，间隔约 1.06–1.12 秒；合并为 1 次部署；出口监听/配置 2925 次采样无缺失，出口 DeleteService/DeleteChains 为 0，流量恢复。
  - (ii) 停止入口后重部署返回包含 `e2e-entry` 的错误，出口服务仍在；入口重启后自动恢复链及转发，真实流量通过。
  - (iii) 双出口第一出口离线时返回包含 `e2e-exit-offline` 的错误，健康出口与入口仍完成配置；一次失败候选尝试后流量及三次追加请求均经健康出口成功。
  - 请求来源由目标 HTTP 日志确认是出口 IP，排除入口直接转发；主机 `nft -s` hash 前后相同，所有自建 namespace、resolver 目录、测试 agent 配置及进程均清理。
  - 前三次 E2E 尝试停在 harness 初始化：隔离网络的公网探测超时，以及无默认路由入口不会输出公网 IPv4 成功日志。仅调整外部 harness 的独立 DNS 与就绪判定，产品/agent 未改；最终三个场景全部实际执行。

- 分支推送成功；CI Build Check `37425814691`（提交 `20941a11`）全部通过：后端、前端、agent 构建与 PostgreSQL 合同测试。fork.20 注解标签指向该已验证提交。

- Build and Push Images `37426077264` 全部成功，无重跑；Release 于 `2026-10-06T07:02:02Z` 发布，为正式版且 Latest。与 fork.19 相同的 10 项资产全部下载核验：v4/v6 compose 镜像 tag、两脚本 PINNED_VERSION/REPO、amd64/arm64 gost SHA256 均正确；详见 `release-verification.json`。

- 生产回滚点：`/opt/flvx-svc/rollback/pre-fork20-20261006T070315Z`，在线 SQLite 单次 backup 后 quick_check=ok。计数：user=12、node=25、tunnel=49、chain_tunnel=163、forward=26、forward_port=26、node_metric=1970473。旧镜像保留为 `local/flvxx-{backend,frontend}:pre-fork20-20261006T070315Z`；compose/.env/ROLLBACK-METADATA.md 完整；正常回滚仅恢复 fork.19 配置镜像，不需还原数据库。

- 生产于 `2026-10-06T07:03:52Z` 启动 fork.20 后端，前端于 `07:03:57Z` 启动；后端 healthy、前端 running，FLUX_VERSION=fork.20。管理 node/tunnel/forward list 均 code=0（25/49/26），node_metric 持续前进。节点重连已输出新的成功统计，未见入口失败导致出口清理的迹象；接下来执行限定诊断修复。
