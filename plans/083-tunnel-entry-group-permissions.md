# 083 — 隧道组按入口授权

基线：`58909738`；与 082 一起发布 `3.0.27-fork.34`。组成员显式保存入口，用户有效入口为组授权并集；管理员/直接授权仍有全部入口。配额、计费及独立展示分组保持原样。

- [x] 确认批准范围、生产迁移要求和计划编号。
- [x] 新增 tunnel_group_tunnel_entry 模型/迁移/仓储与 vite_config 标记的一次性幂等回填；覆盖兼容性测试。
- [ ] 用户入口授权并集、旧 assign payload 全选、新入口默认不授权；用户隧道数据和地址过滤。
- [ ] 规则创建/更新仅用允许入口；组/成员/权限/隧道入口变动收敛规则端口和 runtime，新增端口失败记录并报告。
- [x] 分组页面多入口勾选及 API 类型，单入口外观保持原样。
- [ ] 授权/撤权/补端口/新增入口/admin/direct/旧 payload 回归测试。
- [ ] 生产 SQLite 在线副本 gate：每用户可见隧道、每规则入口集合、所有 forward_port 零差异，记录回填行数。
- [ ] 与 082 一起通过全部发布门禁和截图检视。
- [ ] 备份写明新表和回填标记的回滚方式；上线后 schema 仅增加新表、现有授权与端口零差异。
- [ ] 中文总结含新增广港入口和组勾选用法；最终计划提交和推送。

## WIP 门禁停止（2026-10-09）

新增功能定向测试、完整路由 JWT 测试、后端 build/vet、前端 build 均通过；lint 与 fork.33 同为 96 个错误，无新增。全量 Go 测试为 26 项失败（含父/子测试），原基线 15 项 + 新增 11 项。按任务硬门禁停止后续验证、标签、发布、生产升级和 OTA，保留 WIP 分支。入口权限实现及现有契约兼容性尚未完成。

新增失败与原始日志：`/root/flvx-workers/runs/entry-groups-latency/baseline-comparison.json`、`new-failures.log`；完整状态见该目录 `summary.md`。生产保持 fork.33，未执行生产回填。

新增失败：
- `TestForwardCreateInheritsUserSpeedLimitContract`
- `TestForwardMainlandLandingRejectionContracts`
- `TestForwardMainlandLandingRejectionContracts/create`
- `TestForwardMainlandLandingRejectionContracts/update`
- `TestIssue349_ForwardListFormatsIPv6EntryAddressesContract`
- `TestNodeUpdateRewritesTunnelAndRuleList`
- `TestTunnelBatchDeleteWithForwardsReturnsTunnelLevelFailuresContract`
- `TestTunnelDeleteWithForwardsReplaceReturnsFailureDetailsContract`
- `TestTunnelRegionUserPrivacyContract`
- `TestUserTunnelVisibleListContracts`
- `TestUserTunnelVisibleListContracts/normal_user_sees_enabled_assigned_tunnels_regardless_of_user_tunnel_status`
