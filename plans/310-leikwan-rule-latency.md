# 310 — 利群汇聚：转发行的「利群 #id」换成延迟 tag + 诊断「在用」更醒目

Branch `feature/3.0.27-leikwan-latency-tag` (from prod fork.173 branch head `671aa669`). Task
`leikwan-rule-latency`, 2026-11-25/26 TW. **BUILD ONLY** — no tag, no release, no OTA, prod untouched
(ships later with other changes).

## User request (11-25, voice; A2 11-26 ~01:10 TW)
A. Under each 专线 every rule row shows its number (「利群 #263」) — not needed. Show the rule's latency
   there instead, as a small tag, from the per-minute measurement we already have.
A2. Every 专线 header (admin tree) gets one tag = the lowest current value among its rules. Label it
   honestly: the value is 入口机 → 利群专线出口 (the client's 全程), not 到落地. In the im.gamer App,
   手机本地 rows should show the phone client's own value if the bridge can give it cheaply.
B. 诊断 (整段): the 入口 row the LeiKwan client uses has a faint green 「在用」 — make it a filled tag
   (solid colour, white text), clearly stronger, not blue, unlike the amber 「端内」. Desktop + H5.
Dropped by the user himself: a 「端内」 line monitor (「这个监测还是不要用了」) — not built.

## Design
- **Agent** (`go-gost/x/socket/leikwan_host_linux.go` `read()`): the status.json parse it already does
  every minute also takes `latest_tcp` / `active_udp` (same `lkDiagSel` type + `lkDiagClient` helper as
  诊断) → `LKForward.rttTcp` / `rttUdp` = {rttMs, p95Ms, sent, recv, ageS}. Raw values: the UDP-vs-TCP
  choice + the 2000 ms sanity cap stay in ONE place, the panel's `lkClientPick` (now also behind 诊断's
  `lkDiagSource`), so the rule tag and 诊断 always agree and a policy change needs no OTA.
  `LKHostState.rtt = 1` marks an agent that reports it (old agents → 「入口机 agent 需要升级」).
  No new file reads, dials or goroutines. `lkReportKey` ignores the rtt fields, so a jittering rtt never
  makes a report due by itself (it rides along with the reports that go out anyway).
- **Panel**: the host report is already stored on every ingest (`leikwan_host.report` + `reported_at`);
  `lkForwardView` / `lkStateView` now keep the new fields. The rule list reads it (one
  `ListLeiKwanHosts` query per list call, only the shown entries' reports parsed) — no new writes, no
  repo/DB change. Each rule (admin + user list, users only their own, never `forwardId`) gets
  `latency {ms, src, ageS}` for the shown entry (`entry.nodeId`) or `latencyNote`:
  授权没有主入口 / 入口机离线 / 还没有数据（入口机最近没有上报） / 入口机 agent 需要升级 /
  入口机上没有这条利群转发 / 利群客户端还没有数据 (round 1 also had 利群客户端很久没有新数据 > 10 min —
  dropped in round 2, see below).
  Fresh = report ≤ 3 min old (traffic counting on) or ≤ 7 min (off). `ageS` = report age + client age.
- **UI** (`pages/leikwan/panel/rule-latency.tsx`):
  - admin desktop row: 「利群 #id」 pill → `RuleLatencyTag`; admin H5 card: `#id` chip → the tag, plus a
    「延迟」 field in the open card (what it measures / why grey — touch has no hover text);
  - user page: desktop rows get the tag first in the pill row; H5 rows keep their one protocol chip
    (11-14 rule) and get the 「延迟」 field in the open card;
  - 专线 header (admin desktop + H5): `LineLatencyTag` = lowest value among the rules that have one,
    hover: which rule / which 入口机 / N of M rules have a value; no values → no tag;
  - tag text 「62 ms」, tone = `latencyTone` (< 100 green, 100–180 amber, > 180 red) via `MsTag`; no
    value = grey 「— ms」; hover (round 2 wording): 「全程：入口机 X → 利群专线出口 · 利群客户端选线时测的（UDP）
    · 3 小时前 · 利群 #263 · 导入时登记的利群转发 · 面板读数约每 2 分钟刷新」 (number + import note only
    here, admin only);
  - 手机本地 rows (`usePhoneRule` → `pv`): **no tag** and the header ignores them — the App bridge has
    no cheap call for the phone's 全程: `diag` makes ≤ 3 TCP handshakes to the ingress + ≤ 3 to the 备线
    per call (bridge contract), so polling it per rule would be a background per-item probe. A cheap
    path needs an App change (e.g. PhoneState forwards carrying the core's status rtt, no handshakes).
  - every other forward number stays (备线 #id, 待切换 利群 #id, host-manage, line-dialog, usage).
- 诊断 「在用」: filled emerald tag (`bg-emerald-700` / dark `bg-emerald-600` + ring), white text + white
  dot, same on desktop and H5 (H5: moved from the grey sub line into the title line, like desktop).

## Report cadence (measured on prod, read-only, 11-26 01:08–01:14 TW)
`leikwan_host.reported_at` of 百度云广州 (58) and 阿里广州 (60), sampled every 15 s: after one burst at
01:09 (a sync answer) the steady interval is **120.0 s** on both. Why: traffic counting is on on all 4
利群入口 (`leikwan_host.traffic = 1`); the agent ticks every 60 s and marks a report due when
`now − lastTrafficSent ≥ 60 s`, but the report leaves ≤ 1 s after the tick (with the next system info),
so the next tick is 59.x s later → not due → every other tick = **2 min**. With traffic off the same
phase effect makes the 5 min cadence **6 min**. The rtt therefore refreshes every ~2 min on today's
hosts (6 min on a host with traffic off) — accepted (making it 1 min would double the per-report DB
writes, which this task must not add). Freshness windows (3 / 7 min) and the hover text 「约每 2 分钟」
follow this. The client's own values are fresh: status.json `selected_at` on 阿里广州 was ≤ 30 s old
for both TCP and UDP (read-only check 01:10 TW).

## Tasks
- [x] Agent: rtt fields in `read()`, `rtt` capability flag, `lkReportKey` ignores rtt
- [x] Agent unit test (status.json fields incl. bogus UDP 60000, report key / due unchanged by rtt)
- [x] Panel: `lkClientPick` shared with 诊断, report view fields, rule `latency` / `latencyNote`
- [x] Panel tests: unit (pick / fresh / stale traffic on-off / offline / old agent / no forward / too
      old) + contract (ingest → admin list + user list, own rules only, no forwardId, offline)
- [x] UI: admin desktop row + H5 card tag, 专线 header min tag, user page rows, 手机本地 none
- [x] UI: 诊断 「在用」 filled tag (desktop + H5)
- [x] Report cadence measured on prod (read-only) and written above
- [x] go test (socket, backend handler + contract, SQLite; PG), npm build + tsc + lint
- [x] Screenshots gallery `/root/mmwx-preview/leikwan-latency-tag/`
- [x] Push branch, CI green
- [x] Summary (what a release needs)

## Results (11-26 TW)
- `go-gost/x/socket` all ok (incl. new `TestLeiKwanHostReadClientRtt`); `go vet ./socket/` ok; `go-gost`
  linux build ok (a windows build fails in `x/landing` `syscall.Statfs` — unrelated, CI builds linux only).
- `go-backend` `go test ./...` with `FLVX_POSTGRES_TEST_DSN` (SQLite + PostgreSQL): all packages ok except
  one `TestTLSBlockGuardContract/sqlite` failure = pre-existing flake (hard-coded port 20050 in a 1000-port
  range where earlier forwards get random ports; base 671aa669 fails the same way 1/150, head 0/150).
  New `TestLeiKwanRuleLatencyFor` / `TestLeiKwanRuleLatencyView` / `TestLeiKwanRuleLatencyContract` ok.
- `vite-frontend` `npm run build` (tsc + vite) ok; eslint on the touched files 0 errors.
- CI run 45667341143 (1bbd5a8a) green: frontend, backend, agent, PostgreSQL contract.
- Lab (prod-DB copy, isolated netns, fake rtt reports): 21 screens, gallery
  http://104.145.236.29:8791/leikwan-latency-tag/ — the App mock made no `diag` bridge calls.

## Release needs (not done here)
- Panel (frontend + backend) **and** agent OTA of the 利群入口 hosts: 百度云广州 (58, online),
  阿里广州 (60, online), 火山广州 (52, offline), 火山上海 (61, offline) — all on fork.143 today.
- No DB change (reads the already stored `leikwan_host.report`).
- Old agents / before the OTA: every row grey 「— ms」 = 「入口机 agent 需要升级」, 专线 header no tag;
  诊断 「在用」 works with the panel alone. Offline hosts stay 「入口机离线」 until they come back + OTA.

## Round 2 (11-26 TW, task leikwan-rule-latency-r2 — brief A3)
Prod sample (coordinator, read-only, 阿里广州 fwd 263): `active_udp` = 30 UDP probes taken **once** when the
LeiKwan client selects a path (`selected_at` unchanged for > 5 min, can stay so for hours while the path is
kept); `latest_tcp` = the latest user TCP connection (jumps 16–108 ms with traffic). An old UDP value is
still the client's current path, so round 1's 10-minute cap hid the right number.
- Backend (`leikwan_rule_latency.go`): **no age cap on the client value** — `lkLatencyMaxAgeS = 600` and
  `lkLatStale` 「利群客户端很久没有新数据」 removed. The tag is shown whenever the host report is fresh
  (3 / 7 min, online, plan-310 agent — unchanged); `ageS` = report age + the client's `ageS` (from
  `selected_at`) as before, so an old pick shows e.g. 3 h. **TCP fallback: no limit either** — kept simple:
  it is only used when there is no usable UDP, it is the client's latest real measurement, and the hover
  shows its true age (an hour-old TCP value reads 「1 小时前」, not as live). UDP-first pick = `lkClientPick`
  unchanged; 诊断 unchanged.
- UI (`rule-latency.tsx`): hover / H5 「延迟」 line no longer claim 「面板约每 2 分钟更新」 for the
  measurement. UDP 「利群客户端选线时测的（UDP）」, TCP 「利群客户端最近一条连接（TCP）」, then the age
  (秒 / 分钟 / 小时 / 天前, floored), then (admin) 利群 #id + 导入时登记的利群转发, then a short trailing
  「面板读数约每 2 分钟刷新」. Same for the 专线 header tag. File header comment updated.
- [x] Backend: drop the 10-min cap (UDP + TCP), keep report freshness / online / new-agent checks
- [x] Unit tests: UDP 3 h old + fresh report → shown, ageS ≈ 3 h; old TCP fallback shown; stale report → none
- [x] UI wording (row tag, 专线 header, H5 「延迟」 line) + age text for hours / days
- [x] go test handler + `TestLeiKwanRuleLatencyContract` (SQLite + PG) ok; npm build + eslint ok
- [x] Re-shot the changed screens (admin desktop / H5, user desktop / H5; 广台 Hinet = UDP picked 3 h 14 min
      ago → 「3 小时前」), gallery marks them 「第 2 轮重拍」 (diag / App / old-agent screens unchanged)
- [x] Push, CI green
- [x] Summary `runs/leikwan-rule-latency-r2/summary.md`; RELEASE-LEDGER row 310 updated


## Round 3 (11-26 TW, task leikwan-latency-landing — user decision 11-26 ~05:45 TW)
User (after a long discussion, measured facts in memory leikwan-rule-latency): rows show **到落地**, the 专线
header shows **端内**, and 线路监测 gets a 「利群专线端内」 chart of every 专线. Still BUILD ONLY (ships with 311).

### Design
- **Agent** (`go-gost/x/socket/leikwan_seg_linux.go`, inside the existing 1-min 利群 host tick, no new goroutine):
  for each forward with a UDP selection the ingress is resolved like 诊断 (`active_udp.selection_ref` →
  `offered.json`, else `lines.json`); then ONE netlink socket per tick, per distinct ingress port two
  `SOCK_DIAG_BY_FAMILY` dumps (AF_INET + AF_INET6) with a kernel bytecode filter `dport ≥ p && dport ≤ p`,
  `ESTABLISHED` only, `INET_DIAG_INFO` → `tcpi_rtt` / `tcpi_min_rtt`; sockets to the exact ingress IP:port are
  grouped → `forwards[].seg = {endpoint, ms (lower median srtt), minMs, conns, err}`; `state.rtt = 2`.
  No probe, no new connection, no `ss` subprocess. `lkReportKey` ignores seg (rides along with the reports that go out
  anyway, ~2 min). Conns 0 = the in-use ingress has no connection now → no 入口段 (gap).
- **Panel per-minute 利群 pass** (`leikwan_land.go`, memory only, starts :05 of every minute):
  - 专线出口 → 落地: per native landing port of a 利群 rule (manual / external / shared landings skipped) the exit =
    `lkExitCands(LandingPeerRtt)[0]` (the 诊断 pick; FLVX node IPs = 备线 exits excluded), re-read ≤ every 10 min
    (every 2 min while no exit is known or the echo fails — exits rotate); ONE `ServiceMonitorCheck` icmp echo a
    minute per (landing node, exit IP) pair, deduped across rules, ≤ 4 in flight; offline landings skipped.
    Exit never answers / no answer for > 5 min → the connection rtt (min_rtt) marked ≈; a single lost echo keeps
    the last answer ≤ 5 min. Errors logged ≤ 1 / 10 min, nothing per minute.
  - 端内 readings: every online 利群入口 with a fresh plan-310-r3 report → per forward with UDP 全程 (lkClientPick
    = udp) and seg conns > 0: 端内 = max(0, UDP − 入口段). Auto pick per 专线 = the reading of the last pick while
    it exists (no host / forward jumps), else the freshest 全程.
  - 线路监测 samples for the 「利群专线端内」 lines (below).
- **Rule rows** (`leikwan_rule_latency.go`): `latency = {ms = totalMs + landMs, src, ageS, totalMs, landMs, landAgeS,
  approx}` — only UDP 全程 (latest_tcp is a user connection that already includes the landing) + a landing value;
  otherwise no tag (`latencyNote` keeps the reason for the API only). `landLatency` (出口 → 落地 alone) for the App.
  Note: 诊断 (306) shows 到落地 = max(全程 + n, 入口段 + C); the row uses the user's definition 全程 + n.
- **专线 header** (admin line list `endIn`): the auto pick → 「端内 X ms」 amber (like 诊断), hover: 全程 − 入口段,
  入口机, rule (+ 利群 #id admin), ingress label, age, number of readings. No reading → no tag.
- **App 手机本地 rows**: 本机 → 落地 = the phone's own UDP 全程 + landLatency. The bridge has no handshake-free value
  today (`diag` = TCP handshakes, r2 finding), so the panel reads an optional `PhoneForward.total` from getState
  (no extra bridge call); **needs an App update** that fills it from the core's status — until then no tag there.
- **线路监测 kind 「利群专线端内」** (`line_monitor_endin.go`): `line_monitor_line.kind = 'lk_endin'` +
  `leikwan_line_id`; `source_node_id` = fixed 利群入口 or 0 = auto. Never a TcpPing target. One sample a minute
  (status ok, ms10 = 端内) + its parts in the new table `line_monitor_part` (same packed key: total10, seg10,
  via_node), written by the existing batched flush, pruned with the samples (same key range). No reading = no
  sample = 无数据 (never 断流). Charts with such a line use ≥ 60 s buckets (per-line slot in `linemon.Accumulator`).
  Data API adds per bucket `total / seg / via` + `viaNames`; tooltip 「全程 X − 入口段 Y · 入口机 Z」; stats show
  「—」 for 可用率 / 丢包 / 断流. Page button 「+ 利群专线端内」 = new chart prefilled with every active 专线 (auto).

### Prod numbers (read-only 11-26 TW)
- 专线 (leikwan_line): 1 利群·广港 (CAN→HKG), 2 利群·沪日 (SHA→TYO), 3 利群·广台 (CAN→TPE), 4 利群·福台 (FOC→TPE).
- 6 rules, all native landings on their own ports: 263 → 99u :30000, 264 → JP B :31004, 280 → 马年 :10020,
  281 → Fachost :30000, 282 → 马年 :10019, 615 → Fachost :30001 → **≤ 6 (landing, exit) pairs = ≤ 6 ICMP echoes / min**
  (1 packet each way), LandingPeerRtt (passive) ≈ 6 per 10 min (+ retries while an exit is unknown / silent).
- 利群入口 connections: 阿里广州 75 ESTABLISHED, 19 to :5438; 百度云广州 95 / 27 → per tick 2 filtered dumps return
  ~20–30 sockets; ≤ 12 small /run file reads (offered / lines json). In-use ingress of 263 on 阿里广州 had 10
  connections, srtt 1.9–3.2 ms (lower median ≈ 2.5); 280's in-use ingress 2 connections, 5.7–5.9 ms.
- DB: no new per-minute writes for rows / header; the 「利群专线端内」 chart (4 lines) adds 4 samples + 4 parts a
  minute in one batch → ≈ 173 k rows each table over 30 days (a few MB).

### Release needs
- Panel (frontend + backend) + **agent OTA of the 利群入口** 百度云广州 58 + 阿里广州 60 (火山广州 52 / 火山上海 61 offline);
  **landing nodes need nothing** (LandingPeerRtt + ServiceMonitorCheck icmp are already used by 诊断 on all four
  landing machines: Boil HKT 99u 63, Bestvm JP B 18, Boil Hinet 马年 62, Fachost seednet 黑五 45).
- **DB change: yes** — AutoMigrate adds `line_monitor_line.kind` + `leikwan_line_id` (defaults '' / 0) and the new
  table `line_monitor_part`; no data migration.
- Between the panel swap and the OTA: today's 利群入口 agents (fork.143) report no rtt / seg → rows show no tag and
  headers no 端内 (诊断 keeps working). After the OTA: rows show 到落地 with the next host report (≤ 2 min; the
  landing pass already runs), headers 端内 likewise; old (r2-only) agents would give rows but no 端内.
- **Post-release data op (release worker)**: 节点 → 线路监测 → 「+ 利群专线端内」 → check the 4 lines (广港 / 沪日 /
  广台 / 福台, 入口机 自动) → 保存. Or API: `POST /api/v1/node/line-monitor/chart/save`
  `{"name":"利群专线端内","lines":[{"kind":"lk_endin","leikwanLineId":1,"sourceNodeId":0},{"kind":"lk_endin","leikwanLineId":2,"sourceNodeId":0},{"kind":"lk_endin","leikwanLineId":3,"sourceNodeId":0},{"kind":"lk_endin","leikwanLineId":4,"sourceNodeId":0}]}`
  (admin token). Only after the 利群入口 OTA (otherwise the chart stays 无数据). Uses 4 of the 40 line slots (4 used today).

### Tasks (round 3)
- [x] Agent: ingress of the UDP selection, 入口段 via kernel-filtered sock_diag, seg in the report, rtt = 2
- [x] Agent tests: netlink request bytes, message parse (v4 / v4-mapped / foreign seq / non-ESTABLISHED / error / truncated), stats, report read with fake dump (offer + lines.json, no conn, unknown ref, dump error, report key), real loopback dump
- [x] Panel: per-minute 利群 pass (exits, echoes, ≈, hold, offline), 到落地 rows + landLatency, header endIn
- [x] Panel: 线路监测 kind (model, part table, save / validate, list / options, passive samples, query slot + parts, prune)
- [x] Handler tests (TestLeiKwanLandTick / TestLeiKwanEndIn / TestLeiKwanRuleLatencyLanding, r2 tests → r3) + contract (TestLeiKwanEndInContract, TestLeiKwanRuleLatencyContract r3; SQLite + PG)
- [x] UI: rows / H5 field / user page / 手机本地 / header 端内 / 线路监测 dialog + one-click + tooltip + stats
- [x] npm run build + eslint (touched files) + tsc
- [x] Lab screenshots (第 3 轮) → gallery
- [x] Exit re-read cadence fix (finishing pass, task leikwan-latency-landing-r2): re-read ≤ every 2 min while no exit is known or an exit that answered ping went silent; never-answering exit only every 10 min; same silent exit after the 5-min hold → no-ping exit
- [x] Push, CI green — head d5172bda, CI 45718449237 (frontend, backend, agent, PostgreSQL contract)
- [x] Summary `runs/leikwan-latency-landing-r2/summary.md` (+ copy in `runs/leikwan-latency-landing/`), RELEASE-LEDGER row 310

### Results (round 3, finishing pass 11-26 TW)
- Code head d5172bda (r3 = 5dea18b6 agent + panel, 6d659ab3 UI, d5172bda exit re-read cadence); CI 45718449237 green
  (frontend, backend, agent, PostgreSQL contract).
- `go test ./internal/http/handler/ -run 'LeiKwan|LineMonitor'` ok (142 s).
- Contract `TestLeiKwanEndInContract` + `TestLeiKwanRuleLatencyContract` sqlite + postgres ok, `TestLineMonitorContract` ok.
- `go-backend` `go test ./...` SQLite: all ok.
- `go-backend` `go test ./...` PostgreSQL: all ok except handler / repo / contract hitting Go's 10-min default timeout
  (packages run in parallel on one small PG container, same as r1); re-run `-p 1 -timeout 40m` → all three ok
  (handler 1042 s, repo 2224 s, contract 558 s). 13 leftover test schemas from killed runs dropped from the test PG.
- `go-gost/x` `go test ./socket/` ok. No frontend change in the finishing pass (no npm rerun).
- Logs: `/root/flvx-workers/runs/leikwan-latency-landing-r2/work/t*.log`.

## Released
- [x] Released in `3.0.27-fork.174` (plan 312, 11-26 18:05 TW panel; OTA 百度云广州 + 阿里广州 18:10–18:22 TW;
  chart 「利群专线端内」 created 18:27 TW). Rollback `/opt/flvx-svc/rollback/pre-fork174-20261126T100549Z/`.
