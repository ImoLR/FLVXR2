# nftables 规则连接/IP 限额与用户共享池

## 设计

- 复用 059 协议。panel 在 `AddNftablesRules` 的单条规则中附加规则 `max_connections`、`max_client_ips`，及用户 `quota_group`、`group_max_connections`、`group_max_client_ips`。组预算与 `SetQuotaGroups` 一致：`-1` 不限，`0` 已满；规则 `0` 不限。旧 agent 忽略未知 JSON 字段，仍按原逻辑转发。
- 只在入口节点安装限制。单条 forward 的 TCP/UDP 入口共用一个 nft filter chain，在 DNAT 前匹配原始入口端口；`ct count over N` 限制新连接，已有连接继续通过。每条规则的活跃 IP、用户组 nft 活跃连接数与去重 IP 由 agent 每秒读取 conntrack（IPv4、IPv6）计算。仅对有规则 IP 限额或用户池预算的 nft 入口启动轮询。
- agent 把 nft 用量合并到 059 的 quota group，使 gost 接入检查与每秒 `quotaGroups` 报告都看到 gost+nft 的连接总数、去重 IP 集合。nft 的用户连接门槛用本节点 gost+nft 合计用量与该节点预算比较；规则 IP 门槛和用户 IP 门槛在额度已满时，仅允许已活跃来源 IP。所有门槛只处理 `ct state new`；对 nft 集合和门槛的更新在一个 netlink transaction 内完成，不刷新整个 ruleset。
- panel 的 quota target 查询包含 nft 入口节点；用户总额变更及规则编辑复用既有重同步路径。链式隧道在入口应用门槛，后续节点不计入该入口的配额。无任何限制的转发不增加门槛规则、IP 集合或 conntrack 轮询。
- 精度：规则连接门槛由内核逐连接判断；IP 与用户池经约一秒采样后生效，采样间隔内新连接可能短暂超额。TCP 正常关闭后要等 conntrack 项消失，默认 `TIME_WAIT` 约 120 秒；UDP 默认未回复约 30 秒、已回复约 120 秒（内核参数可调整）。跨节点预算还需 panel 下一轮上报与下发才收敛。旧入口 agent（上游 3.0.27/3.0.28）继续转发，但 nft 限额在升级到 fork.13 前不生效，也不会上报 nft 池用量。

## 实施清单

- [x] 写入设计文档，核对 059 协议与隔离测试方式。
- [x] panel 下发 nft 配额字段并将 nft-only 入口列入预算目标，补充单元测试。
- [x] agent 将 nft 用量并入既有 quota group，补充合并/预算测试。
- [ ] agent 安装 nft 连接/IP/用户池门槛并轮询 conntrack，补充解析、归属和门槛测试。
- [ ] 在 netns 完成真实 TCP 限额和共享池测试，更新 UI/059 说明。
- [ ] 运行要求的各项目测试、比对既有失败、审查范围、推送分支并写中文总结。
