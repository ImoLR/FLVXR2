# 116 — 发布 fork.61：线路机与规则落地节点

用户已于 2026-10-20 批准发布。时间统一台湾时间（UTC+8）。
从 `3.0.27-fork.60` 创建 `maintenance/3.0.27-fork.61-line-chain-target`，
顺序移入 feature 分支的 33 个提交，保留 fork.59/60 的监控与在线 IP 行为。
只发布既有 plan 108 / 113 功能，不开启线路机，只 OTA 一台合适的出口节点。

## 检查清单

- [x] 阅读发布流程、两份功能总结，确认工作树、分支与授权。
- [x] 合入 33 个提交，解决冲突；113 文档改名为 115，仅更新标题和自引用；推送发布分支。
- [x] 在干净 fork.60 临时工作树取得后端完整测试基线。
- [x] 发布代码后端全量测试无新增失败；agent landing/chain 测试及改动包 vet 通过。
- [x] 前端 tsc、改动文件 ESLint 无新增错误、构建通过。
- [x] 本机落地验收通过（含 T1/R1、在线 IP），链式验收通过（含 C6/C8）。
- [x] 重拍并检查 3–4 张关键页面，记录节点列表查询次数。
- [x] Tag fork.61；CI / Images 通过；Latest 与 fork.60 相同资产集合核验。
- [x] 生产升级前记录配置表计数/哈希、在线节点、RSS；验证数据库备份与回滚脚本，执行两份备份保留规则。
- [x] 升级生产面板；验证迁移、节点上报、node 21 在线 IP、日志及资源。
- [x] 唯一出口 canary SSH 备份与恢复脚本就绪；记录诊断及 CPU/RSS 后 OTA，复验转发与无 Core 下载。
- [ ] 生产验收 node 21 + shared 7 / 5 通过；临时用户及节点清理；原配置与计费不变。
- [ ] 清理本任务临时环境和数据库副本；115/116 完成；提交推送，中文总结写入指定文件。

## 证据

运行目录：`/root/flvx-workers/runs/release-fork61-line-chain-target/`。
生产不做 chain-rule smoke：开启线路机由用户决定，链式流量由本机真实 Core 环境验收。

- 2026-10-20：33 个提交按序 cherry-pick，无 merge commit；处理器冲突保留 landingRates 与 landingTargetLocks。另保留 fork.60 发布后验收脚本修复 99a2ab6d（新样本与计费稳定等待），合并其连接上限恢复逻辑。113 改为 115。发布分支已 push -u origin；所有新提交尾部保留 Codex trailer。

- 前端：fork.60 / 发布代码 tsc 均通过，build 通过；改动文件 ESLint errors 25→25（新增0），warnings 666→707。4 张重拍 screenshots 已逐张检查，18 项交互通过，页面错误0；在线 IP 明细只在点击/手动刷新时请求。后端与 agent 所有改动包 go vet 通过。证据：frontend/report.md、screens/、vet-results.json。

- 干净 fork.60 后端基线完成：10 个顶层失败、含子测试共 15 名称（baseline-failures.json）。节点列表查询实测：1/6 个节点均 native 7 次、shared 8 次，管理员/用户一致。agent landing/dialer/socket 全部通过，含真实固定 Core 的配置和热更新；首次测试因 TMPDIR 过长 Unix socket 与缺 Geo 数据失败，改用 /root/f61tmp 和已有固定 Geo 后完整重跑通过，未修改产品代码。

- 后端全量 -count=1 / GOMAXPROCS=2 / -p 1 完成：15 个失败名称与 fork.60 完全相同，新增/消失均0（backend-comparison.json）。独立合并审查通过：保留双方行为，没有 plan112 或额外产品修改（integration-review.md）。

- 本机落地 13 场景 / 96 检查全通过（S1–S8/E1–E3/T1/R1），包含 fork.60 两来源在线 IP；链式 C1–C8 全通过，50 条规则、1000 次连接0失败、C8各删除路径资源清零。两个实验室进程/网络命名空间均已自动退出，链式完整数据 logs/chain-results.json。全部 tag 前 gates 完成。

- 2026-10-20 20:27：tag `3.0.27-fork.61` → `9591487e` 已推送，CI `39422343694` 成功，Images `39422345431` 构建中。20:29 节点47 SSH备份 `/root/flvxx-backup-20261020T122908Z-pre-fork61/restore-agent.sh` 校验通过；远端保留该份与 pre-fork56，删除 pre-fork55。OTA尚未执行。升级前30秒CPU 0.2666%、RSS 59,800 KiB，无Core目录/服务，其他面板PID634。

- 2026-10-20 20:29：生产回滚点 `/opt/flvx-svc/rollback/pre-fork61-20261020T122949Z` 已备份 compose/env、SQLite quick_check=ok、旧镜像及恢复脚本；已立即执行 prune，只保留 pre-fork61 / pre-fork60，删除 pre-fork59 与 fork58旧镜像。备份计数29/12/43/31/143，landing3/1；升级前RSS 35,928 KiB，29/29在线。完整表哈希从已验证备份采样，不另留生产DB副本。

- 2026-10-20 20:45：CI `39422343694` 与 Images `39422345431` 全绿，tag SHA 精确匹配。Release 为 Latest/正式版，10资产与fork60一致；两架构镜像、两份compose、安装脚本固定版本/仓库及两个gost SHA256全部通过（release-validation.json，16项）。

- 2026-10-20 20:46：生产前后端已切fork61且healthy，29/29在线、29台均有重启后上报，agent版本尚未变化。两链式表均0行，node.landing_chain=0、line_machine=false，landing_server.chain_only=false，idx_landing_reuse五列顺序正确。初始RSS40,372 KiB，node21 native server在线；真实在线IP留待production runner临时节点验证。

- 2026-10-20 20:48：只OTA节点47 AWS HK（隧道73/74/80出口）。fork56→fork61，request1.8秒、4秒重连；9项诊断14探测前后全通过。只有47上报landing_chain=1，全部29节点line_machine=false。SSH30秒CPU 0.3332%→0.2666%，RSS62,240→62092 KiB；无landing目录/文件，Core unit仍not-found，没有Core下载。另一面板flux_agent PID634不变。证据ota/与prod-panel-verified-ota.json。

- 2026-10-20 20:49：production runner第一组P3已实证node21的临时SS节点69、Reality节点71：onlineIps=1，明细真实来源104.145.236.29且connections=1，sampleAt为本轮，monitor计数一致。面板错误/panic/fatal均0；资源辅助脚本原redeploy_fail_lines误计failed=0成功行，已收窄正则，仅修运行目录测量脚本，不改产品。后续仍须完成两组生产验收与最终哈希。
