# 079 — 路径分段会话内成功即停（fork.31）

基线：origin/maintenance/3.0.27-fork.30-probe-on-demand，5ba62422。

## 任务清单
- [x] 阅读发布记忆、项目约束、078 与 fork.30 提交，创建指定分支。
- [x] 实现全局需求会话、分段成功缓存/三次失败停止、五分钟过期及逐隧道渐进发布。
- [x] 实现规则弹窗前 30 秒每 2 秒轮询、之后每 10 秒与 40 秒测速占位。
- [x] fake ping/clock 测试 a–i、渐进发布与 race；build/vet/全量失败集对照 fork.30。
- [x] 前端 tsc/build/PWA 门禁，桌面浅色和移动深色空/部分/完整截图并亲自检查。
- [x] 推送并确认 HEAD CI，通过后发布 fork.31，核验 Latest 与全部固定版本资产。
- [x] 保存升级前 15 分钟指标；在线备份、校验、prune 至两个备份；升级生产及健康检查。
- [ ] 生产 90 秒需求/停止后过期验证、渐进出现时间、逐轮探测计数、升级后 15 分钟指标比较。
- [ ] 中文总结、完成计划、提交并推送。

## 实现约定
全局会话由任意授权用户的延迟轮询维持，60 秒无请求后新请求重置分段状态。仅路径独占键缓存，OK 或三连败 settled 后 5 分钟过期；传统共享键继续 count=4。路径独占 count=2，并发上限保持 12/每源 3。若尚无完整 OK 路径且仍有未完成或不足三次失败的分段，保留之前值（新会话无值），不提前显示超时。每个完成分段触发相关隧道重新计算，成功路径立即显示并可被更快路径替换；所有相关分段 settled 后才能确认 timeout。历史/monitor/best-exit 不变。

运行记录：/root/flvx-workers/runs/probe-path-once/。仅一个重进程，临时目录均在此；不改业务数据、agent、依赖、安装脚本、其他栈或用户的 plans/048 文件。

截图发现现有 `/tunnel/user/tunnel` 摘要缺少 type；为让占位只出现在 type-2 条目，摘要查询最小补充现有数据库的 type 字段（管理员/普通用户均适用）。`/tunnel/user/latency` 的四字段响应、权限及所有业务数据不变。首轮截图脚本 DOM 序列化错误已修正，全部临时进程由 finally 清理。

最终后端门禁：build、handler/repository vet 通过；全量测试失败集与 fork.30 完全相同（15 项）；handler/repository 全包 race 无竞争，handler 仅 2 项既有失败，repository 通过。前端 tsc/build 通过，主 JS 2,755,342 B，低于 5 MiB。

截图门禁：桌面 1440 浅色 + 手机 390 深色，空/部分/完整共 6 张，已逐张检查。48 个 type-2 占位依次降至 24、0，type-1 不显示；选择器 DOM 保持同一实例、无水平溢出、2 秒→10 秒轮询切换及关窗停止通过。预览 `/latency-fill/` 的中文图注明确为模拟数值；隔离网络仅 loopback，所有写路由阻断，临时服务已自动清理。

发布：HEAD CI 37792058661 通过；annotated tag 3.0.27-fork.31 → b2d97a3f；镜像/发布流水线 37792406274 全部通过。Release 为 Latest、非预发布，10 个资产与 fork.30 一致；两份 compose 镜像、两份脚本 PINNED_VERSION/REPO 和两架构 gost SHA256 均校验通过。

生产升级：2026-10-08 14:38:25Z 后端启动 fork.31，healthy，前端 fork.31 运行；node/tunnel/forward 列表均 code=0，node_metric 持续推进。备份 `/opt/flvx-svc/rollback/pre-fork31-20261008T143752Z` quick_check=ok，含 compose/.env、全表计数和 fork.30 本地镜像标签。prune 保留 pre-fork30 + pre-fork31，删除 pre-fork29 及 fork.28 本地镜像。生产 JS 已确认含“测速中”（2,757,339 B）。

生产需求验证：启动期会话为 104→1→9→0（9 个新进入计划的键），首次/全部值 24.002/40.005 s，受到在途传统 23.899 s 慢轮次和启动可用路径变化影响。待会话过期后稳定重测 90 s：首轮 113，随后 14 轮均 0；2.003 s 首批、9.002 s 全部 48 条（37 ok / 11 timeout），第 10–90 秒每次均 48 条且时间戳持续刷新。普通用户 3 中途查询 13.334 ms 返回其 12 条，四字段和权限集合精确匹配，没有新增路径 ping。最后轮询后 71.849 s 记录 inactive（包括当前传统轮次完成时间）；60 s 后无路径 ping。连接数 15 分钟后窗口仍在采集。
