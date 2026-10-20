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
- [ ] Tag fork.61；CI / Images 通过；Latest 与 fork.60 相同资产集合核验。
- [ ] 生产升级前记录配置表计数/哈希、在线节点、RSS；验证数据库备份与回滚脚本，执行两份备份保留规则。
- [ ] 升级生产面板；验证迁移、节点上报、node 21 在线 IP、日志及资源。
- [ ] 唯一出口 canary SSH 备份与恢复脚本就绪；记录诊断及 CPU/RSS 后 OTA，复验转发与无 Core 下载。
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
