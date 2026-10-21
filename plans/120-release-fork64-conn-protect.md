# 120 — 发布 fork.64：整机连接数 + 整机连接保护（plan 118）

用户 2026-10-21 看过两组截图后批准发布（「可以 你就發布吧」）。时间统一台湾时间（UTC+8）。
从 fork.63 分支头 `ec7837f5`（= tag `3.0.27-fork.63` 4fa412e9 + plan 119 完成文档）建
`maintenance/3.0.27-fork.64-conn-protect`，按序 cherry-pick `feature/3.0.27-conn-protect` 的 14 个提交
（b2cfa5bf..6a376462），保留 fork.63 监控入口页与共享落地时效提示的行为。不加新功能。
运行目录：`/root/flvx-workers/runs/release-fork64-conn-protect/`。

## 检查清单

- [x] 阅读发布流程记忆、plan 118 总结与契约，确认工作树、分支与授权。
- [x] 合入 14 个提交并解决冲突（两边行为都保留）；推送发布分支。
- [x] 干净 fork.63 临时工作树取后端完整测试基线；发布代码后端全量测试按名称对比无新增失败；go vet 同基线。
- [x] go-gost/x 全部测试通过，新包 `machineprotect` 加 -race 通过。
- [x] 前端 tsc、eslint 无新增错误、`npm run build` 每个文件 < 5 MiB。
- [x] `scripts/landing-acceptance/` 本机验收通过。
- [x] 重拍 3 张关键页面（节点编辑「连接保护」、监控列表芯片含 fork.63 入口页、落地共享卡片）。
- [x] Tag `3.0.27-fork.64`；CI / Images 通过；Latest 资产集合与 fork.63 相同。
- [x] 生产升级前：回滚目录 `pre-fork64-<TS>`（验证过的 DB 备份、compose/.env、镜像 tag、rollback.sh）、
      prune 保留 2 份；记录在线节点数、表计数/哈希、面板 RSS。
- [x] 升级生产面板；核对 node 新列默认值（开 / 1000 / 500 / 10）、在线节点数不变、日志无错误激增、
      规则/隧道/落地/计费不变。
- [x] OTA 前逐台记录 1 天 `tcp_conns` 峰值与（有 SSH 时）整机实时总数；≥ 700 的节点先在面板把上限调到 1500。
- [x] 金丝雀：一台纯 FLVX 节点，SSH 备份 + 恢复脚本，OTA，检查归属 / nft 表 / sysctl / 芯片 / 诊断 /
      CPU·RSS / 单循环 / business-ports.json，观察 15 分钟。
- [x] 与妙妙屋共机的节点（21/31/35/45/36 及按 IP 找到的其它）：OTA 后显示「由妙妙屋负责」或「妙妙屋助手版本过旧」，
      无 FLVX nft 表，sysctl 未动。
- [x] 其余节点逐台 OTA，前后诊断；14 / 20 各试一次；离线节点跳过并记录。
- [x] 清理临时环境；120 完成；提交推送；中文总结写入运行目录 `summary.md`。

## 证据

- 2026-10-21 14:3x：14 个提交按序 `cherry-pick -x`，无 merge commit。唯一冲突
  `vite-frontend/src/pages/landing/monitor-view.tsx`（共享服务器卡片头）：保留 conn-protect 的
  `MachineConnChip` 与 fork.63 的 `AgeHint ageHintMs`。核对：`git diff fork.62..fork.63分支头` 与
  `git diff feature头..发布头` 只差冲突处上下文行和行号偏移，即合并结果 = 两边改动之和。go-backend / go-gost /
  go-gost/x `go build ./...` 通过。
- 后端（14:37–14:50）：fork.63 完整导出（`git archive 3.0.27-fork.63`，临时目录，不注册 worktree）与发布分支同参数
  `GOMAXPROCS=2 go test -json -count=1 -p 1 ./...`：失败名称 15 = 15，新增 0、消失 0（即 fork.62 起的已知 15 个）；
  `go vet ./...` 输出逐字相同（只有原有 leikwan/testapi 警告）。plan 118 新测试（迁移默认值、只推给有能力的 agent、
  管理员可见性、200 节点内存、共享 machine 透传等）全部通过。注：第一次基线只导出了 go-backend，读仓库根目录文件的
  4 个测试因此假失败，已改为完整导出重跑。
- agent：发布分支 go-gost 与 feature 头 6a376462 逐字节相同（`git diff` 0 行）。go-gost/x `go test -p 1 ./...` 通过；
  `-race` machineprotect 与 socket 保护相关测试通过；netns 真实 nft 端到端 5 项通过（UntrackedFlows 按设计需要干净
  命名空间，单独运行通过：已有 TCP/UDP 不受影响、新连接被拒）。
- 前端：`npm run build`（含 tsc）通过，主包 2,886,593 B（< 5 MiB）；改动的 8 个文件 eslint 与 fork.63 逐文件相同
  （errors 14 / warnings 325 → 14 / 325，新文件 machine-conn.tsx 0/0）。
- 本机落地验收（paneld + agent 3.0.27-fork.64 + 固定 Core v1.4.59 sha ae4b83f0）：S1–S8、E1–E3、T1、R1 共 13 项全部
  PASS（122 条检查）。第一次运行因下载的 Core 文件没有执行权限（0644）在 setup 失败，属本机环境问题，加执行权限后重跑
  通过。实验室 agent 会往宿主机写 `/run/flvx/business-ports.json`（/run 不随 netns 隔离），已删除。
- 截图（运行目录 `screens/`）：本地演示面板 + 模拟 agent（真实 WS 协议）+ 模拟 Custom，只在 127.0.0.1：
  `1-node-edit-conn-protect`（连接保护默认 开/1000/500/10）、`2-monitor-node-list-chips`（旧记住页签=落地仍打开
  「节点」= fork.63 行为；芯片红/黄/灰/-）、`3-landing-shared-card`（「整机连接 846/1000」，马年 `-`）；0 页面错误。
- CI Build Check（分支 b09b7442）：run 39556567545 success。
- Release（15:00–15:14）：tag `3.0.27-fork.64` → 92caf0a8；CI Build Check 39558986869 success，Build and Push Images
  39558998157 success（含 Create Release）。`validate-release.py`：Latest = fork.64、非草稿/预发布、10 个资产与 fork.63
  完全相同、两份 compose 除版本号外与 fork.63 逐行相同、install.sh / panel_install.sh PINNED_VERSION=3.0.27-fork.64 +
  REPO=ImoLR/FLVXR2、gost-amd64/arm64 sha256 一致、ghcr backend/frontend 镜像 amd64+arm64。
- 生产面板（15:15:54 重启）：回滚目录 `/opt/flvx-svc/rollback/pre-fork64-20261021T071457Z`（gost.db.validated
  quick_check/integrity_check = ok、compose/.env、`local/flvxx-*:pre-fork64-20261021T071457Z`、rollback.sh
  [--restore-db]）；prune 后保留 pre-fork64 + pre-fork63。升级后：健康、/flow/test 200、version.json
  3.0.27-fork.64-mvhrmbpu；node 新 4 列 29 行全部 = 开/1000/500/10；29/29 在线（5 秒内全部重连）；表计数与备份相同；
  80 张表配置哈希对比 `existing_config_unchanged = true`（只有 node 表新增 4 列）；面板 RSS 36.2 → 39.4（1 分钟）→
  43.0 MiB（15 分钟）。
- 面板重启的副作用：node 48「Leikwanhost 沪台IPLC」重连时 WSS 握手超时，旧 agent 回退到 80 端口 ws:// 并在进程内一直
  沿用；这条线路上 80 端口 WS 只收得到面板的第一条消息（data_segs_in=2），之后命令/心跳都到不了，每 45 秒被面板心跳
  超时断开。实测此刻 48 上 Go 与 curl 到 Cloudflare 两个 IP 的 TLS 握手大多超时（8 次 1 次成功）= 线路问题，与
  fork.64 代码无关；转发服务一直在跑（gost.json），只是暂时无法从面板管理/诊断。SSH 重启 agent 6 次都仍未走上 WSS，
  留待线路恢复后再处理。
- 上限设置（OTA 前）：24 小时峰值（TCP+UDP）最高 45=836（妙妙屋负责，只报告）、42=678、18=670、15=665，其余 < 560；
  52 按 10-20 实测约 905 设为 1500（面板节点编辑接口，完整字段同前端，前后对比只变 machine_*）；36 是妙妙屋生产主机
  （178.214.214.173）但没有运行妙妙屋助手，为保持该主机不变（不建表、不改内核）把 FLVX 保护设为关。两次保存都把
  panel_ip_family 从 '' 规范成 'auto'（前端保存节点本来就会这样；agent 都上报 auto，不触发下发，行为相同），之后的
  保存已不带该字段。
- 金丝雀 42「Bestvm HK 年付」（15:39）：SSH 备份 `/root/flvxx-backup-20261021T071350Z-pre-fork64`（restore-agent.sh），
  OTA 请求 1.8 s、重连 4.0 s；owner=flvx、上限 1000、整机 276（节点实测 280）；nft 表 `inet flvx_machine_protection`
  在、0 个挂钩链（只有 conntrack 链 + 端口集合 + dropped 计数器）；tcp_max_tw_buckets 5000 → 500（原值存
  `/etc/flvxx/machine-protection-state.json`）；`/run/flvx/business-ports.json` 0644、14 个 TCP 端口；诊断 17 项
  87/88 前后相同（forward 6 → 82.152.163.228:34749 升级前就失败）；agent CPU 0.30% → 0.32%、RSS 61 → 57 MB、线程 8、
  到面板 1 条连接（升级后改走 WSS）。`ss -K` 只断 agent 的面板连接 3 次（PID 不变）：每次 65 秒都是 14 个样本、间隔
  5.0 s = 只有一个保护循环；CPU 0.43%（含重连重新下发）。
- 共机节点（15:57–16:03）：按 IP 对妙妙屋服务器清单，共机的是 21(s11)、30(s3)、31(s1)、34(s10)、35(s7)、45(s14)、
  53(s15)、54(s17 同一台 VM)；36 是妙妙屋生产主机本身但没有 mmwxc-helper。妙妙屋并行任务已把这些机器的助手升级，
  状态文件新鲜 → 全部显示「由妙妙屋负责（ok）」；有 SSH 的 7 台确认：没有 `inet flvx_machine_protection` 表、
  tcp_max_tw_buckets 与 OTA 前相同（助手已设 500）、无 FLVX 状态文件、`/run/flvx/business-ports.json` 已写、其它面板
  agent / 落地 Core（21 号 PID 29900）/ 助手 PID 不变；45 的上限 1500 来自妙妙屋（其服务器 14 的设置）。36：FLVX 保护
  关 → 无表、TIME_WAIT 保持 8192、无状态文件，端口文件为空列表。诊断全部前后相同。
- 其余节点（16:04–16:29）：17、25、39、12、22、2、23、32、49、50、51、15、18、26、47、52 逐台通过（诊断前后完全相同，
  owner=flvx、开启、上限 1000，52 为 1500）；47 TIME_WAIT 4096→500、52 5000→500（52 没有 FLVX 业务端口 → 不建表）。
  14「Lightlayer TW」（上游 3.0.27）这次一次成功（请求 5.2 s、重连 6 s，诊断 34 项前后相同）。20「Vmsilo TW」与
  fork.56 时相同：节点回复 OK 但重启没有发生，仍 fork.55，只试一次。
- 48「Leikwanhost 沪台IPLC」：命令通道 08:04 自行恢复后 OTA 成功（fork.64、收到设置、TIME_WAIT 4096→500、表无挂钩），
  但新进程重启后 WSS 握手又失败 → 80 端口 → 45 秒断开循环，面板诊断因此全部失败（STOP diagnosis_regression）。
  数据面确认正常：3 个转发端口 TCP/UDP 在监听，开发机可连上 48 的 IPv6 入口，48 到 113/126 目标 TCP 正常（110 的目标
  pqstw:10015 从 48 本机就连不通，升级前诊断也失败）。与 fork.56 在 07:16–08:04 的表现相同，未回滚。
- 最终（16:3x）：28/29 在 fork.64（20 仍 fork.55）；29/29 在线；0 个节点拦截/丢弃/报错；面板 RSS 36.8 MiB；
  SSH 节点 agent CPU 0.10–0.45%、RSS 56–61 MB、线程 8–9。节点 SSH 备份 `/root/flvxx-backup-20261021T071350Z-pre-fork64`
  （42 30 31 34 35 53 45 21 36 47 52 48），每台只保留它和上一份。逐台表：运行目录 `ota/nodes-final.md`。
- 清理：本机临时编译产物 / 演示面板 / 验收实验室目录 / fork.63 导出 / 下载的 gost 二进制已删除；48 上的 TLS 探测程序
  用完即删。

