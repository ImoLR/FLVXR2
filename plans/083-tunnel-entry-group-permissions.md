# 083 — 隧道组按入口授权

基线：`58909738`；与 082 一起发布 `3.0.27-fork.34`。组成员显式保存入口，用户有效入口为组授权并集；管理员/直接授权仍有全部入口。配额、计费及独立展示分组保持原样。

- [x] 确认批准范围、生产迁移要求和计划编号。
- [x] 新增 tunnel_group_tunnel_entry 模型/迁移/仓储与 vite_config 标记的一次性幂等回填；覆盖兼容性测试。
- [x] 用户入口授权并集、旧 assign payload 全选、新入口默认不授权；用户隧道数据和地址过滤。
- [x] 规则创建/更新仅用允许入口；组/成员/权限/隧道入口变动收敛规则端口和 runtime，新增端口失败记录并报告。
- [x] 分组页面多入口勾选及 API 类型，单入口外观保持原样。
- [x] 授权/撤权/补端口/新增入口/admin/direct/旧 payload 回归测试。
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

## 第二轮：兼容性修复与剩余门禁

证据、临时文件和截图改存 `/root/flvx-workers/runs/entry-groups-latency-r2/`；延续 082/083，不扩大实施范围。

- [x] 将入口权限改为仅组来源授权的收窄过滤；无限制调用恢复 fork.33 路径，入口错误在原有校验之后返回；不改既有测试。
- [ ] 顺序完成 gate 1–3：范围、构建/vet、全量失败集合等于基线 15、handler race、agent build/socket 与新增功能测试。
- [ ] 顺序完成 gate 4–8：生产副本零差异、netns E2E、六张截图、前端构建/lint/体积、最终 HEAD CI。
- [ ] 全部门禁通过后记录新 30 分钟基线、发布、备份/裁剪、升级、仅 node 47 OTA、生产验证；完成 082/083 与中文总结。

第二轮 gate 1–3：全量失败集合与 fork.33 的 15 项完全一致，new/removed 均为空；handler 全包 race 无竞争，保留 2 项已知基线断言失败，功能及回归定向 race 另行验证。agent build/socket、新增功能测试通过；详见 r2 日志。

第二轮生产副本首次比较发现用户 4 的 11 条历史授权因额外关联当前组成员而被隐藏。已按现存 group_permission_grant 求入口并集，保留原 user_tunnel 授权，不清理生产历史数据；补充回归测试，并重新执行受影响门禁。
