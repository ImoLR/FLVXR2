# 079 — 路径分段会话内成功即停（fork.31）

基线：origin/maintenance/3.0.27-fork.30-probe-on-demand，5ba62422。

## 任务清单
- [x] 阅读发布记忆、项目约束、078 与 fork.30 提交，创建指定分支。
- [x] 实现全局需求会话、分段成功缓存/三次失败停止、五分钟过期及逐隧道渐进发布。
- [x] 实现规则弹窗前 30 秒每 2 秒轮询、之后每 10 秒与 40 秒测速占位。
- [x] fake ping/clock 测试 a–i、渐进发布与 race；build/vet/全量失败集对照 fork.30。
- [ ] 前端 tsc/build/PWA 门禁，桌面浅色和移动深色空/部分/完整截图并亲自检查。
- [ ] 推送并确认 HEAD CI，通过后发布 fork.31，核验 Latest 与全部固定版本资产。
- [ ] 保存升级前 15 分钟指标；在线备份、校验、prune 至两个备份；升级生产及健康检查。
- [ ] 生产 90 秒需求/停止后过期验证、渐进出现时间、逐轮探测计数、升级后 15 分钟指标比较。
- [ ] 中文总结、完成计划、提交并推送。

## 实现约定
全局会话由任意授权用户的延迟轮询维持，60 秒无请求后新请求重置分段状态。仅路径独占键缓存，OK 或三连败 settled 后 5 分钟过期；传统共享键继续 count=4。路径独占 count=2，并发上限保持 12/每源 3。若尚无完整 OK 路径且仍有未完成或不足三次失败的分段，保留之前值（新会话无值），不提前显示超时。每个完成分段触发相关隧道重新计算，成功路径立即显示并可被更快路径替换；所有相关分段 settled 后才能确认 timeout。历史/monitor/best-exit 不变。

运行记录：/root/flvx-workers/runs/probe-path-once/。仅一个重进程，临时目录均在此；不改业务数据、agent、依赖、安装脚本、其他栈或用户的 plans/048 文件。

截图发现现有 `/tunnel/user/tunnel` 摘要缺少 type；为让占位只出现在 type-2 条目，摘要查询最小补充现有数据库的 type 字段（管理员/普通用户均适用）。`/tunnel/user/latency` 的四字段响应、权限及所有业务数据不变。首轮截图脚本 DOM 序列化错误已修正，全部临时进程由 finally 清理。

最终后端门禁：build、handler/repository vet 通过；全量测试失败集与 fork.30 完全相同（15 项）；handler/repository 全包 race 无竞争，handler 仅 2 项既有失败，repository 通过。前端 tsc/build 通过，主 JS 2,755,342 B，低于 5 MiB。
