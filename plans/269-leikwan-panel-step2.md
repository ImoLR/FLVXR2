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

- [ ] 读 memory / 266 / 267 / 268 / 094 / 096 / 主机布局（只读），建 worktree + 计划文档。
- [ ] 主机文件夹具（4 台，去密钥）→ go-gost/x/socket/testdata/leikwan-host/。
- [ ] agent：发现 + 解析 + 语义 + 上报（主机状态）+ 单测（4 台夹具）。
- [ ] agent：接管写入（备份、原子写、漂移、窗口、force）+ 渲染单测（渲染后再解析语义相等）。
- [ ] agent：流量计数表 + 屏蔽（nft 脚本生成 + 读取）+ 单测。
- [ ] 面板：模型 + 迁移、上报入库、期望生成 + 学习主机现值（4 台夹具语义差异 0 的测试）、同步下发。
- [ ] 面板：流量入账（去重）、重置、屏蔽/解除 + 契约测试（SQLite + PostgreSQL）。
- [ ] 面板：99U 落地节点准备 + 换落地带备线（自动 + 链接）+ 密钥 + 待切换标记 + 测试。
- [ ] 前端：利群入口区（主机状态、接管开关 + 差异、漂移 ⚠、按面板覆盖、采用主机现值）、规则行流量 + 待切换；`npm run build`。
- [ ] `go test ./...`（两个模块）+ go vet + CI 绿；截图画廊 /root/mmwx-preview/leikwan-step2/。
- [ ] 发布：tag 下一个 fork、CI + 镜像 + 资产；面板备份 + 换面板；只 OTA 4 台利群入口机（每台备份 + 回滚）；保留 2 份备份。
- [ ] 上线：百度云广州（A/B 结束后）接管开 → 语义差异 0、leikwan status、日志、手机端口、SS-2022 实测 → 阿里广州 → 火山广州 → 火山上海；流量统计窗口内开。
- [ ] 99U 落地节点建好 + 新链接；总结 /root/flvx-workers/runs/leikwan-panel-step2/summary.md + final.md；memory 文本写进 final.md。

## 记录

