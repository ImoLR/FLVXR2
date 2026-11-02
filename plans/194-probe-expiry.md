# 194 — 探针「剩余天数」小标签

Branch `feature/3.0.27-probe-expiry` from tag `3.0.27-fork.108` (prod). BUILD ONLY + screens; the coordinator batches
it into a later release. Brief: user 2026-11-02 (coordinator 3d7c53c7) 「机器月付、年付还剩多少时间……搞一个小标签」.

## Design (as approved, nothing more)
- Each machine on `/tz` gets a small tag: 「月付 · 剩 12 天」/「年付 · 剩 104 天」, no cycle →「剩 12 天」,
  0 →「今天到期」, negative →「已过期 3 天」. Narrow cards show the short form without the cycle.
- Days = calendar days in UTC+8, computed on the panel. Same rule as the admin 节点 page: a node with a renewal cycle
  rolls its due date forward by whole cycles until it is not in the past (the panel's renewal job does the same in
  the DB), so only machines without a cycle can show 「已过期」.
- Colour = 节点 page thresholds: neutral (slate) normally, amber ≤ 7 days, red today / expired.
- Card: under the online/offline pill (right column, region-line height). 紧凑表格: new column 「到期」 after 状态.
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
- [ ] Frontend: card tag (long / short), table column, detail row, sort 「到期」, settings switch
- [ ] go build / vet / test (SQLite + PostgreSQL); tsc / build / lint vs base
- [ ] Resource numbers: snapshot bytes before/after (25 nodes, raw + gzip), build time
- [ ] Demo + screens gallery `/root/mmwx-preview/probe-expiry/` (1440 + 390, light + dark)
- [ ] Push, CI green
- [ ] Summary `runs/probe-expiry/summary.md`
