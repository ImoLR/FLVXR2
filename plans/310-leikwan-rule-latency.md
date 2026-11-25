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

