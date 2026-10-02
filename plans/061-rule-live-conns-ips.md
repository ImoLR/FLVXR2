# 规则实时带宽、连接数与来源 IP

## 设计

- gost 转发按秒上报增量；连接结束时停止并等待采样协程，再只补报未采样的余量。账单流量路径保持原样。
- panel 规则列表按去重入口节点汇总已有 service 连接数，并从 060 的 conntrack 归属取得 nft 连接数；实时带宽仍沿用现有指标。UI 在两种表格行和卡片展示同一实时数据。
- gost service 始终维护活跃来源 IP 的连接次数。agent 新增 `GetServiceClientIPs` 命令，单节点每 service 最多返回 500 个 IP 和真实总数；nft 直接复用 060 的 conntrack 归属。panel 新增管理员接口，按入口节点并发查询、聚合 IP、记录离线或旧 agent 错误。编辑弹窗高级功能展开时每 5 秒刷新，关闭即停止。
- 命令与接口均为只读，旧 agent 未识别命令时返回节点提示，不修改原有 ws 上报格式和配额语义。

## 清单

- [x] 修正 gost 关闭时带宽重复计数并补单元测试。
- [x] 修正 panel 多入口连接数汇总、去掉请求日志并补单元测试。
- [x] 实现 agent gost/nft 实时来源 IP 查询命令与单元测试。
- [x] 实现 panel 管理员聚合接口与权限、错误路径测试。
- [x] 实现规则表格、卡片和管理员高级功能实时显示。
- [x] 运行 Go/前端构建和可用的隔离 E2E，比对已知失败，审查范围并推送分支。

## 验证记录

- `go-backend`: 两次运行 `go test ./...`。第一次有 19 项已知失败及一次 `TestBatchAssignRollbackWhenLimiterDispatchFailsContract` 偶发失败；该测试单独重复两次通过。第二次全量失败名称与 `/root/flvx-fork10/base.fails` 的 19 项基线完全一致，无新增失败。
- `go-gost`: `go test ./...` 通过。`go-gost/x`: `go test ./...` 通过；`go test -race ./service ./handler/forward/local ./socket` 通过。`vite-frontend`: `npm run build` 通过。
- 基于 fork8 脚本在新建临时 netns 中运行本轮 panel/agent 二进制：gost 慢速传输期间规则上行速度非零（最高约 1.9 MB/s）、连接数为 1、管理员接口返回来源 `10.233.1.2`；关闭后速度归零，采样未见完整 8 MB 流量的关闭尖峰。nft 慢速传输期间同一接口返回来源 IP，无限额 nft 规则连接数为 1。临时 netns 均已清理；主机 nftables/conntrack 未改动。
- 检查了所有 `AddForwardTraffic` 调用：gost 本地处理器是唯一按秒加关闭补报的路径；nft 路径只上报增量。该统计写入 `ForwardStatsManager`，不进入计费使用的 `GlobalTrafficManager`。

## fork.13 发布轮次

- [x] 验证发布资产、CI、生产回滚点并升级 panel。
- [ ] 入口节点逐步升级到 fork.13 后检查：gost 关闭测速无尖峰、多入口连接数求和、nft 无额度规则连接数、管理员 IP 清单及非管理员拒绝、旧 agent 节点提示、059/060 限额与账单流量一致性。旧入口 agent 仍可上报原有 gost 速度/连接数，但保留旧版关闭尖峰；新 IP 命令与 nft 连接数需要 fork.13 agent。
