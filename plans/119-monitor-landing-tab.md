# 119 — 监控: entry tab by content + shared landing age hint (fork.63)

User report 2026-10-20 (test user `leekao`, granted shared landing 「Boil Hinet 马年」, no own landing nodes, no nodes):
1. 监控 →「落地」: the shared server showed no 实时连接 / no 实时速率 and, after a while, a ticking 「数据 N 秒前」.
   Cause: rates are `null` (「-」) when the viewer has no node on the server, and shared data is collected by
   Custom once a minute while the age hint fires after 30 s, so a normal 30–90 s old sample showed the warning.
2. Entering 监控 opened the (empty) 「节点」 tab (or the last tab from localStorage) — felt unfinished.
   Wanted: any node → 节点; only landings → 落地; both → 节点.

## Tasks
- [x] Backend: per server / node `ageHintMs` (native 30 s, shared 150 s = 2.5 Custom collection cycles)
- [x] Backend: server without viewer nodes reports 0 / 0 rates instead of null
- [x] Backend test `TestLandingMonitorViewAgeHintAndEmptyServerRates`
- [x] Frontend: `AgeHint` uses the item's `ageHintMs` (falls back to the response-level value)
- [x] Frontend: 监控 entry tab picked from content (no more remembered tab); undecided = no tab shown, a tab clicked meanwhile wins
- [x] Gates: tsc, eslint (changed files), go vet + handler tests
- [x] Release fork.63 (CI 39486857813 + Images 39486858080 green, Latest, 10 assets), prod panel upgrade with rollback dir, verify as `leekao`-like view

## Result (2026-10-20 ~20:10Z)
- Prod panel `3.0.27-fork.63`, healthy, nodes online 29/29. No DB change, no agent OTA.
- Rollback: `/opt/flvx-svc/rollback/pre-fork63-*` (`./rollback.sh`), kept with pre-fork62.
- As `leekao` (token signed locally, read-only calls): `/monitor/nodes` = 0 nodes → page opens 「落地」;
  「Boil Hinet 马年」 shared: conns 0, up/down 0 (was null), sample age 36 s < hint 150 s → no 「数据 N 秒前」.
