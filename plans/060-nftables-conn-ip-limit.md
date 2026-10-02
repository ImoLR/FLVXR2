# nftables 规则连接/IP 限额与用户共享池

## 设计

- 复用 059 协议。panel 在 `AddNftablesRules` 的单条规则中附加规则 `max_connections`、`max_client_ips`，及用户 `quota_group`、`group_max_connections`、`group_max_client_ips`。组预算与 `SetQuotaGroups` 一致：`-1` 不限，`0` 已满；规则 `0` 不限。旧 agent 忽略未知 JSON 字段，仍按原逻辑转发。
- 只在入口节点安装限制。单条 forward 的 TCP/UDP 入口共用一个 nft filter chain，在 DNAT 前匹配原始入口端口；`ct count over N` 限制新连接，已有连接继续通过。每条规则的活跃 IP、用户组 nft 活跃连接数与去重 IP 由 agent 每秒读取 conntrack（IPv4、IPv6）计算。仅对有规则 IP 限额或用户池预算的 nft 入口启动轮询。
- agent 把 nft 用量合并到 059 的 quota group，使 gost 接入检查与每秒 `quotaGroups` 报告都看到 gost+nft 的连接总数、去重 IP 集合。nft 的用户连接门槛用本节点 gost+nft 合计用量与该节点预算比较；规则 IP 门槛和用户 IP 门槛在额度已满时，仅允许已活跃来源 IP。所有门槛只处理 `ct state new`；对 nft 集合和门槛的更新在一个 netlink transaction 内完成，不刷新整个 ruleset。
- panel 的 quota target 查询包含 nft 入口节点；用户总额变更及规则编辑复用既有重同步路径。链式隧道在入口应用门槛，后续节点不计入该入口的配额。无任何限制的转发不增加门槛规则、IP 集合或 conntrack 轮询。
- 精度：规则连接门槛由内核 `ct count over N` 逐连接判断；IP 与用户池经约一秒采样后生效，采样间隔内新连接可能短暂超额。名额在 conntrack 项消失后释放；当前内核默认 TCP `CLOSE` 10 秒、`TIME_WAIT` 120 秒、被丢弃的 `SYN_SENT` 120 秒，UDP 未回复 30 秒、已回复 120 秒（内核参数可调整）。被丢弃的 SYN 也可能暂时占用 connlimit 追踪项。跨节点预算还需 panel 下一轮上报与下发才收敛。旧入口 agent（上游 3.0.27/3.0.28）继续转发，但 nft 限额在升级到 fork.13 前不生效，也不会上报 nft 池用量。

## 实施清单

- [x] 写入设计文档，核对 059 协议与隔离测试方式。
- [x] panel 下发 nft 配额字段并将 nft-only 入口列入预算目标，补充单元测试。
- [x] agent 将 nft 用量并入既有 quota group，补充合并/预算测试。
- [x] agent 安装 nft 连接/IP/用户池门槛并轮询 conntrack，补充解析、归属和门槛测试。
- [x] 在 netns 完成真实 TCP 限额和共享池测试，更新 UI/059 说明。
- [x] 运行要求的各项目测试、比对既有失败、审查范围。
- [ ] 推送实施分支并写中文总结（不发布、不部署）。

## 验证记录

- `go-backend`: `go test ./...` 已运行；失败测试/子测试名称与 `/root/flvx-fork10/base.fails` 的 19 项基线精确一致，无新增失败。新测试确认 type 1/type 2 nft payload 的规则限额与组预算、nft-only 入口收到 `SetQuotaGroups` 预算。
- `go-gost`: `go test ./...` 通过。
- `go-gost/x`: `go test ./...` 通过，`go test -race ./service` 通过；新单元测试覆盖旧 JSON 兼容、TCP/UDP conntrack 归属、规则/组门槛和 gost+nft 去重上报。
- `vite-frontend`: `npm run build` 通过。
- `go-gost/x/nftables/netns_integration.sh`: 全部集成测试通过；本轮新增测试覆盖无限额零门槛开销、TCP 连接上限 2 与关闭后释放、IPv4/IPv6 TCP 与 UDP 来源 IP 上限、TCP+UDP 共用一条规则连接上限、两条 nft 规则共用用户连接池及预算 `0`/`-1` 更新。gost+nft 同节点共享池由 service 单元测试验证，未在 netns 中启动完整 gost 服务。
- 所有 nftables/conntrack 写入均在脚本创建的临时 client、entry、target netns 内完成，退出后清理。脚本仅在临时 entry netns 缩短 TCP conntrack 超时以测试释放；主机默认参数保持 `CLOSE=10`、`TIME_WAIT=120`、`SYN_SENT=120` 秒。

## 发布轮次待办（本轮不执行）

- 待单独的 fork.13 发布轮次完成：标注 tag、验证 CI/镜像/发布资产；按既有流程备份生产 DB/compose/镜像并部署 panel；由用户安排入口 agent 升级，旧 agent 的 nft 转发继续工作但不执行新增 nft 限额。
