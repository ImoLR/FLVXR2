# 098 — 历史孤儿服务清扫与多入口/OTA 回滚修复

基线：`origin/maintenance/3.0.27-fork.48-tunnel-delete-orphans` (`26ec310b`)；分支：`maintenance/3.0.27-fork.49-orphan-sweep`；发布：`3.0.27-fork.49`。
范围仅限任务四项；不改 UI、安装脚本、其他栈、memory；仅 OTA 节点 54。运行证据：`/root/flvx-workers/runs/orphan-sweep/`。

- [x] 阅读规则/流程，确认生产 fork.48、隔离工作树及基线。
- [x] 实现只读 ListServices、旧 agent WS 快照回退、管理员孤儿扫描/清理与缺失服务报告；验证归属和联邦保护。
- [x] 修复多入口下发失败回滚，检查创建/更新/批量/隧道入口变化路径，补充故障节点测试。
- [x] 修复 OTA 每源停滞/总时限与镜像回退，补充停滞和涓流测试。
- [x] backend / go-gost/x 全量、基线对照、定向 race/vet；真实 panel+agent 隔离 E2E（精确与旧版回退）；真实 JWT 非管理员 403。
- [ ] 推送分支、CI 通过、打 fork.49 标签；Images/Latest/资产核验。
- [ ] 生产备份/回滚脚本，prune 保留两个，升级面板并核验健康和节点回报。
- [ ] SSH 备份 54，面板 OTA fork.49，验证回连/-V/unit/RA/ListServices；其他节点不 OTA。
- [ ] 生产全在线节点 dry-run，审核后仅清理明确孤儿，再 dry-run；保存逐节点/未知/缺失报告。
- [ ] 中文总结含改动、测试、发布、备份、破坏性操作/回滚、建议；提交最终计划并推送。

## 记录

初始化：生产 `/opt/flvx-svc/.env` = `3.0.27-fork.48`；工作树已存在且干净，分支 HEAD = `26ec310b078b9f40be6271aec43198d739f62234`。主 checkout 未修改。

OTA：实际 GET 每源重试，30 秒无字节/5 分钟总上限，切换时重建临时文件和 hash；保留 checksum 比对规则，checksum 请求限制 30 秒。定向测试覆盖停滞、涓流、无响应头、HTTP 错误、空包、全部失败及 hash/文件重置，通过。只有已运行 fork.49 的 agent 后续 OTA 才受益。

多入口：forwardCreate 失败先尽力清理 runtime 再删除 DB；forwardUpdate 与 forwardBatchChangeTunnel 失败先按新 mode/ports 清理，再恢复旧字段、ports，并逐节点尽力恢复。补齐回滚时 mode/WG/限速等运行字段恢复。三入口第 2 节点失败测试（gost/nft 创建、单条/批量换隧道）通过。仓库只有 forwardCreate 调用 CreateForwardTx，批量创建复用该路径；隧道入口变化保留已提交的新 DB 归属，applyTunnelRuntime 已有 createdChains/createdServices 回滚，后续 forward 同步失败仅返回 warnings，不产生 DB 回滚后的孤儿，故不改。

扫描：`POST /api/v1/node/orphan-sweep`，默认 dry-run；`{"execute":true}` 执行，`nodeIds` 选节点，`targets:[{nodeId,kind,name}]` 限定已审核名称。严格匹配 FLVX 名称，仅清理 DB 不存在或不属于该节点的对象；每次删除前重读归属。共享/远程/历史联邦记录及 fed_ 标记节点跳过，未知名称仅报告。新 agent 返回 registry services/chains/hops/limiters、静态 nft rule IDs、FLVX WG 接口/配置。旧 agent 用当前 WS 会话最近 30 秒 serviceConnections 快照；重连清空旧快照。
已核实普通 GOST service 解析统一使用 defaultService，具备 CurrentConns；自定义不实现该方法的服务不会出现在旧快照。链内 hop 随链删除，独立注册 hop 无独占归属，仅报告。旧 nft 统计只包含有连接的 id（conntrack 错误时也可能缺失），不能宣称旧快照 nft 全覆盖；API 明示覆盖限制，反向缺失仅报告不修复。
验证：backend 全量与 fork.48 同为 15 失败、新增 0；go-gost/x 全量通过；handler/ws/repo 与 socket/nftables 定向 race、vet 通过。真实 JWT 全 router 非管理员 dry-run/execute 均 403；新 API 同时使用 HTTP 403 和 envelope code 403。netns 两次真实 E2E：新命令/实际 fork.48 agent 回退，删 DB 中一个 forward 后恰好识别其 TCP/UDP，execute 后 0 孤儿，另一 forward 两监听保持，missing=0。
54 访问预检：经 leikwan-vps 到 120.253.251.139:1000 可达，但 restrict_cn/leikwan_vps 密钥被拒；nobrand memory 明确尚无我方密钥。继续查已有访问，不跳过 SSH 备份门槛。

发布门禁：分支最新提交 `4d718de9`，CI 38600711888 全部 success（含 PostgreSQL contract）；annotated tag `3.0.27-fork.49` 已推送，Images 38601072115 构建中。首次全量基线 15 无新增；补充 HTTP 403 合约检查与 vet 已通过，最终全量复核运行中。
