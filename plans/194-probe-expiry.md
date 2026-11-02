# 194 — 探针「剩余天数」小标签

Branch `feature/3.0.27-probe-expiry` from tag `3.0.27-fork.108` (prod). BUILD ONLY + screens; the coordinator batches
it into a later release. Brief: user 2026-11-02 (coordinator 3d7c53c7) 「机器月付、年付还剩多少时间……搞一个小标签」.

## Design (as approved, nothing more)
- Each machine on `/tz` gets a small tag: 「月付 · 剩 12 天」/「年付 · 剩 104 天」, no cycle →「剩 12 天」,
  1 →「明天到期」, 0 →「今天到期」, negative (no-cycle nodes only) →「已过期 3 天」. Narrow cards show the short form
  without the cycle.
- **Rolling (round 2, user 11-02):** `expiry_time` is an anchor. Exactly the rule of the 节点 page
  (`pages/node/renewal.ts` `getNodeRenewalSnapshot`): while due < now (ms, strict) add one cycle (month / quarter /
  halfyear / year, month steps chained in UTC+8 like JS `setMonth`), so the countdown restarts by itself; with a cycle
  there is no 「已过期」. Implemented once in Go: `go-backend/internal/renewal` (`NormalizeCycle`, `NextDue`,
  `DaysUntil`, `Compute`) for reuse by plans 195 (救命版 anchors) and 196 (reminders); `internal/probe` only wraps it.
- Days = calendar days in UTC+8, computed on the panel (prod anchors are 00:00 TW = 16:00 UTC: due 00:00 TW tomorrow
  → 「明天到期」 at 23:30 TW, never 「今天到期」). For 00:00 TW anchors this equals the TS `diffDays` (ceil of hours);
  for non-midnight anchors the probe counts calendar days by design (fixture `tsDays` documents the one difference).
- Go ↔ TS agreement: shared fixtures `go-backend/internal/renewal/testdata/rolling.json` (20 cases). Go test
  `TestComputeFixtures`; TS replay (no frontend test infra in the repo, so outside it):
  `TZ=Asia/Taipei node runs/probe-expiry/tscheck/check.mts <fixtures>` on a verbatim copy of `renewal.ts` →
  15 rolled cases equal (next due + days), 5 unset cases unset; same under `TZ=Asia/Shanghai`.
- Colour = 节点 page thresholds: neutral (slate) normally, amber ≤ 7 days, red today / expired.
- Card: under the online/offline pill (right column, region-line height). The 月付 / 年付 prefix shows when the card is
  ≥ 17rem wide inside (`@container`; desktop 4 columns at 1440 = 297 px, phones ≥ 360 px), else 「剩 12 天」; long
  names truncate with … as before. 紧凑表格: new column 「到期」 after 状态.
  Detail dialog: 「到期」 row. Sort: new 「到期」 option (soonest first, no expiry last). All of it only appears when
  the snapshot carries days (switch off → page exactly as before).
- Setting 「显示到期天数」 (`probe_expiry`, default ON) in 设置 → 公开探针页; off → fields absent from the snapshot.

## Public snapshot
- Per node `days` (integer, may be ≤ 0) and `cycle` ∈ {month, quarter, halfyear, year} (unknown → omitted). No raw
  timestamp. No expiry or switch off → both omitted. Hidden nodes stay absent. Top-level keys unchanged.
- Data source: `ListProbeNodes` (existing 15 s node cache) now also selects `expiry_time`, `renewal_cycle`; no new
  query, goroutine or timer per tick.

## Checklist
- [x] Backend: `ListProbeNodes` + `ProbeNodeRow` expiry/cycle; `probe.NodeInput` + `ExpiryDays` (UTC+8 calendar days,
      cycle roll-forward); `Node.days` / `Node.cycle`
- [x] Backend: `probe_expiry` setting (load default ON, admin GET/save, generic config API validation, cache reset)
- [x] Unit tests: day calculation edges (today, tomorrow, expired, null, cycles, month-end, midnight UTC+8), whitelist
      keys incl. new ones, switch off → absent
- [x] Contract tests SQLite + PostgreSQL: exact key sets incl. days/cycle, no ms timestamp in bytes, switch off →
      absent, hidden node absent, validation
- [x] Round 2: shared rolling helper `internal/renewal` (exact TS rule, chained month steps, strict `<`, ms) + probe
      wrapper; fixtures `testdata/rolling.json`; Go fixture test + TS replay agree (15/15 rolled, 5/5 unset)
- [x] Frontend: card tag (long / short), table column, detail row, sort 「到期」, settings switch; 1 →「明天到期」
- [x] go build / vet (touched packages clean; `internal/leikwan/testapi` unkeyed-field vet notes are pre-existing) /
      `go test ./...` with `FLVX_POSTGRES_TEST_DSN` (13 m 48 s, 0 failures; `TestProbeExpiryContract/{sqlite,postgres}`
      both ran); tsc 0 errors, vite build OK, eslint + prettier clean on all changed files (base: clean too)
- [x] Resource numbers (demo, same frame, 24 published / 21 with expiry): raw 7697 → 8161 B (+464, +6.0 %), gzip
      2310 → 2432 B (+122, +5.3 %); switch off = 7697 / 2310 (identical to before). Build of a 25-node frame
      (benchmark, GOMAXPROCS=2): ~40–45 µs off vs ~42–54 µs on, +30 allocs / +240 B. No new query, goroutine or timer:
      expiry rides on the existing 15 s node cache (`ListProbeNodes`), one shared build per tick.
- [x] Demo + screens gallery `/root/mmwx-preview/probe-expiry/` (:8791/probe-expiry/): 26 screens (cards, phone zoom,
      sort, table, detail, settings, switch off; 1440 + 390, light + dark), 49/49 checks, 0 page errors; anchors at
      00:00 TW like prod incl. 3 anchors one or more cycles back that rolled; switch off → card geometry identical to
      the fork.108 frontend (24 cards × 4 combos)
- [x] Push, CI green: run 41478596826 on 906e881b (Go backend, frontend, Go PostgreSQL contract, agent)
- [x] Summary `runs/probe-expiry/summary.md` (open: temp PG volume removal was permission-blocked in the worker
      session; red-at-≤3-days and 「今天到期」 on the due day itself left for the user to decide)
