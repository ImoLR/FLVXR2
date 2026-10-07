# 075 — Agent 出站探测与逐跳自定义连接 IP

范围：按任务约定实现 additive 出站探测、逐跳 literal IP 覆盖，发布 `3.0.27-fork.27` 并升级生产面板；仅节点 47 做 agent OTA canary。不改生产节点/隧道配置，不主动重新部署生产隧道。

运行目录：`/root/flvx-workers/runs/egress-detect-connect-ip/`；TMPDIR/GOTMPDIR 使用其 `t/`，重型进程串行。

- [x] 阅读 plan 074、仓库规则与发布记忆，从 `7af40d9b` 建立指定分支。
- [x] Agent TCP v4/v6 探测、异步缓存与 SystemInfo 字段；注入 dialer 单测、相关包测试和构建。
- [x] Panel 探测列、change-only 更新、API/备份与 additive 有效出站；相关单测。
- [x] ConnectIP 运行时共享选择、诊断/探测/best-exit、校验与更新清空；修复两项既有失败。
- [x] 节点探测展示、隧道自定义 IP 输入与读写；类型检查及生产构建。
- [x] 在线生产副本：fork.26 与新逻辑全部相邻跳对比为 0；记录假设全部 detected dual 的变化。
- [x] 本地隔离 paneld:16365 + 遥测 harness + 配置/诊断验证；1440/390 节点与隧道表单截图。
- [x] 后端全量测试：基线仅减少指定两项及其子测试；CI Build Check 成功。
- [x] 注解标签、Build and Push Images 成功；Latest/资产集/compose/安装脚本/sha256 校验。
- [x] 生产回滚点与备份保留清理；升级 backend/frontend。
- [x] 生产只读健康/指标/列默认/API 权限/JS 验证。
- [x] 仅节点 47 OTA；确认在线、指标、forward 102 诊断与探测值，或记录失败及回滚。
- [x] 中文总结、清理本任务大型临时文件；提交完成计划并推送。

## 行为与应用路径

自动出站 = 入口地址族 ∪ 最新非空探测族；手动 v4/v6/dual 优先。探测不移除入口能力，不触发自动重新部署。现有隧道保存/重新部署、节点重连的节点范围配置下发会重读节点记录并使用新结果；诊断及质量/路径探测也读取节点记录。显式逐跳 v4/v6 与 lan 语义保持 fork.26。

自定义连接 IP 覆盖目标地址选择，目标端口保持原值；仅单节点中继跳或单出口可填写，必须为 literal IPv4/IPv6。共享选择函数供链配置、两类诊断、质量/路径探测及 best-exit 使用。

## 验证与发布记录

实施时逐项补充。

- Agent `go test -p 1 ./socket` 与 `go build -p 1 .` 通过；覆盖 v4/v6/dual/unknown、逐目标 fallback、3s deadline、上报省略与取消。

- 后端定向测试通过：additive/manual/no-detection 矩阵、API/备份往返、重复上报与重连仅 change-only 更新、literal IP/多节点校验、创建/更新/清空、自定义链配置/探测/best-exit。两个原基线测试及普通/流式诊断子测试均通过。

- 前端 `tsc --noEmit` 与 `npm run build` 通过，主 JS 2,753,064 B，PWA 5 MiB 余量 2,489,816 B。

- 在线生产副本：quick_check=ok；28 节点、53 隧道、169 chain_tunnel 行，139 个相邻跳组合。提取 fork.26/当前 Go 函数比较，无检测数据差异 **0**；全部 detected=dual 的假设变化也为 **0**。生产非空 connect_ip 为 0。详见 gate-input.json / gate-result.json / dial_gate.go。

- 全量 `go test -p 1 ./...` 的失败集合为 15/19：仅减少 `TestReconstructTunnelState_PreservesConnectIP`、`TestTunnelDiagnosisUsesConfiguredConnectIPContract` 与该诊断的两个子测试，无新增/额外缺失，基线文件未改。CI Build Check 37685225783 四项成功。追加真实质量/路径探测测试确认直连与含中继路径都使用自定义 IP；探测字段仅保留在前端只读节点类型。

- 本地隔离 namespace/paneld:16365：合成节点 901 经 AES WebSocket 每 250 ms 上报 dual；SQLite 触发器统计探测字段仅写 1 次，入口 IPv6-only 自动下发下一跳 IPv4。单中继自定义 IPv4、单出口自定义 IPv6 均进入真实 AddChains 与普通诊断。浏览器捕获保存 payload 后回放本地 API，数据库/list/诊断回读一致。
- 1440/390 节点、单中继、单出口表单共 6 张首轮截图 `screens/seed-*.png`；自动项显示检测族与时间，无错误/遗漏 API/横向溢出，已检测 IPv4 的上游不再显示缺失 IPv4 出站提示。Chrome 启动守卫记录可用 932.8 MiB、无其他 Chrome，paneld 已停止。
- 最新源码 CI Build Check [37685660990](https://github.com/ImoLR/FLVXR2/actions/runs/37685660990) 四项成功。

- 保存后的真实 API 响应再次在 1440/390 回读通过，新增 `screens/save-*.png` 6 张；Chrome 前可用 931.4 MiB，无其他 Chrome，无页面错误/横向溢出。
- 注解标签 `3.0.27-fork.27` 指向 `81f6a158`；镜像发布 run [37686280668](https://github.com/ImoLR/FLVXR2/actions/runs/37686280668)，标签提交 CI run 37686258952。

- 标签提交 CI [37686258952](https://github.com/ImoLR/FLVXR2/actions/runs/37686258952) 四项成功。生产回滚点 `/opt/flvx-svc/rollback/pre-fork27-20261007T210043Z`：在线 DB 366,944,256 B、quick_check=ok、62 表计数、compose/.env、fork.26 镜像本地标签。按规则执行 prune-backups.sh，仅保留 pre-fork27/pre-fork26，删除 pre-fork25 备份及不再保留的 fork.24 镜像。额外保存已校验 SHA256 的 fork.9 amd64 agent 回滚二进制。

- Release 2026-10-07T21:17:47Z 发布，Latest、非 prerelease，10 资产与 fork.26 同名；所有资产 SHA256/digest、两份 compose、两份安装脚本 PINNED_VERSION/REPO、两架构 gost 及 offline zip 内代理二进制均通过。backend/frontend 镜像均含 amd64/arm64。镜像流水线 37686280668 完整成功。

- 已安装校验过的 v6 compose（仅两个镜像标签变化），FLUX_VERSION 更新为 fork.27，执行 pull/up backend/frontend，backend healthy，frontend running。

- 生产 backend/frontend 于 2026-10-07 21:19:01/07 UTC 启动 fork.27；backend healthy，前端 HTTP/JS 正常。管理员列表 28 节点/53 隧道；非管理员 HTTP 200/code=403/data=null。新增列 28/28 为空串/0，22 节点指标晚于重启并推进，静态节点/隧道/链配置全部未变。线上主 JS 2,755,797 B，包含新文案与 fork.27 版本。

- 仅节点 47「AWS HK」OTA fork.9 → fork.27，4.8 秒观察到新版本在线，10.8 秒确认指标恢复；egress_detected=dual，egress_detected_at=1791407997649（2026-10-07 21:19:57.649 UTC），随后指标继续推进而检测时间不重复更新。与备份逐节点比对，只有 47 的 agent 版本变化，其余 27 节点检测值仍空。服务名 flvxx，未触碰 flux_agent。
- **Canary 未全通过项**：forward 102「hinet boil」入口节点 2 已离线，入口→47 报“节点不在线”；47→目标 TCP 成功，约 18.87 ms、丢包 0。节点 2 最后指标为 2026-10-07 20:13:36.142 UTC，在面板升级前的两份快照中已停止，非本次 canary 引起。未对节点 2/其他 agent 操作，未回滚健康的节点 47；完整规则诊断等待入口恢复后由用户复验。

- 最终中文总结写入 `/root/flvx-workers/runs/egress-detect-connect-ip/summary.md`；保留 12 张截图与全部校验记录，已删除本任务 DB 副本、测试二进制及大型临时/下载文件共 863,232,934 B。生产 fork.27 健康，节点 47 agent canary 健康；forward 102 全链诊断因升级前的入口节点 2 离线而未全通过，已明确报告并保留失败证据。其他 agent 未升级。
