# 隧道协议过滤按服务隔离修复计划

## 范围

本计划只修复协议过滤配置文件安全落盘、入口 GOST TCP forward 的隧道级隔离、失败 warning、支持范围提示及对应测试；不处理首读/分片绕过、UDP/nftables 过滤、tunnel-entrypoint relay 跳过或可观测性，也不做发布、部署或生产重启。

## 混合版本兼容

- Panel 继续发送现有 `SetProtocol` 命令，使 fork.7/fork.8 等旧 agent 保持原有节点全局行为。
- Panel 同时把隧道的四个过滤值显式写入每个入口 forward service（包括四项全为 0），新 agent 优先使用 service 自身值，不受 legacy 全局值覆盖或泄漏。
- 新 agent 对没有携带 service 级过滤标记的旧配置保留 legacy 全局回退；节点重连/重新下发 forward 时，现有 forward 重建路径会带上隧道值并自动收敛。
- service 四项全为 0 时不包装连接，避免 detector 开销。

## 清单

- [x] 从 `2991b893` 创建 `maintenance/3.0.27-fork.9-protocol-filter`，记录范围与混合版本兼容策略。
- [x] 安全保真地持久化 agent 协议配置，并添加未知字段、原子替换和权限单元测试。
- [ ] Panel 在入口 forward service 中携带隧道过滤值，更新时复用 forward sync 并向调用方返回离线/失败 warning；添加生成配置测试并核实重连收敛路径。
- [ ] Agent 按 service 使用过滤值，零值不包装；添加同节点双 service 隔离和 legacy 命令不覆盖测试。
- [ ] 在隧道表单添加仅支持 GOST 模式 TCP forward 的文字说明。
- [ ] 运行 go-backend、go-gost、go-gost/x 全量 Go 测试和前端构建，并将后端失败与 `2991b893` 基线比较。
- [ ] 推送分支并完成实施记录（不发布、不部署）。
