# 117 — 发布 fork.62：节点附加 IP 与隧道入口 IP 勾选

授权：用户于 2026-10-20 看过截图后同意发布。只升级面板，不 OTA，不填写真实节点附加 IP，不保存生产隧道。时间使用台湾时间（UTC+8）。

## 清单

- [x] 阅读发布流程、计划 112 和 fork.61 交付记录；确认生产为健康的 fork.61。
- [x] 从 `3.0.27-fork.61` 建立 `maintenance/3.0.27-fork.62-multi-entry-ip`，创建本计划。
- [x] 搬入原分支 10 个提交；保留线路机、已有落地节点规则和多 IP 复制链接行为。
- [x] 干净 fork.61 基线与候选版后端全测按测试名比较，零新增失败；改动包 vet 通过。
- [x] 前端 tsc、改动文件 eslint 零新增错误、build 通过。
- [x] 本地 landing-acceptance 通过；重拍 4 张关键界面并检查复制链接。
- [x] 采集生产升级前状态、23+51 多入口隧道诊断、入口/监听配置与下发大小、面板 RSS。
- [x] 推送分支，打 fork.62 tag；CI、Images 通过，Latest 与 fork.61 资产集合一致。
- [x] 创建并校验 pre-fork62 回滚备份，执行保留两份的清理脚本，升级生产面板。
- [x] 核对生产结构、节点上报、既有入口/监听配置字节一致、诊断一致与资源实测。
- [x] 生产 landing-acceptance（21+共享 7、21+共享 5）通过，临时数据清理完毕。
- [x] 提交并推送完成记录，删除本任务临时数据库/实验资源，写中文 summary.md。

## 范围与证据

- 保留 `plans/112-multi-entry-ip.md`；不实现其中建议，不改 agent/mmwx/安装脚本。
- 原分支：`feature/3.0.27-multi-entry-ip`，`e69941a8`。
- 基础 tag：`3.0.27-fork.61`，`9591487e`。
- 证据目录：`/root/flvx-workers/runs/release-fork62-multi-entry-ip/`。
- 回滚目录、测试结果和实测数据随步骤补充。

## 合并核对

- 原分支 10 个提交均已搬入；前两个是 fork.61 已有改动，以空提交保留来源。
- 节点表单冲突同时保留 `lineMachine` 与 `extraIPsToText`。
- 线路机复制接口原来遇到逗号分隔的入口地址会回退主 IP；现复用普通落地链接的地址展开与入口归属过滤。新增测试覆盖附加 IPv6 单选、双 IPv6、IPv4+附加 IPv6及部分入口权限，测试通过。

## 升级前证据

- 29/29 节点在线，附加 IP 均为空；面板 RSS 40,272 KiB（39.33 MiB）。
- 23+51 的 12 条入口隧道（25、26、27、28、29、30、31、32、33、39、42、43）55/55 诊断成功。
- `go vet -p 1 ./internal/http/handler ./internal/store/repo` 与后端 build 通过；后端全测按名比较及本地下发命令大小比较进行中。

## 配置与前端验证

- 前端 tsc、build 通过；改动页面 eslint 29→29 个既有错误，按文件/规则/消息比较零新增，warnings 1922→1957。
- 干净 fork.61 与当前版在相同生产备份副本上比较：43 条隧道、31 条规则显示、410 条重下发命令、119 条监听配置均字节一致。
- 12 条多入口隧道的 141 条命令 JSON payload 共 24,644→24,644 字节，差值 0。两个临时生产数据库副本在比对后删除。
- 首轮后端全测失败集合与基线一致（15，含子测试）；复制链接融合修复后正在跑最终全套确认。

- 本地 landing-acceptance 全部 13 场景、96/96 检查通过；4 张截图与 15 项浏览器断言通过。另补跑线路机 C1–C8，确认复制链接融合不影响数据面。

- 最终后端全测包含复制链接新测试：1132 pass / 15 fail / 83 skip；基线 1102 pass / 15 fail / 83 skip。15 个失败（含子测试）逐个同名，新增失败为 0。

## 发布

- 台湾时间 2026-10-20 21:43 推送 annotated tag `3.0.27-fork.62` → `29f25ee3983c59cc1d5d47742e2aa9cbe971e94f`。
- CI `39432703822` 成功；Images `39432851967` 成功（前端镜像 13:43–14:01Z），`Create Release (Tag Only)` 自动建好 Release。
- r2 校验（`/root/flvx-workers/runs/release-fork62-multi-entry-ip-r2/release-validation.json`，16/16 PASS）：Release `3.0.27-fork.62` 非草稿、非预发布、为 Latest；10 个资产与 fork.61 同名（compose v4/v6、gost-amd64/arm64 及 sha256、install.sh、panel_install.sh、offline-amd64/arm64.zip）；compose 镜像为 `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.62`；两个安装脚本 `PINNED_VERSION=3.0.27-fork.62`、`REPO=ImoLR/FLVXR2`；gost sha256 一致；GHCR 两镜像均含 amd64/arm64。
- 台湾时间约 21:53 推送分支到 `3142e834`（tag 不动）。
- 线路机 C1–C8 全通过；50 条链、1000 次下载零失败，临时 namespace/进程均已退出。

## 生产升级（r2，台湾时间 2026-10-20）

- 21:53–22:02 升级前重新只读采集：29/29 在线；12 条多入口隧道 55/55 诊断成功，与 r1 完全相同；全部配置导出（tunnel、chain_tunnel、forward 配置、forward_port、节点地址、schema、API 地址投影）与 r1 21:32 的导出逐字节相同。30 秒窗口 RSS 平均 37.5 MiB（38,443 KiB），CPU 4.0%（单核），线程 14，FD 40。
- 22:02 回滚点 `/opt/flvx-svc/rollback/pre-fork62-20261020T140251Z`（385 MB）：SQLite 在线备份 quick_check=ok、integrity_check=ok，计数 node 29 / user 12 / tunnel 43 / forward 31 / user_tunnel 143 / landing_server 3 / landing_node 1；compose、.env、旧镜像 tag `local/flvxx-{backend,frontend}:pre-fork62-20261020T140251Z`、`rollback.sh`（默认不回拨数据库，`--restore-db` 才回拨）。
- 清理脚本保留 pre-fork62 + pre-fork61；删除 `pre-fork60-20261020T095406Z` 目录及其 local tag、ghcr fork.59 镜像。
- 22:03 升级到 fork.62（backend 14:03:04Z 启动），10 秒内 healthy；前端 `/version.json` = `3.0.27-fork.62-mvgql4hg`，公网 flvxx.imgamer.top 同样返回 fork.62。
- 升级后校验 `verify-after.json` 8/8 通过：容器 fork.62 健康；29 个节点、版本、状态不变，全部在重启后重新上报；29 轮节点重下发全部 failed=0；`extra_ips` 仍全空；schema 无变化（本版不新增表列，复用既有 `node.extra_ips`）；全部配置导出 diff 为空；表计数不变；55/55 诊断结果逐项相同；日志无 error/panic。
- 升级后 30 秒窗口（刚重启）RSS 平均 41.0 MiB（41,938 KiB），CPU 4.3%，线程 14，FD 40。

## 生产验收与收尾（r2）

- 生产 landing-acceptance（prod 模式，节点 21 原生落地）：21+共享 7 于 22:19 完成 11 场景 76/76；21+共享 5 于 22:31 完成 11 场景 76/76。两轮 PC 场景均确认临时用户及其落地节点/授权/配额/规则全部删除，既有服务器、其他用户节点凭据和用户未变。
- 独立复核（22:31）：user 12、node 29、landing_node 1（仅 `keekao ss2022`）、forward 31、user_tunnel 143，与升级前相同；无残留验收进程；全部配置导出与升级前再次逐字节相同；29/29 在线。
- 验收后稳态 30 秒窗口：RSS 平均 43.0 MiB（44,018 KiB），CPU 4.6%，线程 14，FD 40；与 fork.61 发布时验收后 35.1→43.1 MiB 的走势一致，线程/FD 未增加。重启至今后端日志 0 条 error/panic。
- 多入口隧道下发大小：12 条多入口隧道 141 条命令 24,644→24,644 字节（r1 在生产副本上对比），本版不增加下发量；生产升级后重下发 29 轮全部 failed=0。
- 清理：验收 scratch `/root/gotmp/release-fork62-multi-entry-ip-r2` 已删除；r1 的两份约 403 MB 生产库副本 r1 已删除，两个运行目录无 `.sqlite`/`.db`；下载的 gost/offline 二进制校验后删除（哈希保留在 `releases/fork62-binaries.sha256`）。
- 回滚：`bash /opt/flvx-svc/rollback/pre-fork62-20261020T140251Z/rollback.sh`（回到 fork.61 镜像与 compose/.env，保留当前数据库；本版无结构变化，fork.61 可直接读）。
- 未做：agent OTA、填写任何真实节点附加 IP、打开/保存生产隧道或节点、移动 tag。
