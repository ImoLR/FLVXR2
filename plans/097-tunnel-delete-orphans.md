# 097 — 删除隧道时清理转发服务、修正 IPv6 RA 误报

基线：`origin/maintenance/3.0.27-fork.47-leikwan-phase2` (`7e2d786a`)；发布：`3.0.27-fork.48`。
仅修复已有删除逻辑和 RA 判断，无 schema/UI/安装脚本变更。OTA 仅 52、53。

- [x] 阅读流程与项目规则，确认生产 fork.47，建立隔离 worktree。
- [x] Part A：审计所有删除 forward 的入口，复用单条删除的运行时清理，添加命令接收端回归测试，保留 replace 行为。
- [x] Part B：记录曾观察到的 RA 依赖，修正用户态 RA 和无 IPv6 误报，补充单元测试。
- [x] 验证：backend / agent 测试、基线对照、race、vet；可行时本地 E2E。
- [ ] 推送分支，CI 通过后打 fork.48 标签，核验 Images 和发布资产。
- [ ] 生产备份、清理到两个备份、升级面板及健康检查。
- [ ] 生产临时隧道及两个转发删除验证；检查现有服务读取命令并安全清扫孤儿。
- [ ] 备份 52/53 agent，OTA、版本/单元/回连/RA 验证；清理临时对象。
- [ ] 写中文总结、回滚说明与建议，提交计划最终状态并推送。

## 审计与验证记录

运行证据：`/root/flvx-workers/runs/tunnel-delete-orphans/`。

Part A：所有 tunnel cascade 在 `deleteTunnelByID` 删除数据库前共用 runtime cleanup，涵盖普通/批量、with-forwards、远程节点删除及创建失败回滚（通常无 forwards）。用户普通/批量、批量转发、远程节点直接转发、授权撤销及内部 batch 也复用；单条 force-delete 保留其明确的数据库强制删除语义。普通 forward create 失败为单条数据库回滚，不属于本次批量删除修复；入口权限变更逐旧端口清理后才删空规则。replace 的转发先迁移，旧隧道 cleanup 不再选中它们。
定向测试通过：多入口、两个规则（含暂停）、无关规则保护、离线不阻止 cascade、nftables/WG 两端、replace（同/不同入口）。服务候选保留历史 grant/legacy 名称兼容。

Part B：转发启用前及每次状态采样保存 RA 依赖证据；未见 RA 的接口不因 1→2 或超时出现 badge。accept_ra=0 但地址/路由完整时报告用户态 RA 正常。现有 1→2、失败报告、30 分钟恢复窗口保持。nftables 包测试通过，包括 node52/node53、node48 保留、真实丢失及恢复。

验证完成：backend `go test -p 1 -json ./...` 与 fork.47 同为 15 个失败（含子测试），新增 0；go-gost/x 全量通过。handler 定向 race、nftables race、handler/repo/nftables vet 通过。隔离 netns 真实 panel+agent E2E：两个 forwards 的 4 个 TCP/UDP services/listeners 删除后为 0，DB tunnel/forward/forward_port/chain_tunnel 均为 0。
孤儿清扫能力审计：现有 GetCoreConfig 读取固定 `/etc/flvxt2/<core>.json`，不读取运行 agent 的 `/etc/flvxx/gost.json`/服务注册表；没有 Get/ListServices 命令。10 分钟配置上报是 agent 主动 POST `/flow/config`，不保存可查询快照，且既有自动清理范围比本任务授权扫描更宽。不会新增命令或通过该回调触发全量清理；生产清扫按任务的无读取命令分支报告限制。

补充必要的 RA 上报修复：旧面板忽略空 status，52 的历史 error 不会自行清除。agent 显式发送空 `ipv6_ra_status`；面板用字段是否存在区分旧 agent 和明确清除，仓储允许空状态，首次回连也可清除。保持 write-on-change，不改 UI/schema。WebSocket/socket 回归及 race/vet 通过，覆盖旧 agent 不清除、显式空清除、重复空不上写。
