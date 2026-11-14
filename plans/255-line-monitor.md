# 255 — 「线路监测」 standalone comparison chart (TCP latency + 断流, 30 s, 30 days)

Build only (no tag / release / prod change). Branch `feature/3.0.27-line-monitor`, base tag `3.0.27-fork.131`.
Panel only: no agent change, no OTA; DB auto-migrates three new tables.

## Why (user, 11-14 TW)
Compare the stability of two Seednet products (Fachost seednet 黑五 vs Tcscloud Seednet), e.g. AWS HK → each, as two
lines in ONE chart over a long time; later both Seednets → a HiNet box that is not an FLVX node (custom host:port).
No per-node probe charts — one admin tool where only the comparison charts the admin cares about are built.

## Agreed rules (from the brief, not reopened)
- Admin only. Entry = one 「线路监测」 button in the 节点 page toolbar → new lazy page `/node-monitor`. Nothing else on
  the 节点 page changes. Not on /tz, no anonymous route (targets reveal IPs). Non-admin → 403.
- 监测图: create / rename / delete / reorder; 1–8 lines each; 40 lines total. Line = source FLVX node → target
  (FLVX node with editable host, or custom host:port) + port + optional label; colour auto from the palette; pause /
  remove.
- Ports: never default to 22; node targets get port suggestions from what the panel knows listens there (tunnel relay /
  rule / landing ports); port 22 = hint only 「请勿使用 SSH 端口（22），频繁连接可能被对方 fail2ban 封禁」, not a block.
- Measurement: panel-driven existing `TcpPing` (count 1, timeout 3000 ms) every 30 s per active line; on failure retry
  once ~2 s later. ok / loss (first failed, retry OK, retry ms) / down (both failed) / gap (panel could not ask the
  agent — never counted as 断流 or loss). Lines spread over the 30 s window, ≤ 4 in flight, scheduler idle with no
  active lines, survives restarts (config in DB; restart = gap).
- Storage: tables chart / line / sample (line_id, unix s, status, ms), index (line_id, ts); raw 30 s samples 30 days;
  hourly prune in small batches; deleting a line deletes its samples. Repository methods only.
- Range: 1小时 / 6小时 / 12小时 / 1天 / 30天 + 自定义 (within 30 days) + drag-to-zoom + 重置; default 1小时; last choice per
  chart in localStorage.
- Chart: all lines overlaid, shared x-axis, tooltip lists every line; 断流 red, 丢包 yellow, 无数据 grey hatched; long
  ranges bucketed server-side (≤ ~1500 points per line, avg + max + ok/loss/down/expected), a bucket with any `down`
  always shows it; animation off; 30 s live refresh on short ranges, no spinner after first paint.
- Stats per line: 当前延迟, 平均, p95, 抖动, 可用率, 丢包次数, 断流次数, 最长断流, 无数据 %; 「断流记录」 newest first, click → zoom.
- UI add-only, FLVX palette, bridge components, empty state, H5 390 px without page horizontal scroll, light + dark.

## Design decisions (this plan)
- Package `internal/linemon` = pure logic (classification, scheduling slots, buckets, stats, event detection) with unit
  tests; handler file `line_monitor.go` = HTTP + scheduler wiring; repo file `repository_line_monitor.go`.
- Routes under `/api/v1/node/line-monitor/*` (already admin-only in the JWT middleware) + explicit admin check in every
  handler. Probe host gate keeps them off the 探针专用域名.
- Sample row: `line_id`, `ts` (unix s), `status` (1 ok / 2 loss / 3 down), `ms10` (latency × 10, integer → compact in
  SQLite); composite primary key (line_id, ts) = the (line_id, ts) index; partial index on non-ok rows for fast
  event / loss lookups.
- Classification of (attempt 1, attempt 2): ok → ok; fail+ok → loss; fail+fail → down; any attempt the panel could not
  get an answer for (source offline / command error / channel timeout) and no clear rule outcome → gap (no row).
  Offline source = no command sent at all.
- Target node with empty host follows that node's current address (re-read on config change and every 5 min).
- Jitter = mean |Δ| between consecutive measured latencies (adjacent slots only).
- Long-range reads on SQLite use a separate read-only connection (WAL readers never block the main single
  connection); results of long ranges cached briefly.

## Tasks
- [x] Plan doc
- [x] Backend: models + auto-migrate (SQLite + PG), repository methods (CRUD, batch insert, range reads, prune, delete)
- [x] Backend: `internal/linemon` logic (classify, slots, buckets, stats, events) + unit tests
- [x] Backend: scheduler (one goroutine, ≤ 4 in flight, retry 2 s, spread, idle with no lines, batched writes, hourly
      prune) + tests (classification incl. offline / timeout, retry, jitter + concurrency, pause, prune)
- [ ] Backend: HTTP API (list / options / ports / chart save / delete / reorder / line pause / data) + contract tests
      (CRUD, admin-only, caps 8 / 40, port 22 hint, bucket keeps down, stats on fixtures)
- [ ] `go test ./...` (SQLite) + PG variant (local temp postgres)
- [ ] Frontend: API module, `/node-monitor` lazy page, 节点 toolbar button, empty state, chart dialog, chart with
      status lanes, range picker + custom + drag-zoom, stats table, 断流记录; H5 + dark
- [ ] `npm run build`, eslint 0 new errors
- [ ] Local lab: real agents in a loopback netns, real 断流 / 丢包 / 无数据; 40-line × 30-day synthetic query time + DB
      size; panel RSS / goroutines / CPU with 40 active lines
- [ ] Gallery http://<dev>:8791/line-monitor/ (desktop 1366×900 + mobile 390×844, light + dark)
- [ ] First prod setup proposal (below) — NOT executed
- [ ] Ledger row 255 (待发布, waits for screen approval); push branch, CI green
- [ ] Clean temp files; summary

## Results
(filled in as tasks finish)
