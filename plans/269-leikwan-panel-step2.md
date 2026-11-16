# 269 — 利群专线入面板 第二步：agent 接管利群主机文件 + 主机状态 + 利群流量 + 99U 落地（构建 + 发布）

基线：分支 `maintenance/3.0.27-fork.139-leikwan-grant`（tag `3.0.27-fork.139` + 268 文档），新分支 `feature/3.0.27-leikwan-step2`，
worktree `/root/projects/flvxx/flvxx-leikwan-step2`。用户 11-17 ~02:10 台湾：「第二步你这边也一起完成了，到时也发布没有关系」。

## 主机现状（11-17 04:40 台湾只读 SSH）

| 主机 | 前端 | 预热文件 | 首包过滤 | 日志快切 | 首包合并 | 手机备线 |
|---|---|---|---|---|---|---|
| 百度云广州 | 测试版（headroom 构建） | /opt/leikwan-front-test/warm：263/540=10，264/280/282=4，backup:*=4，standby 2，pace 100，burst 行（A/B 中） | /opt/leikwan-front-test/ssfilter 7 条 enforce + /etc/leikwan-front-test/sskeys.json | v11，LINES 7 条（含 540），triggers 文件 + return_check 5 条 | off | 19263–19282（6 个） |
| 火山广州 | 测试版 | 同上但无 540、无 burst | 6 条（无 540） | v11，LINES 6 条 | on budget_ms=2 min=59 | 19047 |
| 阿里广州 | 测试版 | 同火山广州 | 6 条 | v11，LINES 6 条 | on … | 无 |
| 火山上海 | 稳定版（无预热/过滤文件） | 无 | 无 | v9.1，LINES 6 条，RECORD_ONLY 全部只记录，无 triggers 文件 | on … | 无 |

- LOGSWITCH_LINES / RECORD_ONLY 在 unit 的 Environment 里（改了要 daemon-reload + 重启 logswitch）；warm / ssfilter / sskeys / coalesce / triggers 前端或 logswitch 每秒热读。
- 主机已有 `inet leikwan_meter`（厂商 agent 的 input/output 计数）：经前端 REDIRECT 和日志快切 DNAT 的流量不在里面 → 不能拿来计费，用自己的表按 conntrack 原始目的端口计数。

## 设计

- **agent（go-gost/x/socket/leikwan_host*.go，只在 linux）**：一个 goroutine，≥60 s 或收到面板同步时检查一次。
  - 按主机发现路径（读前端 / logswitch 的 unit + drop-in 的 Environment，没有就用默认路径），把每个文件解析成「语义」（token map / 行集合 / 指纹），
    sskeys 只上报每条转发的指纹（sha256 前 16 位），密钥不出主机。
  - 上报：主机状态（厂商 agent / 核心版本、更新锁、原生 UDP、选路、前端通道/构建、快切通道、logswitch 版本、首包合并、手机备线、利群转发列表 + 状态）
    + 各文件语义 + 接管状态（已应用 rev、漂移、待重启、错误、备份）+ 流量计数（epoch + 每端口上/下行字节）。有变化才发，≥5 min 全量一次。
  - 接管：面板下发每台主机的期望语义（nil = 不管该文件）+ rev + force。接管关 = 只读。接管开：语义相同不写；不同且 rev/force 比已应用新 → 备份旧文件
    （/var/lib/flvx-leikwan/backup/<时间>/，留最近 20 份）+ 原子写（同目录临时文件 + rename，保留权限，sskeys 0600）+ 热读文件无需重载；
    logswitch（drop-in `leikwan-logswitch.service.d/60-flvx-panel.conf`）和手机备线（`nft -f`）要重启/改 nft → agent 自己只在台湾 02:00–06:00
    （不含 03:45–04:20）执行，窗口外报「待执行」。rev 没变而主机文件和期望不同 = 漂移，只上报，不覆盖（「按面板覆盖」= force+1）。状态存 /etc/flvx_agent/leikwan-host-state.json。
  - 流量：自己的表 `inet flvx_leikwan`：prerouting（非 lo，ct 原方向，ct original proto-dst = 利群端口）计上行，postrouting（非 lo，ct 回复方向）计下行；
    屏蔽 = prerouting 同条件 drop（在前端 REDIRECT / 日志快切 DNAT 之前）。计数表的创建/改端口只在窗口内；屏蔽立即生效（执行限额）。
- **面板**：新表 `leikwan_host`（每台入口机：接管开关、流量统计开关、主机覆盖 profile、rev/force、上报、计数基线）。
  - 期望 = 专线默认 + 规则覆盖 + 全局 + 主机覆盖（学习主机现值时只记录和通用值不同的项），保证接管时语义差异 = 0；
    主机状态上报写进 leikwan_host_state（source=agent，兼容第一步界面）。
  - 流量：每端口增量 → 利群规则（in/out 计数）→ 用户总流量（专线计费方向/倍率，默认和隧道一样单向 ×1）+ 入口机的单服务器配额 + 流量池；
    落地是同一用户的面板落地节点时按 planLandingForwardBilling 一样去重。随用户重置清零。
  - 屏蔽：用户总流量/到期/禁用、入口机单服务器配额超了 → 该规则利群端口在所有入口机屏蔽 + 自动备线暂停；恢复后解除（状态驱动，每 60 s + 流量入账后）。
  - 99U：在共享落地「Boil HKT 99u」给 263 / 540 各建一个 SS2022 面板落地节点（admin 名下），规则上记「待切换」，用户按「换落地」时：
    利群目标 + 自动/链接备线目标 + 首包过滤密钥一起换；不动 99U :10016。
- **界面**：利群入口区：实时主机状态、接管开关（先看差异）、漂移 ⚠（admin-attention）、按面板覆盖/采用主机现值、规则行流量、待切换标记；只加不改。

## 检查清单

- [x] 读 memory / 266 / 267 / 268 / 094 / 096 / 主机布局（只读），建 worktree + 计划文档。
- [x] 主机文件夹具（4 台，去密钥）→ go-gost/x/socket/testdata/leikwan-host/。
- [x] agent：发现 + 解析 + 语义 + 上报（主机状态）+ 单测（4 台夹具）。
- [x] agent：接管写入（备份、原子写、漂移、窗口、force）+ 渲染单测（渲染后再解析语义相等）。
- [x] agent：流量计数表 + 屏蔽（nft 脚本生成 + 读取）+ 单测。
- [x] 面板：模型 + 迁移、上报入库、期望生成 + 学习主机现值（4 台夹具语义差异 0 的测试）、同步下发。
- [x] 面板：流量入账（去重）、重置、屏蔽/解除 + 契约测试（SQLite + PostgreSQL）。
- [x] 面板：99U 落地节点准备 + 换落地带备线（自动 + 链接）+ 密钥 + 待切换标记 + 测试。
- [x] 前端：利群入口区（主机状态、接管开关 + 差异、漂移 ⚠、按面板覆盖、采用主机现值）、规则行流量 + 待切换；`npm run build`。
- [x] `go test ./...`（两个模块）+ go vet + CI 绿；截图画廊 /root/mmwx-preview/leikwan-step2/。
- [x] 发布：tag 下一个 fork、CI + 镜像 + 资产；面板备份 + 换面板；只 OTA 4 台利群入口机（每台备份 + 回滚）；保留 2 份备份。
- [x] 上线：百度云广州（A/B 结束后）接管开 → 语义差异 0、leikwan status、日志、手机端口、SS-2022 实测 → 阿里广州 → 火山广州 → 火山上海；流量统计窗口内开。
  （用户 11-17 05:40 决定后顺序改为 阿里广州 → 火山广州 → 火山上海 → 百度云广州（等 A/B 结束），不受夜间窗口限制。）
- [x] 99U 落地节点建好 + 新链接；总结 /root/flvx-workers/runs/leikwan-panel-step2/summary.md + final.md；memory 文本写进 final.md。

## 记录
- 夹具（11-17 05:5x 台湾只读采集，假密钥同形状）：4 台主机的 unit / warm / ssfilter / sskeys（假）/ coalesce / triggers / 手机备线 / 转发配置与状态
  → go-gost/x/socket/testdata/leikwan-host/；agent 读出来的上报 → go-backend/tests/contract/testdata/leikwan-host/*.json。
  百度云广州夹具里 burst=off（A/B 当时的值），A/B 结束后按最终值再采一次。
- agent 单测：4 台读取（数量/版本/前端通道/日志快切版本/手机端口逐台核对，上报里没有密钥）、渲染→再读取语义相等（7 种文件 × 4 台）、
  语义相同不写（字节和 mtime 都不变、不建备份）、改值写入 + 备份 + 窗口外 logswitch 待执行 + 手改漂移不覆盖 + 按面板覆盖 + 窗口内 drop-in + restart、
  重启后保留已应用 rev、接管关只读、窗口边界、nft 脚本 + 计数读取 + 窗口外只执行屏蔽 + 幂等 + 解除屏蔽保留计数、面板 sync JSON 跨模块解析 + 密钥指纹一致；-race 通过。
- 面板契约测试 TestLeiKwanHostTakeoverContract（SQLite + PostgreSQL 18）：第一步导入后 4 台上报 → 采用主机现值 → 4 台语义差异全部 0；
  覆盖项 = 百度云广州 540 预热 10、火山广州 540 不接日志快切/不过滤、火山上海只有日志快切文件（只记录 9 项）、手机端口指向备线 148 等；
  广港预热 10→8 只改 263（百度云广州 540 仍 10，火山上海不变）；漂移 ⚠；按面板覆盖；疑似密钥的上报丢弃；
  流量：增量、同值不重复计、换 epoch、双向 ×2、另一入口机单服务器配额；落地是自己面板落地节点时不重复计入用户总流量；
  总流量用完 / 到期 / 单服务器配额用完 → 屏蔽，恢复后解除；待切换（只能选规则用户自己的落地）；未接管的入口机在过滤 → 拒绝换落地。
- nft 计数实验（本机两个网络命名空间，不碰主机网络）：前端 REDIRECT 10018 上 100000→计 101048 / 下 200000→200944；
  直连 TCP+UDP 10015 上 40000→41016 / 下 80000→80912；本机 127.0.0.1 / 本机地址到 10015 的 50 KB 没被计；
  日志快切 DNAT 10021（转发到备线）上 50000→50840 / 下 70000→70736；屏蔽 10015 TCP+UDP 都超时、10018 照常、计数不清零；解除后恢复。
- **用户 11-17 ~05:40 台湾**：两点到六点的维护窗口指业务节点；利群入口 4 台还没业务，不受限制，做完就发布，只要不影响业务节点。
  → agent 的夜间窗口改成面板设置 `nightOnly`（利群全局设置，默认关 = 需要时就做；以后利群上有业务可以打开）；
  发布不等夜里；只 OTA 4 台利群入口机，别的节点不动；百度云广州仍等 warm-headroom-r2 A/B 结束再写。
- 11-17 05:30 台湾协调员删了旧管道 #318 和隧道 80（用户同意），利群名额 7/8，leikwan_pipe 空。
- 预热池（含备线池）默认只在规则用户的「主入口」机上开（第一步界面就是这么写的；省连接）；过滤 / 日志快切 / 回查在每台入口机都配。
  现有各机上的预热由「采用主机现值」记成覆盖值保留（阿里广州 / 火山广州的 263 预热 10 不变）。
- 界面（子任务 Claude，同一 worktree）：host-manage.tsx 新的「面板接管」区（实时状态、差异弹窗、接管/流量统计开关带确认、按面板覆盖、
  立即同步、⚠ 列表）、利群入口标签 ⚠、规则行 ↑↓ 流量 / 已暂停 / 待切换 + 「切到新落地」、专线弹窗 流量计算 + 倍率。
  tsc 0 错、eslint 0、`npm run build` 过；画廊 /root/mmwx-preview/leikwan-step2/（38 张，桌面 + H5 390/360，浅色 + 深色，页面 JS 错误 0）。
- 测试汇总：go-backend `go test ./...`（带 PG 18）全部 ok；go-gost / go-gost/x 全部 ok（x/socket 改动后单独重跑 ok）；go vet 两个模块 0；
  第一次全量跑时 PG 临时容器（tmpfs 512M）中途崩了导致一批 PG 子测试连不上库，换磁盘目录重跑全部通过（不是代码问题）。
- 发布：分支 `maintenance/3.0.27-fork.140-leikwan-step2` = feature @ fd638436，tag `3.0.27-fork.140`（11-17 06:00 台湾推送）。

### 第二轮（leikwan-panel-step2-r2，11-17 06:03 台湾起）
- Images 43965671735 绿，release 3.0.27-fork.140 = Latest、10 个资产；发布包 compose v6 和 fork.139 只差 tag。
- 回滚检查（生产库副本，--network none）：fork.140 迁移后 fork.139 正常起来、0 重启、52 条规则、7 条利群规则不变；fork.140 在副本上跑满第一次屏蔽检查：0 条屏蔽。
- 换面板前算的屏蔽清单：7 条利群规则都属于 admin（99999 GB、2056 年到期、启用），没有暂停的单服务器配额 → **现在 0 条会被屏蔽**。
- 面板：备份 /opt/flvx-svc/rollback/pre-fork140-20261116T221434Z（库 quick/integrity ok）；换面板 06:14:41–06:14:53 台湾；30/30 在线，52 条规则不变，
  重连 30 台 updated 0 / failed 0，利群表旧列逐行一致（新列默认值），0 重启、0 错误；之后 prune → 只留 pre-fork139 + pre-fork140。
- OTA（只 4 台利群入口机，先各自 backup-agent-140.sh）：阿里广州 06:17:14（10 s）、火山广州 06:17:45（10 s）、火山上海 06:18:10（8 s）、
  百度云广州 06:19:09（39 s）→ fork.140；利群文件哈希 + mtime、服务状态都没变（只是 FLVX 自己的 flvx_acct 表重建后顺序变了）；其他 26 台版本不变。
- host-backup.sh：Debian 12 coreutils 9.1 的 `cp -a --parents` 在新建上级目录时报 No such file → 改成 tar 拷贝（同样的路径 + ultra-forward 单元 + /usr/local/bin/leikwan-* + ultra-client）。
- 接管上线（各机先整机备份 /root/backup-<时间>-before-flvx-takeover/RESTORE.txt → 采用主机现值 → 差异 0 → 接管开 → 核对 → 流量统计开）：
  - 阿里广州 06:21:48 接管开：6 个文件全部一致，agent applied rev 3、written 无；文件哈希 + mtime 不变；7/7 ready；主线 263/264 SS-2022 3/3；
    force 文件强制 263 走备线 17 s：backup_conns=4 via_front=4、3/3 通过，已撤（force_end）。06:23:38 流量统计开：5 MB 下载 → nft u/d = 99 065 / 5 181 877，
    规则入账同值，阿里广州单服务器配额同值；下一次上报没有重复入账。
  - 火山广州 06:24:44 接管开：7 个文件（含手机备线 nft）一致、written 无、文件不变；主线 263 3/3，手机备线口 19047（DNAT 到 263 备线）3/3；06:25:24 流量统计开，3 MB → 59 175 / 3 113 625 同值入账。
  - 火山上海 06:26:17 接管开：2 个文件（logswitch unit、首包合并）一致、written 无、无 drop-in；主线 263 3/3；它的日志快切只记录、备线目标（深港ixp1）从这台连不上（原有，所以从不切）→ 强制备线不适用；
    06:28:07 流量统计开，2 MB → 40 313 / 2 076 542 同值入账。
  - 263 规则累计 198 553 / 10 372 044 = 三台计数之和（只计一次）。
- 99U：在共享落地「Boil HKT 99u」用面板正常路径建了 admin 名下 SS2022 落地节点 194「利群263 99U」:30000、195「利群540 99U」:30001（mmwxc-flvx-core，
  不碰 xray :10016 / lkbk-core :10017 / :10019，PID 不变）；直连实测 3/3（出口 99U）；规则 263 → 194、540 → 195 标「待切换」；三台已接管主机期望不变、没写文件。
  百度云广州不接管时，按「换落地」会被拒（首包过滤保护：「入口机 百度云广州 还没接管…」）。
- 百度云广州：warm-headroom-r2 07:14 按中止规则提前结束（summary.md 07:2x，unit inactive）；最终值 burst=off、首包合并 off（23:10 已关）；
  重采夹具（只读）和第一轮夹具完全一样 → 不用改测试数据。整机备份 /root/backup-20261117-072757-before-flvx-takeover；采用主机现值 7 个文件差异 0
  （另外三台仍 0）；07:28:23 接管开：written 无、文件哈希 + mtime 不变、无 drop-in、7/7 ready；主线 263 / 540 3/3，手机备线口
  19263/19264/19279/19280/19281/19282 全部 3/3；force 文件强制 540（没用户）走备线 16 s：backup_conns=4 via_front=4、3/3，已撤。
  07:30:15 流量统计开：540 下载 3 MB → 59 157 / 3 092 157，入账同值，百度云广州单服务器配额同值。
- 收尾核对 07:3x：4 台 接管 + 流量统计 开、差异 0、无漂移、无写入、无 ⚠、屏蔽规则 0 条；30/30 在线、52 条规则、其他 26 台 agent 版本不变；面板 0 重启。
  期间 Bestvm sg 年付 / Bestvm HK STD 06:45–06:48 控制通道 pong 超时几次（agent 没重启，重连后 updated 0），是那边网络抖动，和发布无关。
- 观察（没改）：日志快切切到备线时，那段流量在利群规则（入口机 nft）和 FLVX 备线转发（148/587/595/599/601/619，都是 admin 的）上各计一次。

