# 099 — Agent quiet probe logs / 3.0.27-fork.50

范围：只调整代理例行命令日志；保留 fork.47 脱敏。不改变面板 60 秒路径探测的间隔、次数、超时、重试、调度或 picker，不改前端、安装脚本、协议、配置或数据库结构。

基线：origin/maintenance/3.0.27-fork.49-orphan-sweep，d51691ef；独立工作树 `/root/projects/flvxx/flvxx-quiet-probe-logs`。生产开始版本已确认是 3.0.27-fork.49。

- [x] 阅读项目/发布/备份规则，确认基线、生产版本并创建独立工作树。
- [x] 审核 TcpPing/PipePing、诊断/响应、报告/心跳/ListServices 路径；实现成功静默、按命令和目标的首次失败/10 分钟汇总/恢复日志及测试。
- [x] 运行 backend 与 go-gost/x 全量测试（短 /root 临时目录），对比 15 项基线；触及包 go vet。
- [ ] 推送分支、验证 CI，发布 annotated tag 3.0.27-fork.50，验证 Images、Latest 和 fork.49 同资产集合。
- [ ] 备份生产面板（在线 DB 校验、配置、镜像和回滚脚本），prune 保留两份；升级并核对健康和在线节点。
- [ ] Breadcloud JP canary：记录日志/版本和转发基线，备份并升级、验证重连/版本/转发/日志。
- [ ] 仅处理授权的其余 12 个探测源，逐台检查并记录；拒绝且无 SSH 的节点零改动跳过；首个重连或转发故障即停止并回滚。
- [ ] 验证 picker 延迟继续约 60 秒更新，记录逐节点版本、日志率、转发和回滚；提交最终计划并写中文总结。

授权节点：Bestvm JP B、Bestvm HK 年付、Mkcloud 深港ixp 1、Datawave HK 2、Mkcloud 沪港ixp 新春版、Mkcloud 沪日ixp 440、vmsilo 沪日首发、Mkcloud 沪日ixp G口 bug鸡、Datawave TW 1、Bestvm TW、Breadcloud JP、火山广州、Dmit Nathan。canary 为 Breadcloud JP（ssh mmwx-s11）。原版 3.0.28 的版本比较先确认，禁止绕过面板为无 SSH 节点降级。

证据及最终总结：`/root/flvx-workers/runs/agent-quiet-probe-logs/`。主工作区、其他栈和记忆文件保持不动。

实施核查：fork.49 已排除 TcpPing/PipePing 成功命令日志；本次补上 ListServices、统一探测/响应失败记录（含部分丢包）、10 分钟计数汇总与恢复。联邦 Diagnose 最终发送 TcpPing。报告/心跳成功本来静默，配置定时上报的成功行已移除；HTTP 最终失败和 WS 报告失败限频，网络行为不变。

本地门禁：backend 全量失败集恰好为既有 15 项，无新增；go-gost/x 全量通过；go vet ./logsafe ./socket ./service 通过。证据 test-results.json / *-tests.jsonl。
