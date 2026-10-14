# 097 — 删除隧道时清理转发服务、修正 IPv6 RA 误报

基线：`origin/maintenance/3.0.27-fork.47-leikwan-phase2` (`7e2d786a`)；发布：`3.0.27-fork.48`。
仅修复已有删除逻辑和 RA 判断，无 schema/UI/安装脚本变更。OTA 仅 52、53。

- [x] 阅读流程与项目规则，确认生产 fork.47，建立隔离 worktree。
- [x] Part A：审计所有删除 forward 的入口，复用单条删除的运行时清理，添加命令接收端回归测试，保留 replace 行为。
- [x] Part B：记录曾观察到的 RA 依赖，修正用户态 RA 和无 IPv6 误报，补充单元测试。
- [x] 验证：backend / agent 测试、基线对照、race、vet；可行时本地 E2E。
- [x] 推送分支，CI 通过后打 fork.48 标签，核验 Images 和发布资产。
- [x] 生产备份、清理到两个备份、升级面板及健康检查。
- [x] 生产临时隧道及两个转发删除验证；检查现有服务读取命令并安全清扫孤儿。
- [x] 备份 52/53 agent，OTA、版本/单元/回连/RA 验证；清理临时对象。
- [x] 写中文总结、回滚说明与建议，提交计划最终状态并推送。

## 审计与验证记录

运行证据：`/root/flvx-workers/runs/tunnel-delete-orphans/`。

Part A：所有 tunnel cascade 在 `deleteTunnelByID` 删除数据库前共用 runtime cleanup，涵盖普通/批量、with-forwards、远程节点删除及创建失败回滚（通常无 forwards）。用户普通/批量、批量转发、远程节点直接转发、授权撤销及内部 batch 也复用；单条 force-delete 保留其明确的数据库强制删除语义。普通 forward create 失败为单条数据库回滚，不属于本次批量删除修复；入口权限变更逐旧端口清理后才删空规则。replace 的转发先迁移，旧隧道 cleanup 不再选中它们。
定向测试通过：多入口、两个规则（含暂停）、无关规则保护、离线不阻止 cascade、nftables/WG 两端、replace（同/不同入口）。服务候选保留历史 grant/legacy 名称兼容。

Part B：转发启用前及每次状态采样保存 RA 依赖证据；未见 RA 的接口不因 1→2 或超时出现 badge。accept_ra=0 但地址/路由完整时报告用户态 RA 正常。现有 1→2、失败报告、30 分钟恢复窗口保持。nftables 包测试通过，包括 node52/node53、node48 保留、真实丢失及恢复。

验证完成：backend `go test -p 1 -json ./...` 与 fork.47 同为 15 个失败（含子测试），新增 0；go-gost/x 全量通过。handler 定向 race、nftables race、handler/repo/nftables vet 通过。隔离 netns 真实 panel+agent E2E：两个 forwards 的 4 个 TCP/UDP services/listeners 删除后为 0，DB tunnel/forward/forward_port/chain_tunnel 均为 0。
孤儿清扫能力审计：现有 GetCoreConfig 读取固定 `/etc/flvxt2/<core>.json`，不读取运行 agent 的 `/etc/flvxx/gost.json`/服务注册表；没有 Get/ListServices 命令。10 分钟配置上报是 agent 主动 POST `/flow/config`，不保存可查询快照，且既有自动清理范围比本任务授权扫描更宽。不会新增命令或通过该回调触发全量清理；生产清扫按任务的无读取命令分支报告限制。

补充必要的 RA 上报修复：旧面板忽略空 status，52 的历史 error 不会自行清除。agent 显式发送空 `ipv6_ra_status`；面板用字段是否存在区分旧 agent 和明确清除，仓储允许空状态，首次回连也可清除。保持 write-on-change，不改 UI/schema。WebSocket/socket 回归及 race/vet 通过，覆盖旧 agent 不清除、显式空清除、重复空不上写。

发布：代码标签 `3.0.27-fork.48` → `f476e42a`；CI 38589110916、Images 38589504750 均 success。Latest、非预发布、10 个资产与 fork.47 一致，sha256/ZIP/compose 与安装脚本 pin 验证通过。最终 netns E2E 同时验证旧 RA error 通过真实 agent 空上报清除。
补充审计：联邦分享清理 `cleanupFederationTunnels`、联邦 runtime 创建失败回滚也走 `deleteTunnelByID`，自动覆盖。普通 node 删除只删 forward_port，不删 forward；WG path 删除不删 forward。本次未扩展这些非 forward 级联路径。

生产：2026-10-14 15:44:58Z 开始升级，后端 healthy、前端运行、版本均 fork.48，nginx/flow/test 正常；schema 不变，升级前 29 个在线节点均恢复回报。备份 `/opt/flvx-svc/rollback/pre-fork48-20261014T154409Z` quick_check=ok，保留本地 fork.47 镜像与 rollback.sh；prune 保留 pre-fork47/pre-fork48，删除 pre-fork46 和 fork.45 本地镜像。构建等待期间发生外部业务数据变化，升级前快照为 56 tunnels / 30 forwards / 30 ports / 186 chains，本次健康对照使用此最新快照。

生产 Part A：临时隧道 88 `临时测试-fork48-删除-10141646`，52→53 TLS:13007，转发 141/142、入口 21161/21162；52 4 个 TCP/UDP services、53 `88_tls` 及监听创建前后证据已存档。通过 `/tunnel/delete-with-forwards` 删除后，服务、监听、tunnel/forward/forward_port/chain_tunnel 记录均不存在，52 `chains_80` 与其他已有名称保持。扫描按无读取命令分支完成能力审计：29 在线节点无法全量读取，24 既有离线；删除 0；52/53 必需 SSH 快照只读对照均 0 孤儿。详见 run dir `prod-delete-proof.json` / `orphan-sweep.json`。

OTA：仅 52/53，SSH -V 均 fork.48，flvxx.service active 且 unit/连接设置保持。52 GitHub 下载约 8 分钟、客户端 180 秒超时，完整下载后校验请求停滞；独立确认 SHA256 `8f4a0defb46b987b24f49af98e4917e3e0991da7f9e43eba2d0921bc435e7bbc` 后，只用 `ss -K` 关闭 flvxx 的 `172.31.10.50:37076 → 20.205.243.166:443` GitHub 连接，原 OTA 完成，无重发；约 742.76 秒回报版本，下载期保持在线。53 正常 OTA，约 6.06 秒回报版本。下一次指标后 DB/node list：52 error→空，无徽标；53 error→ok（用户态 RA）。节点备份均为各自 `/root/flvxx-backup-20261014T152626Z-pre-fork48`。
最终 29/30 在线，其他 agent 版本不变；52 chains_80 内容不变。生产 23:57:28/33 另有本任务未发出的 tunnel delete-preview/delete-with-forwards 请求（run dir concurrent-delete.log），最终业务计数 55 tunnels / 29 forwards / 29 ports / 184 chains。逐项确认本次临时 88 / 141 / 142 已删除，没有临时名称；不以与较早备份全局计数相等作为并发业务的验收条件。

中文总结及完整回滚/破坏性操作/建议记录已写入 `/root/flvx-workers/runs/tunnel-delete-orphans/summary.md`，运行证据和逐节点扫描限制同目录保存。
