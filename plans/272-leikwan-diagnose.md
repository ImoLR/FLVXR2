# 272 — 利群规则「诊断」+ 复制链接提示 + 旧主入口停用 + 利群进落地链接/订阅 + 利群落地可选外部节点 + 用户只给订阅

基线：tag `3.0.27-fork.142`（b2be15e9），分支 `feature/3.0.27-leikwan-diagnose`，worktree `/root/projects/flvxx/flvxx-leikwan-diag`。
用户 11-17 ~16:30–17:20 台湾批准设计稿 http://104.145.236.29:8791/leikwan-diagnose/（改一处：用户第一跳只看自己的主入口，管理员看全部）。
发布：面板 + 只 OTA 4 台利群入口（百度云广州 / 阿里广州 / 火山广州 / 火山上海，先一台金丝雀）。

## 现场（11-17 17:1x 台湾只读）

- 利群客户端在每台入口机上：`/run/leikwan-fast-failover/forward-<id>.offered.json`（0600，root）= 当前 4 个候选入口
  `{at, offered:[{endpoint:"IP:5438", ref}]}`；`lines.json` = 见过的 ref→endpoint；`/run/ultra/forward-<id>.status.json`
  的 `latest_tcp` / `active_udp` 有 `selection_ref`（= 在用入口的 ref）+ `rtt_ms`（UDP 还有 p95、probe_sent/received、selected_at）。
  4 台都有这些文件。厂商文件里没有运营商 / 地区。
- 屏蔽：agent 早就按 sync 里的 `block` 端口丢包（`inet flvx_leikwan` pre/post，lo 不管），和接管开关无关 → **按主机屏蔽只改面板**。
- 生产：7 条利群规则都是 imolr（管理员）的导入规则，手动落地；管理员授权主入口 = 百度云广州。
  按主机计数（11-17 06:2x 起）：263（:10018）在阿里广州 7.2 MB↑ / 35 MB↓（非主入口，真流量）；其余非主入口只有几百字节（扫描 / 测试）。

## 设计

### 1 复制链接提示（只前端）
- `rule-actions.tsx` `link()`：管理员复制手动落地规则时只出一条自定义 toast（`toast.custom`，15 s，悬停暂停，点 ✕ / 点一下关闭），
  内容按设计稿 08。不改全局 Toaster 默认值。

### 2 利群规则诊断
- agent 新命令 `LeiKwanDiag {forwardId, count≤5 (默认 3), timeoutMs≤3000}`（只在利群入口机上有意义）：
  读 offered.json（+ lines.json 映射旧 ref）+ status.json；对每个候选入口 IP:5438 依次做 TCP 握手测时（count 次，间隔 100 ms，RST 关闭，
  同一时刻只有 1 个连接，最多 4×5=20 次）；返回 `{ingresses:[{endpoint, inUse:"tcp"|"udp"|"tcp+udp"|"", ok, avgMs, loss, error}], tcp:{rttMs, at}, udp:{rttMs, p95Ms, sent, recv, at}, state}`。
  不碰厂商控制 socket，不改任何文件，不留后台任务。老 agent 不认识命令 → 行显示「入口机 agent 需要升级」。
- 面板 `POST /api/v1/leikwan-rule/diagnose/stream {id}`（NDJSON：start / item / done，和规则页一样；非流式 `…/diagnose` 兜底）。
  每条结果 `{section, title, sub, inUse, success, estimate, averageTime, packetLoss, message}`，section = hop1 / hop2 / exit / backup / other。
  - 第一跳：主入口机 LeiKwanDiag。**用户**：一行「主入口 → 利群专线」（在用入口的值，没有在用就取最快），不出 IP；
    **管理员**：主入口的每个候选入口一行（① … ④ + IP + 运营商/国家），外加折叠的「其他利群入口机」：其余入口机各一行（最快入口）。
  - 全程 = 利群客户端在主入口机上报的 rtt（UDP 探测优先，带丢包；没有就用 TCP 最近一次，写明多久以前），标「利群客户端实测」。
  - 第二跳 = 全程 − 第一跳（在用），**一律标「估算」**：落地机上看不到利群出口地址，没有诚实的实测办法；不把估算当实测。
  - 出口测试（只有面板落地）：落地服务器是 FLVX 节点时用现有 TcpPing（google / bing / cloudflare 轮换，和隧道诊断一样）；共享落地不测，写原因。
  - 备线：有备线时复用规则诊断（prepareForwardDiagnosis）跑备线转发，各跳合成一行（延迟相加，失败写哪一跳）。
  - 每个探测有超时，整个诊断 ≤ 40 s；同一用户 10 s 内只能诊断一次（保护利群入口）。
- 运营商：小的 IP 段表（UCloud / 华为云 / 腾讯云 / 阿里云 …）+ 内置 geoip 国家；不认识的只写国家。只给管理员。

### 3 换主入口后旧入口停用（只面板）
- 利群全局设置新增 `entryLock`（「只开主入口」，默认关；生产在迁移完豁免后打开）。
- 打开后：规则端口在不是规则主人主入口的入口机上进 `block`（按主机算）。例外：规则开了「所有利群入口都可用」（`leikwan_rule.all_entries`，只管理员能改）；
  主人没有授权 / 没有主入口；宽限中的旧入口。
- 宽限：授权换主入口时记 `leikwan_grant.prev_entry_node_id` + `entry_changed_at`；旧入口保留到主人任一订阅在换入口之后被拉取，或满 24 h，先到为准。
  订阅拉取钩子在 `subPublic` 写完以后（每个用户每分钟最多一次查库）；面板循环每 5 min 检查到期。连续换两次：只保留上一个入口。换回旧入口：宽限清掉。
- 界面：授权弹窗换主入口提示加一句宽限说明；授权行宽限中显示小 chip「旧入口 X 保留中」；规则行（管理员）显示 / 切换「所有入口可用」。
- 生产迁移：发布前只读看 4 台的按端口计数 + 厂商 meter 计数 + `ss`（对端不是本机地址的连接）；非主入口上 7 天内有真流量（>64 KB）或有活连接的规则
  → 打开「所有利群入口都可用」，然后再开 `entryLock`。

### 4 利群规则进落地「复制链接」和订阅
- 落地 `landing/node/links`：加 kind `leikwan`（主人在这个落地上的利群规则，名字「<专线名> · <主入口名>」，地址 = 主入口:端口）。
  管理员给完整链接；普通用户不给链接，给 `subToken`（自己的利群汇聚订阅）+ 说明。
- 订阅新 ref：`leikwan_rule {leikwanRuleId}`（key `k:<id>`）、`leikwan_all`（key `ka`，跟随全部利群规则）。拉取时现算：主入口 IP + 监听端口，
  面板落地 → 落地链接换地址；外部节点 → 复制上游、换 server/port，原来是域名的 SNI / ws host 补上原域名。手动落地不可选（灰，写原因），`leikwan_all` 跳过。
- 订阅编辑器加「利群汇聚」组（单条 + 「全部利群规则（自动跟随）」），只加不改现有组。

### 5 利群规则落地可选外部节点
- `leikwan_rule.external_node_id`；新建 / 换落地可选主人自己的外部节点（ShareURI 支持的协议）；目标 = 节点 server:port；备线同手动落地（备线到同一 host:port）。
  首包过滤只对面板 SS2022 落地，外部节点和手动一样不过滤。手动 host:port 仍只管理员。

### 6 利群「生成订阅」（推荐订阅，节点链接照常给；用户 11-17 ~17:30 台湾定稿，替换早先「只给订阅」的草稿）
- 利群汇聚页头一个醒目的主色按钮「生成订阅」（用户；管理员可选任一已授权用户）→ 弹窗直接给出一条订阅链接（包含这个用户全部利群规则，
  自动跟随新规则和换主入口），客户端格式按 UA 自动识别（同现有订阅），复制 + 二维码 + 说明「推荐用订阅：换入口时刷新订阅即可，不用改地址」。
- 弹窗「详细设置」→ 跳到订阅模块打开这条订阅的编辑器（加别的落地 / 规则转发节点、利群汇聚组、集合或分流）。
- 这条订阅就是普通订阅行（名字「利群汇聚」，`user_subscription.auto_kind='leikwan'` 只用来「第一次点就建，之后重用、不重复」）；
  用户可以改、可以删（删了再点生成订阅就新建一条）；编辑时去掉了「全部利群规则」，再点生成订阅会补回来；不占 5 条上限。
- 规则行「复制链接」（节点链接）对用户和管理员都不变；落地页复制链接里利群条目对所有人都是普通节点链接；接口不限制利群节点链接。

## 检查清单

- [x] 读 memory / 设计稿 / 现场（4 台只读），建 worktree + 计划文档。
- [x] 11-17 ~17:30 协调员转达用户更正第 6 项（推荐订阅、不限制节点链接）：后端 link / 落地链接 / 订阅删除限制都改回，契约文档同步。
- [ ] 面板：模型新列（grant prev_entry/entry_changed_at、rule all_entries/external_node_id、subscription auto_kind）+ 全局 entryLock。
- [ ] 面板：按主机屏蔽 + 宽限（订阅拉取钩子 + 24 h）+ 豁免接口 + 授权视图 grace + 契约测试（SQLite + PG）。
- [ ] agent：LeiKwanDiag 命令 + 单测（夹具）。
- [ ] 面板：诊断接口（流式 + 兜底）+ 运营商表 + 单测。
- [ ] 面板：外部节点落地（options / create / update / 备线 / 链接）+ 契约测试。
- [ ] 面板：订阅 ref leikwan_rule / leikwan_all + 「生成订阅」（建一次、重用）+ 落地链接 leikwan 条目 + 契约测试。
- [ ] 前端（子代理，同 worktree）：toast、诊断按钮 + 弹窗（桌面 + H5）、生成订阅按钮 + 弹窗 + 详细设置跳订阅编辑器、外部节点落地、
      豁免开关、宽限提示/chip、entryLock 开关、落地复制链接 leikwan 条目、订阅编辑器利群汇聚组、订阅卡片「利群汇聚」标；`npm run build`、eslint。
- [ ] `go test ./...`（go-backend -timeout 30m，SQLite + PG 18）、go-gost / go-gost/x、go vet、CI 绿。
- [ ] 实验室 + 画廊 /root/mmwx-preview/leikwan-diagnose-built/（和批准的 PNG 对照）。
- [ ] 发布：tag 下一个 fork、CI + 镜像 + 资产；面板备份 + 换面板；生产迁移（只读计数 → 豁免 → entryLock）；
      OTA 4 台利群入口（金丝雀一台先，检查利群转发 / 日志快切 / 预热）；保留 2 份备份。
- [ ] 验证：节点全在线、规则数不变、利群表除新列外不变、管理员诊断真实规则、aws-hk 测非主入口超时 / 主入口通。
- [ ] 台账 272、memory、summary。

## 记录
