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
- Sample row: `id` = `line_id << 32 | ts` (INTEGER primary key = the SQLite rowid, so rows are stored in
  (line_id, ts) order with no second index), `line_id`, `ts` (unix s), `status` (1 ok / 2 loss / 3 down), `ms10`
  (latency × 10). Every read / prune / delete is a primary-key range. Measured: 40 lines × 30 days 76 MB vs 123 MB for
  a composite key + its index (live interleaved inserts 69 vs 98.5 KB per line per day).
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
- [x] Backend: HTTP API (list / options / ports / chart save / delete / reorder / line pause / data) + contract tests
      (CRUD, admin-only, caps 8 / 40, port 22 hint, bucket keeps down, stats on fixtures)
- [x] `go test ./...` (SQLite) + PG variant (local temp postgres)
- [x] Frontend: API module, `/node-monitor` lazy page, 节点 toolbar button, empty state, chart dialog, chart with
      status lanes, range picker + custom + drag-zoom, stats table, 断流记录; H5 + dark
- [x] `npm run build`, eslint 0 new errors
- [x] Local lab: real agents in a loopback netns, real 断流 / 丢包 / 无数据; 40-line × 30-day synthetic query time + DB
      size; panel RSS / goroutines / CPU with 40 active lines
- [x] Gallery http://<dev>:8791/line-monitor/ (desktop 1366×900 + mobile 390×844, light + dark)
- [x] First prod setup proposal (below) — NOT executed
- [x] Ledger row 255 (待发布, waits for screen approval); push branch, CI green (run 43590459999: backend, agent,
      frontend, PG contract all success)
- [x] Clean temp files; summary (/root/flvx-workers/runs/line-monitor/summary.md)

## Results
(times 台湾时间 = UTC+8)

### Code (branch `feature/3.0.27-line-monitor`)
- 72b14332 backend (tables, `internal/linemon` engine + range maths, handler, repo), 247520b1 frontend + tests,
  7f80ebe2 packed (line_id, ts) primary key, b47831d8 chart polish, 4d5cd5fb cache bound / re-render / dialog fixes.
- New: `go-backend/internal/linemon/{engine,stats}.go`, `internal/http/handler/line_monitor.go`,
  `internal/store/{model/line_monitor.go,repo/repository_line_monitor.go}`, `vite-frontend/src/api/line-monitor.ts`,
  `src/pages/node-monitor.tsx` + `src/pages/node-monitor/{chart-card,chart-dialog,format}.tsx|ts`.
  Changed (add-only): `handler.go` (routes + 2 fields), `jobs.go` (1 goroutine), `repository.go` (3 models in
  auto-migrate, read-only connection), `App.tsx` / `page-loaders.ts` (route), `pages/node.tsx` (one toolbar button),
  `types/recharts.d.ts` (ReferenceArea / ReferenceDot declarations).

### Tests
- `internal/linemon`: classification table (ok / loss / down / gap incl. no-answer + offline), retry delay, spread
  over the slot, ≤ 4 in flight with slow probes, idle engine without lines (0 wake-ups), kick → pause, hourly
  maintenance; buckets keep `down`, step choice (≤ 1500 points), stats on a fixed 20-slot fixture (availability
  11/17, 无数据 15 %, avg, p95 nearest rank, jitter = mean |Δ| of adjacent slots, 3 events, longest 90 s, gap split,
  ongoing), JSON null for NaN. Stable over `-count=3`.
- handler (SQLite + PostgreSQL 18): admin-only (user 403, anonymous 401) on all 8 endpoints, CRUD + colours kept +
  removed line's samples deleted, caps 8 / 40 (refused save rolls back), validation, port 22 = warning only, port
  hints (tunnel / rule, never 22), scheduler end-to-end with a fake TcpPing (ok / loss ms / down / command timeout →
  no rows / offline source → no command, no rows / paused line → no command; ≤ 4 batched writes), prune 30 d in
  batches + orphans + duplicate slot ignored, 30-day query (step 1800, 断流 kept, events, cache), 1 h, zoom, bad
  windows.
- contract (real router + JWT middleware): anonymous / user refused on every route, chart CRUD, reorder, port 22
  hint, data for 1h/6h/12h/1d/30d keeps the 2-slot 断流, delete removes samples.
- `go test ./...` SQLite: all 28 packages ok. PostgreSQL 18 (local throwaway container): handler, contract and
  repository packages run in full with the DSN — all ok (incl. CI's PG contract test). A first all-packages PG run in
  parallel failed only because the 256 MB / then 768 MB RAM-disk test container was OOM-killed / ran out of space
  ("in recovery mode", "No space left on device"); re-run per package on a fresh container = all green.
- Frontend: `npm run build` ok (page chunk 41.9 kB / 14.2 kB gzip), eslint 0 errors in new files, 0 new errors
  in changed files (the 90 remaining errors are pre-existing on other lines/files), `tsc` clean.

### Lab (loopback-only netns, panel + 3 REAL agents of this branch in sandboxed units, 11-14 18:48–19:06)
- 断流 by refused port (100 s) → 3 down slots; black hole (75 s) → 2–3 down slots on both lines.
- 「Drop the first SYN」 does NOT give 丢包: the kernel resends the SYN after 1 s and the 3 s connect succeeds at
  ≈ 1005 ms (recorded as a latency spike — correct). Real 丢包 (target blocked for the whole first attempt, retry
  2 s later open) → every slot `loss` with the retry's latency.
- Agent frozen (SIGSTOP, command timeouts) 90 s and agent stopped (offline) 90 s → no rows (无数据), never 断流;
  offline = no command sent, reason 源节点离线.

### Resources (lab panel, GOMAXPROCS=2)
- 0 lines: RSS 37 MiB, CPU 0.43 % of a core, 42 goroutines (line monitor = 1, blocked).
- 40 active lines (cap): RSS 31 avg / 38 max MiB, CPU 0.59 %, 44 goroutines, 488 TcpPing in 6 min = 40 × 12, max in
  flight 1 (≤ 4 cap), 0 skipped, one batched insert per 30 s. Agents 32–36 MiB.
- Storage: 40 lines × 30 days synthetic = 3.45 M rows = 76 MB (composite key + index would be 123 MB); live
  interleaved inserts ≈ 69 KB per line per day.
- 30-day read of an 8-line chart: 0.41–0.52 s cold, 3–5 ms cached (60 s); 1 h 5 ms, 1 d 21 ms. During 8 back-to-back
  cold 30-day reads the main DB path (node list) stayed at p50 3.1 ms / max 8.6 ms (baseline 3.0 / 10.1) — the reads
  use the separate read-only SQLite connection.

### Gallery
http://104.145.236.29:8791/line-monitor/ — 59 screens (节点 page button, empty state, dialog incl. port 22 hint,
1小时 / 1天 / 30天 / 自定义 / drag zoom / 断流记录 zoom / tooltip / stats / second chart with custom HiNet target and
无数据), desktop 1366×900 + mobile 390×844, light + dark, 40/40 automatic checks (no page horizontal scroll on 390,
no lab addresses or secrets in the DOM, range remembered), 0 page errors. Demo = synthetic DB + fake agents with
documentation-range addresses only.

## First prod setup after the release (proposal — NOT done)
Read-only check of the prod DB (11-14) + one read-only `ss -ltn` on Tcscloud Seednet:
- **AWS HK** (source) is online and on a current agent — any agent can run TcpPing, no OTA needed.
- **Fachost seednet 黑五** listens on two FLVX tunnel-exit relay ports: **51877** (tunnel 「vmsilo沪日首发 -> TW(Akari日台互联…)」)
  and 22121 (tunnel 「mk沪日ixp440 -> TW(…)」). Pick **51877**: a gost relay listener that the panel's 60 s path rounds
  already TcpPing, so one more connect per 30 s changes nothing for the box (relay auth happens after the TCP
  handshake, so the connect itself always completes while the line is up).
- **Tcscloud Seednet** has no panel-known listener: native landing server 「Tcscloud Seednet」 exists but has 0 landing
  nodes, no tunnel/rule uses it, and the box only listens on SSH 22 publicly. **Do not use 22**: connect-and-close
  every 30 s is logged by sshd as pre-auth disconnects and fail2ban would ban AWS HK's IP (our fleet bans permanently).
  So the first step is to give it the same kind of listener as 黑五: create one 隧道转发 tunnel 「AWS HK → Tcscloud（监测用）」
  (entry AWS HK, exit Tcscloud Seednet, port auto from its range 10000–29999), no rules needed. Its exit relay port is
  then the target port — same listener type on both Seednets = a fair comparison. (Alternative: the first real
  native landing node on Tcscloud Seednet, port in 30000–39999.)
- Then 线路监测 → 新建监测图 「AWS HK → Seednet」:
  1. 源 AWS HK → FLVX 节点 Fachost seednet 黑五 (address empty = follow the node), port 51877, 备注 「黑五」;
  2. 源 AWS HK → FLVX 节点 Tcscloud Seednet (address empty = follow the node: its server address is the DDNS name, so
     the dynamic home IP is followed automatically), port = the new tunnel's exit port, 备注 「Tcscloud」.
- Later 「Seednet → HiNet」: 源 Fachost seednet 黑五 and 源 Tcscloud Seednet → 自定义地址 = the HiNet box's IP and a port
  that is listening there and is not 22 (the user has to provide it).

---

# Part 2 检测端口 — auto-open a TCP check port on the target (panel + agent)

Build only (no tag / release / prod change), same branch. Needs panel + a **targeted agent OTA** of the nodes used
as monitor targets (Fachost seednet 黑五, Tcscloud Seednet) and sources (AWS HK, so it reports the `refused` kind).

## Why (user, 11-14 TW)
A TCP check needs a port that listens on the target; a port with nothing on it is refused → every slot would read as
断流. Creating a node / rule just to get a listening port is 「小题大做」. The 线路监测 dialog itself opens a port on
the target (or picks one) — and nothing may stay open forever once nobody measures any more.

## Design (agreed in the brief; deviations are marked ✎ with the reason)

### A. Target = FLVX node → the panel opens the port through its agent
- Agent package `go-gost/x/monitorport`: listener = **accept → close inline in the accept loop** (normal FIN, ✎ not
  RST: on a sub-ms path an RST can reach the client before its connect() reports success → a false 「refused」; the
  panel's TcpPing closes with RST itself, so a measurement leaves no TIME_WAIT on either side); never reads, never writes, no TLS / protocol / proxy path. One goroutine per open port, no goroutine per
  connection (✎ the brief allowed one per conn; closing inline is cheaper and makes "≤ 16 concurrent" hold by
  construction: at most 1 accepted conn per port exists at any time). Accept pace ≤ 50/s per port (token bucket; the
  loop sleeps, the kernel backlog absorbs — the client's connect time is the handshake, so pacing never changes a
  measured latency). Per source ≤ 20 connections / 10 s (bounded map of 256 sources): over-limit sources are closed
  the same way but do not count as activity. No log line per connection; errors ≤ 1 line / 10 min / port.
- Agent refuses by itself (does not trust the panel): port < 1024, 22, > 65535, more than **8** monitor ports, a port of
  one of its own gost services (incl. paused ones, from the config), nft-forwarded rule ports, native landing public
  ports, and anything the kernel says is in use (`EADDRINUSE` → `busy`). Open is idempotent.
- Command `MonitorPort` actions `open` / `close` / `list` / `sync`; the panel only uses `sync` = full desired list of
  that node (opens what is missing, closes every monitor port not listed) → one idempotent message per change and per
  reconnect.
- Persisted set `monitor_ports.json` in the agent's config dir (next to `config.json`): restored at agent start, before
  the panel connects. Idle safety net: a port that saw no connection for **24 h** closes itself and leaves the set (the
  listener's accept deadline — no extra timer); the restore after an agent restart starts a fresh 24 h (✎ the last
  connection time is not written per connection; the panel's reconnect `sync` closes stale ports at once anyway).
- A real service always wins: before the agent creates / updates / resumes a gost service or applies native landing
  ports, a monitor port on the same port is closed first (defence in depth; the panel reservation is the main guard).
- Capability: handshake query `monitorPort=1` (same pattern as plan 244 `warmPool`). An online target without it →
  auto mode disabled with 「需升级节点 agent（OTA）后才可自动开启检测端口」.
- **Auto pick**: random free port from the node's **rule range** (`node.port`; landing ranges are never used),
  excluding rule / tunnel / landing (incl. guard + shared) / chain-node / deployed inbound / runtime resource /
  federation binding / nftables rule ports, other monitor ports, 22, < 1024 and ports the agent reported busy (kept in
  memory 1 h). A second auto line to the same target node reuses that node's port (ref-counted).
- **Reservation**: table `line_monitor_port (node_id, port, state open|closing)`; every row reserves its port in all
  port allocators / conflict checks (rule auto pick + manual check, tunnel ports, federation, landing / chain ports).
- **Verify**: after a push the panel asks each affected line's **source** agent for one TcpPing (3 s) →
  line `check_state` ok / unreachable / refused / unverified + message. Chips: 「检测端口 :NNNNN 已开启 ✓」 /
  「已开启，但从 <source> 连不上（可能商家防火墙 / NAT 未放行该端口）」 + 「换一个端口」 (tries ≤ 3 new ports, stops at
  the first that verifies, then says so). No loops beyond that.
- Dialog port modes for node targets (add-only): 「自动开启检测端口（推荐）」 / 「使用已在监听的端口」 (existing port chips +
  hint that landing ports may count repeated bare connects as scanning) / 「手动填写」. Existing / manual = today's
  behaviour, the panel opens nothing.

### B. Target = custom host:port
- 「自动选端口」: random 20000–29999 avoiding well-known ports and ports of other lines to the same host.
- 「复制开启命令」: one paste for Debian / Ubuntu (root), generated by the panel from integers / enums only (port,
  window seconds, validated source IPs re-printed by `netip`); no download, no user text (label / host never appear).
  Undo command shown next to it. 「测试连通」 = one TcpPing from every source of the chart to host:port → ✓ / ✗ per
  source (with the refused / timeout kind).

### C. Auto-expiry
- One-liner = `flvx-linemon-<port>.socket` (Accept=yes, MaxConnections=16, MaxConnectionsPerSource=4) +
  `flvx-linemon-<port>@.service` (/bin/true, DynamicUser, ProtectSystem=strict, NoNewPrivileges, RuntimeMaxSec=5,
  MemoryMax=8M, LogLevelMax=warning, …) + `flvx-linemon-<port>-watch.timer/.service` (every 10 min) running
  `/var/lib/flvx-linemon/<port>/watch.sh`: compares the socket's `NAccepted` with the last value in its state file; no
  new connection for the idle window → disable + stop + delete its 4 units and its own state dir (parent dir only if
  empty), one journal line 「flvx-linemon <port>: idle N h, removed」. Window 1 小时 / 6 小时 / **24 小时（默认）** / 3 天
  (stored on the line); test-only env override `FLVX_LINEMON_TEST_IDLE` / `FLVX_LINEMON_TEST_TICK` (integers, checked
  on the box).
- Agent `TcpPing` answers get `errorKind` refused / timeout / unreach / dns (new optional field). Panel: both attempts
  refused → new sample status 4 「端口未开启」 (grey-blue, excluded from 可用率 and 断流 like 无数据). Old source agents
  (no field) → 断流 as before. Line row: ⚠ 「检测端口已关闭（可能已自动过期）」 + 「复制开启命令」 (custom) /
  「重新开启」 (node auto; the panel also re-sends the node's list by itself, ≤ 1 / 10 min per node).
- Pause confirm for check-port lines: 「暂停超过 24 小时，对方检测端口会自动删除」 (custom: the line's window; node auto:
  the agent's 24 h, re-opened on resume).

### D. Lifecycle on our nodes (one transaction each: line change + reservation / close intent)
| Path | Effect |
|------|--------|
| delete line (chart save without it) / delete chart | lines gone; ports no line uses any more → `closing`; push `sync` |
| change target node / port / mode of a line | old port → `closing` unless another auto line uses it; new port reserved |
| pause | port kept; agent 24 h net applies; resume → push `sync` (re-open) |
| source node deleted | its lines go with it (✎ part 1 left them dangling); their target ports → `closing` |
| target node deleted | lines to it deleted (✎ a line without its target can only read 断流), reservation rows dropped, nothing to close |
| same node + port used by another line | kept (ref-count = auto lines on that node + port) |
| target offline at delete | row stays `closing` (still reserved) → closed by the full `sync` on its next reconnect, then the row goes |
| agent restart with a stale persisted port | reconnect `sync` lists only the wanted ports → the agent closes the stale one |
Rows are deleted only after the agent acknowledged a `sync` without them.

### E. Security — threat table
| # | Threat | Defence | Test |
|---|--------|---------|------|
| T1 | Port used as a data / proxy path | listener only accepts and closes; no read, no write, no forward code path | `TestListenerNeverWritesOrReads` (client reads EOF/RST, 0 bytes; sent bytes never read) |
| T2 | Panel (or a stolen panel channel) opens ports it should not | agent refuses <1024, 22, own service / nft / landing ports, busy ports, > 8 ports | `TestOpenRefusesReservedPorts`, `TestCaps` |
| T3 | Flood / resource exhaustion on the open port | inline close, ≤ 50 accepts/s per port, per-source cap, bounded source map, no per-conn log | `TestAcceptPaceAndSourceLimit`, fd / goroutine measurement in the lab |
| T4 | Forgotten port stays open forever | agent 24 h idle net + reconcile on reconnect; custom one-liner self-removes after the window | agent idle test, Debian 12 / 13 container lab |
| T5 | Non-admin / anonymous access, data leak | new routes under `/api/v1/node/line-monitor/` (admin middleware) + handler admin check; nothing in probe / sub / share / federation payloads; no target / port in logs | contract test (user 403 / anonymous 401 on every new route) |
| T6 | Command injection through the generated one-liner | only integers / enums / `netip`-reprinted IPs are interpolated; label and host never; inline content, nothing downloaded | `TestCheckPortScriptOnlyIntegers` (fuzzed labels / hosts never appear, shellcheck-style lint of the output) |
| T7 | Watchdog deletes foreign files | fixed names `flvx-linemon-<port>.*` + `/var/lib/flvx-linemon/<port>/` only, `rmdir` (not `rm -r`) on the parent | container lab: other files in the dirs survive |
| T8 | Port reserved for monitoring taken by a rule / landing (or the other way round) | panel reservation in every allocator; agent closes a monitor port before a real service binds | `TestCheckPortReservationBlocksRules`, `TestYieldToService` |
| T9 | Scanners see the custom port | optional 「仅允许来源节点连接」 (`IPAddressAllow` + `IPAddressDeny=any`) → filtered; default on only when every source has a stable literal IP (DDNS / hostname → off + hint) | unit test of the default + container lab |
Agent-side source allow-list: **skipped** — our nodes' egress IPs are not always the panel-known address (NAT / EIP,
沪台 two egress IPv4s, IPv6 privacy), so an allow-list would produce false 断流, and the only firewall-free way (a socket
BPF filter on the listener) adds kernel-level code to a port that exposes nothing.

## Part 2 tasks
- [x] Plan section (this)
- [x] Agent: `monitorport` package (listener, caps, pace, per-source, idle net, persistence, restore) + `MonitorPort`
      command + handshake `monitorPort=1` + yield before services / landing + TcpPing `errorKind`; unit tests
- [x] Backend: model columns + `line_monitor_port`, repo (pick / reserve / ref-count / closing, node delete paths),
      reservation in every allocator, `sync` push + reconnect + verify + rotate (≤ 3) + heal, sample status 4
      (stats / buckets), custom one-liner generator + suggest + 测试连通, routes; tests SQLite + PostgreSQL
- [x] Frontend: port modes, auto chip, OTA-disabled state, custom command panel (window, allow, copy, undo, 测试连通),
      line-row chips / ⚠ / 换一个端口 / 重新开启, 「端口未开启」 lane + legend + stats, pause confirm; build + eslint
- [x] Lab with real agents (open, verify from another node, nft block → 连不上 + 换一个端口, restart → back, delete →
      closed, offline-at-delete → closed after reconnect, stale persisted → closed); fd / goroutine / RSS numbers
- [x] Debian 12 + Debian 13 systemd containers: one-liner alive with connections, idle → self-removed, panel shows
      「端口未开启」, re-run → back; undo command
- [x] Gallery http://<dev>:8791/line-monitor/ extended (port modes, ✓ / ✗ chips, old-agent state, custom command +
      测试连通) desktop + mobile, light + dark
- [ ] Push, CI green, ledger row 255, prod setup proposal (below), summary

## Part 2 results
(times 台湾时间 = UTC+8; build only — no tag, no release, no prod change)

### Code (branch `feature/3.0.27-line-monitor`)
- 588f58ff agent: `go-gost/x/monitorport/{monitorport,command}.go` (+ tests), `socket/monitor_port.go` (MonitorPort command,
  in-use check, yield hooks, restore), `socket/tcp_ping_kind.go` (errorKind), handshake `monitorPort=1`, MonitorPort is a quiet
  routine command (the manager logs only opens / closes / expiries / restores).
- a3ccdafc panel: `line_monitor_port` table + 6 line columns (auto-migrate), `repository_line_monitor_port.go` (pick, reserve,
  ref-count, 待关闭, busy move, node delete), reservation in every allocator (rule pick + manual check, tunnel ports,
  federation, landing / chain / guard / protocol ports, landing holder labels 「线路监测检测端口」), `line_monitor_port.go`
  (sync push, verify, rotate, heal, reconnect, command, 测试连通), sample status 4, `linemon/checkport.go` (one-liner).
- cdecfff0 UI: `pages/node-monitor/check-port.tsx` (chips, 开启命令 panel, 测试连通, command modal), port modes in the dialog,
  端口未开启 lane / tooltip / legend / stats pill, pause confirm. 3c2c543e / 7fa8b36b small fixes (avoid failed ports 1 h, real
  window in the command's last line, chip falls back to the last stored slot after a panel restart).

### Deviations (✎) and known limits
- Accept → close with a normal FIN (not RST) — see design A.
- Lines whose **target** node is deleted are deleted too (a line without its target could only read 断流).
- 「手动填写」 / 「使用已在监听的端口」 = today's behaviour; only 「自动开启检测端口」 opens something.
- A firewall that *rejects* (TCP RST / ICMP port-unreachable) reads as 端口未开启, not 断流 — both mean "reachable, refused".
- An agent restart takes ~5 s before it re-opens its ports (it waits for its own services first): a slot in that window
  reads 端口未开启 (seen once in the lab), never 断流.
- A check port under a sustained flood > 50 conn/s keeps the agent at ≤ ~1–3 % CPU, but the kernel backlog can fill and
  probes on *that* port may then time out (断流) until the flood stops.
- The command modal's window / allow switch only change the generated command (the line keeps its saved values).

### Tests
- Agent: `monitorport` 10 tests (`-race`): open / close / list idempotent, <1024 / 22 / >65535 / own service / busy (EADDRINUSE)
  refused, cap 8, never reads or writes (client gets EOF, 0 bytes), sync closes unlisted, persist + restore after restart (a
  port taken meanwhile is dropped), restore skipped once the panel synced (stale port never reopens), 24 h idle net (short
  window: busy port stays, idle one closes and leaves the file), per-source cap 20 / 10 s + pace 50/s, yield. `socket`:
  errorKind classification (refused / timeout / unreach / dns), real TcpPing to a refused port → `refused`, 20 pings to a
  check port all succeed, configured (paused) service ports count as in use, yield to services and landing.
  `go test ./...` in go-gost/x: all ok (final run listed below); go-gost root builds (not changed).
- Panel (`line_monitor_port_test.go`, SQLite + PostgreSQL 18): admin-only on the 4 new routes; auto pick skips every used
  port (rules, tunnels, nft, privileged) and the reservation blocks rule auto pick, `HasOtherForwardOnNodePort`, used-port
  maps, federation ports and manual rule ports (message names 线路监测); open + verify from the source, two lines share a
  node's port, vendor-blocked → unreachable, 换一个端口 → ok, all blocked → exactly 3 tries + message, tried ports closed;
  busy port moves the line; old agent → refused with the OTA hint + options flag; offline target → 待开启, opened on reconnect;
  lifecycle: delete chart with another chart on the same port (kept), pause (kept, no command), resume (re-open), target
  change (old closed, new opened), auto → manual (closed), offline at delete (待关闭 stays reserved → closed after reconnect),
  stale agent port → closed by reconnect sync; node delete as source (lines go, target port closes) and as target
  (reservation dropped, nothing sent); scheduler: refused twice → status 4 (not 断流), heal re-sends once per 10 min;
  old agents (no errorKind) → 断流; one-liner command (suggest, allow default on static IPs / off with the DDNS host-name
  source named, window, refusals) and 测试连通 (✓ / refused / offline). `linemon`: classify incl. refused, closed slots
  (not 断流, not in 可用率, not 无数据, split 断流 runs), script only interpolates integers / netip IPs (diff test), no
  download / `rm -r`, suggestion range. Contract test: the 4 new routes refused for user / anonymous through the router.
- Frontend: `npm run build` ok (page chunk 58.4 kB / 18.9 kB gzip), `tsc` clean, eslint 0 errors / 0 warnings in the
  changed files.

### Lab with real agents (netns lm255p2, loopback only, this branch's paneld + 3 agents, 11-14 20:40–20:47)
- Save chart with 2 auto lines → 黑五 :21017 / Tcscloud :22015 opened (agents log one line each), verify ok 0.1–0.2 ms,
  `monitor_ports.json` written.
- nft drop 21017 (vendor firewall) → verify `unreachable`; 换一个端口 → :21093 ok in 1 try, 21017 closed.
- nft drop 21000–21099 → 换一个端口 tries 3 ports in 9.0 s, all unreachable, stops with the message; only the last stays.
- Agent restart → port back 5 s after start (panel sync). Panel stopped + agent restart → the agent restores the port
  from its own file after 10 s; panel back → verify ok.
- Tcscloud agent stopped, its line deleted → row 待关闭 (still reserved); stale 22098 written into its file; agent started →
  sync closes / never opens both, file `[]`, row deleted.
- Custom target 127.0.0.9:25999 (nothing listening) → samples status 4 only (5 slots), 断流 0, 可用率 none.
- Delete chart → no listener left, files `[]`, rows 0; delete source node AWS HK → target port closed, rows 0.
- Panel log: 0 `[line-monitor]` error lines.

### Resources (lab)
- Agent with 1 check port idle: 17 CPU ticks / 60 s vs 18 for an agent without (no measurable cost); RSS 54 MB, 13 threads,
  11 fds.
- Flood 3000 connects in 0.15 s on the check port: RSS +2.5 MB, threads / fds unchanged, accepts paced to 50/s (the backlog
  drained in ~60 s), a probe sent after it waited in the backlog (pace working), verify ok right after. 500 paced connects
  (TcpPing-like RST close) cost ~0.1 s CPU (~0.2 ms each) → ≤ ~1 % of a core at the 50/s cap. Normal use = 2 connections /
  min per line.
- Panel 41 MB RSS, 8 threads; no new loop (reconnect sync + on-demand only).

### Self-expiring one-liner (Docker systemd containers, Debian 12 = systemd 252, Debian 13 = systemd 257, 11-14 21:51–21:58)
Both identical: install ok; 12 connections in 180 s keep it (NAccepted 12, state updated), **0 journal lines** for 12
connections + 9 watchdog runs, 0 errors; no connection → self-removed 163 s later (window 120 s + tick 20 s), journal
「flvx-linemon 25931: idle 120 s, removed」, 4 units + its state dir gone, foreign `/var/lib/flvx-linemon/foreign.txt`
and `/etc/systemd/system/keep-me.service` kept; then refused; re-run → connected; undo → gone at once; allow variant
(`IPAddressAllow=172.17.0.1/32` effective) → host connects, the other container times out (filtered).

### End-to-end chain (branch panel + REAL agent in the Debian 13 container as source → Debian 12 container target)
Port open → ok 3 / 断流 0; line paused → the target's check port self-removed 71 s later; resumed → **3 slots 端口未开启,
断流 0, 可用率 100 %**, live status 4, 测试连通 → `refused`; re-run the command → ok again. Panel errors 0.

### Gallery
http://104.145.236.29:8791/line-monitor/ — part 2 on top (42 new screens: port modes + OTA-disabled state, existing ports
+ scan hint, custom 自动选端口 + 开启命令, 测试连通, ✓ / 连不上 / 待开启 chips, 换一个端口, 端口未开启 lane + stats + tooltip,
复制开启命令 + 测试连通 ✓/✗, pause confirm), desktop 1366×900 + mobile 390×844, light + dark, 64/64 automatic checks, 0 page
errors; part 1 screens kept below. Fake documentation addresses only.

### Security assessment (安全评估)
Someone who finds a check port can open a TCP connection that is closed at once without a byte read or written: no
protocol, no banner, no data, no proxy, no auth surface — the only thing learnt is "a port is open". Floods cost the agent
≤ ~1–3 % CPU (pace 50/s, per-source cap, inline close, no per-connection log). The panel cannot be tricked into opening a
privileged / SSH / in-use port (the agent refuses itself, ≤ 8 ports). The APIs are admin-only; nothing reaches probe /
sub / share / federation payloads. The one-liner only interpolates integers and re-printed IPs, downloads nothing, runs the
per-connection service sandboxed (DynamicUser, ProtectSystem=strict, NoNewPrivileges, no capabilities, 8 MB, 5 s) and the
watchdog only removes its own fixed-name files.

## Prod setup after the release (proposal — NOT done)
Release = panel (DB auto-migrate: new table `line_monitor_port`, 6 new columns on `line_monitor_line`; rollback = image swap,
the new table / columns stay inert) + **targeted OTA only**: AWS HK (source: errorKind), Fachost seednet 黑五 and Tcscloud
Seednet (targets: MonitorPort). Other agents stay.
1. 线路监测 → 新建监测图 「AWS HK → Seednet」: 源 AWS HK → FLVX 节点 Fachost seednet 黑五 (自动开启检测端口, 备注 黑五);
   源 AWS HK → FLVX 节点 Tcscloud Seednet (自动开启检测端口, 备注 Tcscloud; its address is the DDNS name, followed
   automatically). Check the chips: ✓ for both; if one reads 「连不上」 the vendor / home router does not forward that part of
   the node's rule range → 换一个端口 (≤ 3) or fix the forwarding.
2. 「Seednet → HiNet」: replace the hand-made socket on mmwx-hinet2 first (its port would otherwise be "in use"):
   `systemctl disable --now flvx-linemon.socket; rm -f /etc/systemd/system/flvx-linemon.socket /etc/systemd/system/flvx-linemon@.service; systemctl daemon-reload`,
   then the dialog: 2 lines 源 Fachost seednet 黑五 / 源 Tcscloud Seednet → 自定义地址 `boilhinet2.imgamer.top`, port 25931,
   window 24 小时, 仅允许来源节点连接 stays off (Tcscloud Seednet is a dynamic IP); 复制开启命令 → paste on mmwx-hinet2 as root
   → 测试连通 (both ✓) → 保存.
