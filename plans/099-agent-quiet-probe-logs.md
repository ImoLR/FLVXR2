# 099 — Agent quiet probe logs / 3.0.27-fork.50

范围：只调整代理例行命令日志；保留 fork.47 脱敏。不改变面板 60 秒路径探测的间隔、次数、超时、重试、调度或 picker，不改前端、安装脚本、协议、配置或数据库结构。

基线：origin/maintenance/3.0.27-fork.49-orphan-sweep，d51691ef；独立工作树 `/root/projects/flvxx/flvxx-quiet-probe-logs`。生产开始版本已确认是 3.0.27-fork.49。

- [x] 阅读项目/发布/备份规则，确认基线、生产版本并创建独立工作树。
- [x] 审核 TcpPing/PipePing、诊断/响应、报告/心跳/ListServices 路径；实现成功静默、按命令和目标的首次失败/10 分钟汇总/恢复日志及测试。
- [x] 运行 backend 与 go-gost/x 全量测试（短 /root 临时目录），对比 15 项基线；触及包 go vet。
- [x] 推送分支、验证 CI，发布 annotated tag 3.0.27-fork.50，验证 Images、Latest 和 fork.49 同资产集合。
- [x] 备份生产面板（在线 DB 校验、配置、镜像和回滚脚本），prune 保留两份；升级并核对健康和在线节点。
- [x] Breadcloud JP canary：记录日志/版本和转发基线，备份并升级、验证重连/版本/转发/日志。
- [ ] 仅处理授权的其余 12 个探测源，逐台检查并记录；拒绝且无 SSH 的节点零改动跳过；首个重连或转发故障即停止并回滚。
- [x] 验证 picker 延迟继续约 60 秒更新，记录逐节点版本、日志率、转发和回滚；提交最终计划并写中文总结。

授权节点：Bestvm JP B、Bestvm HK 年付、Mkcloud 深港ixp 1、Datawave HK 2、Mkcloud 沪港ixp 新春版、Mkcloud 沪日ixp 440、vmsilo 沪日首发、Mkcloud 沪日ixp G口 bug鸡、Datawave TW 1、Bestvm TW、Breadcloud JP、火山广州、Dmit Nathan。canary 为 Breadcloud JP（ssh mmwx-s11）。原版 3.0.28 的版本比较先确认，禁止绕过面板为无 SSH 节点降级。

证据及最终总结：`/root/flvx-workers/runs/agent-quiet-probe-logs/`。主工作区、其他栈和记忆文件保持不动。

实施核查：fork.49 已排除 TcpPing/PipePing 成功命令日志；本次补上 ListServices、统一探测/响应失败记录（含部分丢包）、10 分钟计数汇总与恢复。联邦 Diagnose 最终发送 TcpPing。报告/心跳成功本来静默，配置定时上报的成功行已移除；HTTP 最终失败和 WS 报告失败限频，网络行为不变。

本地门禁：backend 全量失败集恰好为既有 15 项，无新增；go-gost/x 全量通过；go vet ./logsafe ./socket ./service 通过。证据 test-results.json / *-tests.jsonl。

发布进行中：实现 commit `4dca15f4`；分支 CI `38803590992` 四个任务均成功，新增日志测试 race 通过；annotated tag `3.0.27-fork.50` 已推送，Images `38803926106`。

生产前测：29/30 节点在线；节点 24 原本离线。picker API 48 条路径的 updatedAt 间隔 60.076 / 59.807 秒。Breadcloud JP SSH 回滚点 `/root/flvxx-backup-20261015T203958Z-pre-fork50/rollback.sh`；正式一小时样本 3,637 行（含验收诊断），其中探测 3,501 行；火山广州 43 行/小时，探测 0 行。其余无 SSH 节点无 journal 行数接口，禁止将估算伪称实测。

发布验收：Images `38803926106` 全绿，fork.50 为 Latest/非 prerelease，10 个资产与 fork.49 一致，amd64/arm64 SHA256、compose 镜像引用、安装脚本 PINNED_VERSION 均通过。生产在线备份 `/opt/flvx-svc/rollback/pre-fork50-20261015T210001Z`，quick_check=ok，已运行 prune，仅保留 pre-fork50 与 pre-fork49；移除 pre-fork48 及其 fork.47 镜像。

生产面板已升至 fork.50：backend healthy、frontend nginx/version.json/API 通过；schema/业务表计数不变，29/30 在线，升级前在线的 29 台均在重启后继续上报。

节点 21（Breadcloud JP）：3.0.28 (debian/amd64) → 3.0.27-fork.50 (debian/amd64)；panel OTA，verified；重连 4.1 秒；诊断新增失败 0。证据 `node-21-ota.json`。

Canary 降噪实测：新版本稳定后的连续 120 秒，flvxx journal 总行数=0、探测行数=0（归一化 0 行/小时）；升级前一小时 3,637 行。面板正常接受原版 3.0.28→fork.50，无需 SSH 替换二进制；10 条隧道诊断 37 项成功与 4 项既有离线失败均保持不变。picker 后续轮次间隔 59.946 / 60.096 秒。

节点 18（Bestvm JP B）：3.0.27 (debian/amd64) → 3.0.27-fork.50 (debian/amd64)；panel OTA，verified；重连 4.0 秒；诊断新增失败 0。证据 `node-18-ota.json`。

节点 42（Bestvm HK 年付）：3.0.27 (debian/amd64) → 3.0.27-fork.50 (debian/amd64)；panel OTA，verified；重连 4.1 秒；诊断新增失败 0。证据 `node-42-ota.json`。

节点 2（Mkcloud 深港ixp 1）：3.0.27-fork.27 (debian/amd64) → 3.0.27-fork.50 (debian/amd64)；panel OTA，verified；重连 4.1 秒；诊断新增失败 0。证据 `node-2-ota.json`。

节点 15（Datawave HK 2）：3.0.27 (debian/amd64) → 3.0.27-fork.50 (debian/amd64)；panel OTA，verified；重连 4.0 秒；诊断新增失败 0。证据 `node-15-ota.json`。

节点 32（Mkcloud 沪港ixp 新春版）：3.0.27-fork.27 (debian/amd64) → 3.0.27-fork.50 (debian/amd64)；panel OTA，verified；重连 4.0 秒；诊断新增失败 0。证据 `node-32-ota.json`。

**OTA 批次已按停止规则终止：节点 23（Mkcloud 沪日ixp 440）在 fork.27→fork.50 后约 4 秒重连，但转发 124「JP-SSH」从节点 23 到 160.21.75.136:22 在两轮完整诊断及单独复核中均由成功变为失败；同转发入口 51 仍成功。其余基线成功检查保持通过。** 7 台已安装 fork.50，其中前 6 台验收通过；节点 23 标记诊断回归。51/24/26/12/52/49 共 6 台未 OTA。

回滚阻塞：现有 SSH 配置/任务凭据无节点 23 入口，直连 root@163.53.55.158:22 返回 Network is unreachable。面板只有当前版本升级 API，没有代理 rollback 路由；node/deploy/rollback 是配置回滚，不能用于二进制。未添加端点、未临时伪造面板版本、未修改受保护的其他服务。节点 23 旧 fork.27 的 .old 回滚还需处理 -C/tls 兼容性，控制台命令写入最终总结；本次未能实际回滚。

停止批次后的核验：29/30 在线且指标新鲜，未OTA节点版本不变；picker 48条路径继续约60秒刷新，48个entry延迟实值发生变化。Breadcloud JP稳定600秒日志总行数/探测行数均为0。中文总结与节点23控制台回滚脚本已写入run目录。OTA事项保持未完成：节点23回滚受阻，余下6台未升级；没有擅自继续批次。

第二轮（2026-10-15）：继续本计划，代码与已发布的 fork.50 不变。审阅已确认 tcpPingHost/routeCommand 的拨号行为未变；23→160.21.75.136:22 异常需要以同目标其他端口/来源及节点 51 升级后的结果判断，不能仅凭发生时间认定代理回归。不操作目标主机或转发配置，不重新发布、升级面板、创建生产备份或 prune。

- [x] 读取两份操作记忆、上一轮总结/停止记录/清单及原有辅助脚本，确认现有工作树与分支。
- [x] 各复测一次转发 124/125，记录时间与 23/51 的源和端口对照。
- [x] 52：验证原有 SSH 备份，日志前测，逐台 OTA 与前后诊断、版本/重连核验。
- [x] 51：逐台 OTA 与前后诊断，重点确认 fork.50 下到目标 :22 的结果。
- [x] 26：按顺序逐台 OTA 与前后诊断、版本/重连核验。
- [x] 12：按顺序逐台 OTA 与前后诊断、版本/重连核验。
- [x] 49：按顺序逐台 OTA 与前后诊断、版本/重连核验。
- [ ] 24：轮到时仅在线才 OTA，否则零操作跳过。
- [ ] 批次后复测 48 条延迟路径约 60 秒刷新；21 的 10 分钟 journal 和 52 日志后测。
- [ ] 更新两轮合并中文总结，推送计划文档并确认最终文档 CI 全绿。

第二轮证据继续存于 `/root/flvx-workers/runs/agent-quiet-probe-logs/`，文件名统一 `r2-` 前缀；两轮合并总结写入 `/root/flvx-workers/runs/agent-quiet-probe-logs-r2/summary.md`。

便宜复核（UTC）：124 于 2026-10-15 21:27:01.354（epoch 1792099621.354）开始，23→:22 仍失败、51→:22 成功；125 于 21:27:02.079（1792099622.079）开始，23/51→:51015 均成功。异常仍局限于节点 23 与目标 160.21.75.136 的端口 22 组合；证据 `r2-forward-124-confirm.json`、`r2-forward-125-confirm.json`。最终代理版本对照等待节点 51 OTA 后结果。

52「火山广州」：原有 `/root/flvxx-backup-20261015T205239Z-pre-fork50` 的二进制 SHA256 与记录及当前 fork.48 均一致，配置 JSON/回滚脚本校验通过。第二轮升级前一小时日志总 15、探测 0（第一轮另一窗口为 43/0）。面板 OTA fork.48→fork.50，下载/请求 93.7 秒、返回后重连 4.1 秒；隧道 80 的 2 项诊断前后全通过，SSH 版本与 active 状态再次确认。回滚命令：`ssh leikwan-vps 'bash /root/flvxx-backup-20261015T205239Z-pre-fork50/rollback.sh'`；未执行回滚。证据 `r2-node-52-{backup-valid,logs-before,ota,diagnose-before,diagnose-after}.json`。只操作 flvxx 代理，未操作 LeiKwan relay/bridge。

51「vmsilo 沪日首发」：面板 OTA fork.37→fork.50，返回后 4.0 秒重连；11 个活跃转发及 12 条关联隧道的诊断前后均为 77 项成功/5 项既有失败，新增失败 0。fork.50 下 51→160.21.75.136:22 仍成功，23→:22 仍失败，23/51→:51015 均成功。结合审阅确认拨号逻辑未变，节点 23 的特定源/端口故障不认定为本次代理回归；最可能是目标端针对源 IP 的 SSH 访问限制（如 fail2ban），具体机制未登录目标确认。本轮不需要因该现象回滚节点 23，第一轮的“优先回滚 23”建议已被本轮验证结论取代。证据 `r2-node-51-{ota,diagnose-before,diagnose-after}.json`。51 的旧二进制回滚须有控制台/SSH：`systemctl stop flvxx && cp -a /etc/flvxx/flvxx.old /etc/flvxx/flvxx && systemctl start flvxx`；无面板降级路径，未执行回滚。

51 完成 OTA 后的独立确认：2026-10-15 21:33:14.756 UTC（1792099994.756）开始的转发 124 诊断，51→:22 成功，平均 26.649 ms；23→:22 仍失败。证据 `r2-forward-124-after-node51.json`（包含面板上报 fork.50 版本及服务端 timestamp）。

26「Datawave TW 1」：原版 3.0.28→fork.50，面板 OTA 接受，返回后 4.1 秒重连；1 个活跃转发、14 条隧道的诊断前后均为 106 项成功/5 项既有失败，无新增失败。旧失败仅 tw.zjweu.cn:57347 和节点 24 离线路径。证据 `r2-node-26-{ota,diagnose-before,diagnose-after}.json`；无 SSH，日志行数与 `.old` 完整性未现场核验，旧版回滚需控制台并处理 -C/tls 兼容性。

12「Bestvm TW」：原版 3.0.28→fork.50，面板 OTA 接受，返回后 4.0 秒重连；1 个活跃转发、14 条隧道的诊断前后均为 106 项成功/5 项既有失败，无新增失败。证据 `r2-node-12-{ota,diagnose-before,diagnose-after}.json`；日志/旧二进制核验及控制台回滚限制同节点 26。

49「Dmit Nathan」：fork.22→fork.50，面板 OTA，返回后 4.0 秒重连；转发 109 与隧道 74 的诊断前后均为 3 项成功/1 项既有失败，无新增失败。该既有失败为出口 AWS HK→boilhinet.imgamer.top:10020 等待响应超时，已在本节点 OTA 前记录；没有把它算成升级故障。证据 `r2-node-49-{ota,diagnose-before,diagnose-after}.json`；无 SSH，旧 fork.22 的控制台回滚仍需处理 -C/tls 兼容性。
