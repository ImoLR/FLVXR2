# 098 — 历史孤儿服务清扫与多入口/OTA 回滚修复

基线：`origin/maintenance/3.0.27-fork.48-tunnel-delete-orphans` (`26ec310b`)；分支：`maintenance/3.0.27-fork.49-orphan-sweep`；发布：`3.0.27-fork.49`。
范围仅限任务四项；不改 UI、安装脚本、其他栈、memory；仅 OTA 节点 54。运行证据：`/root/flvx-workers/runs/orphan-sweep/`。

- [x] 阅读规则/流程，确认生产 fork.48、隔离工作树及基线。
- [ ] 实现只读 ListServices、旧 agent WS 快照回退、管理员孤儿扫描/清理与缺失服务报告；验证归属和联邦保护。
- [x] 修复多入口下发失败回滚，检查创建/更新/批量/隧道入口变化路径，补充故障节点测试。
- [x] 修复 OTA 每源停滞/总时限与镜像回退，补充停滞和涓流测试。
- [ ] backend / go-gost/x 全量、基线对照、定向 race/vet；真实 panel+agent 隔离 E2E（精确与旧版回退）；真实 JWT 非管理员 403。
- [ ] 推送分支、CI 通过、打 fork.49 标签；Images/Latest/资产核验。
- [ ] 生产备份/回滚脚本，prune 保留两个，升级面板并核验健康和节点回报。
- [ ] SSH 备份 54，面板 OTA fork.49，验证回连/-V/unit/RA/ListServices；其他节点不 OTA。
- [ ] 生产全在线节点 dry-run，审核后仅清理明确孤儿，再 dry-run；保存逐节点/未知/缺失报告。
- [ ] 中文总结含改动、测试、发布、备份、破坏性操作/回滚、建议；提交最终计划并推送。

## 记录

初始化：生产 `/opt/flvx-svc/.env` = `3.0.27-fork.48`；工作树已存在且干净，分支 HEAD = `26ec310b078b9f40be6271aec43198d739f62234`。主 checkout 未修改。

OTA：实际 GET 每源重试，30 秒无字节/5 分钟总上限，切换时重建临时文件和 hash；保留 checksum 比对规则，checksum 请求限制 30 秒。定向测试覆盖停滞、涓流、无响应头、HTTP 错误、空包、全部失败及 hash/文件重置，通过。只有已运行 fork.49 的 agent 后续 OTA 才受益。

多入口：forwardCreate 失败先尽力清理 runtime 再删除 DB；forwardUpdate 与 forwardBatchChangeTunnel 失败先按新 mode/ports 清理，再恢复旧字段、ports，并逐节点尽力恢复。补齐回滚时 mode/WG/限速等运行字段恢复。三入口第 2 节点失败测试（gost/nft 创建、单条/批量换隧道）通过。仓库只有 forwardCreate 调用 CreateForwardTx，批量创建复用该路径；隧道入口变化保留已提交的新 DB 归属，applyTunnelRuntime 已有 createdChains/createdServices 回滚，后续 forward 同步失败仅返回 warnings，不产生 DB 回滚后的孤儿，故不改。
