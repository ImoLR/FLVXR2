# 187 — 公开「探针」页（轻量、实时、不卡）

Build only (no tag / release / OTA). Base = `maintenance/3.0.27-fork.105-sub-link` (3aea6ee7, = fork.105 + plan-doc commit).
Branch `feature/3.0.27-probe-page`, worktree `/root/projects/flvxx/flvxx-probe-page`. Runs together with plan 188 (监控页去卡顿).
Release later in a batch **together with plan 184's branch** (latency-tag-region, approved, separate branch).

User approval 2026-11-01 (coordinator 6e28c3e5): public, no login, whitelisted fields only; latency phase 1 = existing
60 s path-latency round (no agent change, no OTA); fix 监控 lag too; security first (Komari incidents).

## Design

### Route & switches
- The probe replaces `/tz` (same URL). Switch = existing `login_monitor_link`, relabelled 「公开探针页」 in 设置.
  Off → page shows 「探针未开启」, every probe endpoint answers HTTP 404, the probe WS is refused, live streams close.
- New config rows (additive, admin-only, not in the public config list): `probe_title`, `probe_domain`,
  `probe_latency` (default on), `probe_nodes` (JSON `{"hidden":[ids],"names":{"id":"name"}}`).
  Saved through a dedicated admin endpoint (`/api/v1/config/probe/{get,save}`) with validation.
- 探针专用域名: when the request Host (or X-Forwarded-Host) equals it, only `/api/v1/probe/*` answers; every other
  `/api/*`, `/system-info`, `/flow/*`, `/s/*` → 404. The SPA (served by nginx, which cannot know the domain) asks
  `/api/v1/probe/host` once (cached per hostname in localStorage) and redirects any non-probe route to `/tz`;
  no login link on that host. The admin cannot save the domain they are currently using (lock-out guard).

### Data path (panel only)
- `internal/probe`: public whitelist structs, snapshot hub (one build per 2 s while viewers exist, marshal once, gzip
  once, same bytes to every client and to the GET cache), latency rings, series downsampling helpers.
- Latest metrics: in-memory map fed by the existing node metric hook (1 s agent reports), not the DB.
- `GET /api/v1/probe/snapshot` (gzip if accepted, ETag), `GET /api/v1/probe/ws` (binary gzip frames with `?gzip=1`,
  else text), `GET /api/v1/probe/series?k=&range=1h|6h|24h&points=N` (SQL-bucketed node_metric, cached 30 s),
  `GET /api/v1/probe/host`.
- Opaque key `k` = first 10 base32 chars of HMAC-SHA256(JWT secret, "flvx-probe-node:"+id): stable, not reversible.
- Latency: after each 60 s path round, node→node edges (deduped per directed pair, min of the round's OK
  measurements) go into a ring of 60 rounds per pair (memory only); snapshot shows the newest per pair to shown
  nodes only; series returns the ring for the detail sparkline.
- Limits: WS 5/IP, 200 total (503 → frontend polls GET every 5 s), send queue 4 (slow client dropped), read limit
  512 B (client data ignored), ping 25 s / pong 60 s; GET 60/min/IP, series 30/min/IP, WS connects 20/min/IP; no
  per-request logs.
- Old public endpoints (`/api/v1/monitor/public/nodes{,/metrics}`, `/system-info?type=2`) keep working for stale
  clients but are tightened: hidden nodes removed, `version` reduced to `(distro/arch)`, node ids replaced by an
  opaque number (same mapping in REST and WS).

### Frontend
- `main.tsx` splits entries: `/tz` loads only the probe chunk (no admin bundle, no service-worker registration);
  other paths check the probe host (cached) then load the admin app.
- Probe page: KPI header (在线 N/总数, 总上下行 + 3-min sparkline from snapshots, 累计流量, 平均负载), toolbar (search,
  region pills, sort, 卡片/紧凑表格), cards with meters, detail dialog with lazy recharts chunk (animations off) +
  latency sparklines. Smoothness rules: GET in parallel with WS, no spinner after first paint, memoized cards,
  CSS width transitions, tabular-nums, pause when hidden, 10 s silence watchdog, GET poll fallback.
- 设置: switch relabelled + hint; probe card (title, domain, latency on/off, per-node show + 显示名).

## Threat list (Komari CVE class) — each item tested

| # | Threat | Defence | Test |
|---|--------|---------|------|
| T1 | Auth bypass on public routes reaching admin / agent functions | the 4 public probe routes only read the in-memory snapshot / bucketed `node_metric`; no probe file calls SendCommand, diagnose, TCP ping, a write or the node control plane (grep: the only write is `UpsertConfig` in the admin-only save handler) | review + `TestProbeContract` (user / anonymous → 403 / 401 on settings) |
| T2 | Data leak (IP, ports, ids, secrets, agent version, users / tunnels / rules) | dedicated whitelist structs in `internal/probe`; version reduced to `(distro/arch)` tokens `[a-z0-9._-]` | `TestBuildSnapshotWhitelist`, `TestProbeSnapshotLiveData`, `TestProbeContract` (exact key sets, byte scans for IP / secret / `"id"` / `version` / port range / hidden name), `TestProbeWebSocket` (frame bytes) |
| T3 | IDOR via ids | opaque HMAC key (10 chars base32 of HMAC-SHA256 with the JWT secret); numeric id, unknown or hidden key → 404 | `TestProbeContract` (k of hidden node, `k=701`, `k=nope` → 404), `TestKeyerOpaqueStable` |
| T4 | WebSocket hijack / cross-site | same Origin rule as `/system-info` (Origin host = Host / X-Forwarded-Host / FLVX_WS_ALLOWED_ORIGINS); stream carries only public data, no cookie is read | `TestProbeWebSocket` (foreign Origin → 403) |
| T5 | Resource exhaustion | WS 5 / address, 200 total (503), queue 4 (slow client dropped), read limit 512 B, client frames discarded, ping 25 s / pong 60 s; GET 60/min, series 30/min, WS connects 20/min per address (buckets capped at 4096 keys); series: ≤ 2 queries at a time, cache keys only (shown node × 3 ranges × 12 point steps), ≤ 512 entries; builder runs only with viewers | `TestHubCapsAndSlowClient`, `TestHubLoopOnlyWithViewersAndSwitchOff`, `TestProbeWebSocket` (6th stream → 429), `TestProbeContract` (GET → 429), `TestClampPointsAndStep` |
| T6 | Command injection / exec / file access | nothing of the kind on the probe path (no shell, no file reads; client WS frames are read and dropped) | review + `TestProbeWebSocket` sends junk |
| T7 | Switch off but data still served | 404 on all probe routes, WS refused, open streams closed with 4404 at the next tick (settings cache reset on save) | `TestProbeContract` (before / after), `TestProbeWebSocket` (4404), `TestHubLoopOnlyWithViewersAndSwitchOff` |
| T8 | Probe domain used to reach the admin API | outermost host gate: on that Host every non-probe path (API, `/system-info`, `/flow`, `/s`, old public endpoints) → 404; lock-out guard on save | `TestProbeContract` (8 paths + X-Forwarded-Host → 404, main host unaffected) |
| T9 | Log flooding / IP logging | probe handlers suppress the per-request log; gate refusals + limits counted (one summary line per minute); nginx `access_log off` for `/api/v1/probe/` | review (grep: no log calls in probe code) |
| T10 | Admin settings abuse | admin-only (middleware `requiresAdmin` + handler `ensureAdminAccess`), title ≤ 40, names ≤ 30, domain regex, body ≤ 64 KB, unknown node ids dropped; generic config API validates the same keys | `TestProbeContract` (invalid inputs rejected, lock-out, broken JSON) |

## Tasks
- [x] Plan docs 187 + 188
- [x] Backend `internal/probe` (types, hub, latency rings, series helpers) + unit tests
- [x] Handler glue: metric observe, node cache, settings, endpoints, host gate, path-latency hook, old endpoint tightening
- [x] Router / middleware (`shouldSkip`, admin path), nginx.conf (WS upgrade block); vite dev proxy: none exists in this
      repo (dev uses `VITE_API_BASE` straight to the backend), the probe page uses the same base — nothing to add
- [x] Contract tests SQLite + PostgreSQL (settings + endpoints + host gate + switch off)
- [x] Frontend entry split + probe page (cards, table, KPI, detail, live hook)
- [x] 设置: switch relabel + probe settings card
- [x] Demo panel (25 nodes, fake live metrics, offline nodes, latency edges), screens 1440/390 light/dark, frames
- [x] Resource numbers (snapshot bytes, CPU 0/1/50 clients, ring memory, series timing, bundle gz)
- [x] Gates: go build/vet, go test (SQLite + PG), tsc/build/lint, push, CI green
- [x] Security self-review written below
- [x] release-notes.md + summary.md

## Results (2026-11-02 TW)

Commits on `feature/3.0.27-probe-page`: 5014df3b (plans), d40cbc94 (backend), 77b173a3 (frontend), 7bfee660 / 7f50044e
(plan 188), f931045f (hardening). CI Build Check green on every push (last: 41323489810 on 7f50044e).
`git merge-tree` against `origin/feature/3.0.27-latency-tag-region` (plan 184): clean, no conflicts.

### Tests
- `go build ./...` OK; `go vet ./...`: only the 5 old `internal/leikwan/testapi` unkeyed-field notes (untouched files).
- `go test ./...` with SQLite **and** PostgreSQL (`FLVX_POSTGRES_TEST_DSN`, temporary postgres:18-alpine, removed after):
  all 22 packages pass (first PG run died on a full tmpfs WAL — infra only; rerun on a disk volume: repo + contract OK,
  0 skips). New: `internal/probe` (keys, OS parsing, whitelist + hidden targets, latency rings + memory, hub
  build-once/shared bytes/caps/slow drop/idle/switch-off, point clamp), `handler.TestProbeSnapshotLiveData`,
  `contract.TestProbeContract` (sqlite + postgres), `contract.TestProbeWebSocket`.
- Frontend: `tsc --noEmit` clean; `vite build` OK; `eslint src`: 94 errors / 3753 warnings vs base fork.105 94 / 3754
  (no new errors; new files 0 warnings; changed files no new warnings).

### Demo (local, loopback-only netns, synthetic DB, never prod)
25 nodes (22 fake agents on the normal `/system-info?type=1` ingest path, metrics every 1 s, TcpPing answered from a
region table so the panel's own 60 s path rounds produce edges; 3 offline), 24 h of seeded `node_metric` history.
Gallery: http://104.145.236.29:8791/probe-page/ — 33 screens (1440 + 390, light + dark), 27/27 automatic checks, 0 page
errors. Smoothness on the demo: snapshot response at 39 ms after navigation start, cards on screen ~170 ms, 25/25 card
DOM nodes kept over 12 s of live updates (no re-mount), layout shift 0, 0 long tasks.

### Resource numbers (demo panel, GOMAXPROCS=2)
- Snapshot (25 nodes): 7 944 B raw, 2 341 B gzip; built once per 2 s only while somebody watches, one marshal + one gzip,
  the same byte slices for every viewer (test asserts identity).
- Panel CPU / RSS (40 s each, 22 agents reporting): 0 viewers 0.67 % / 43.7 MB; 1 viewer 0.82 % / 44.9 MB; 50 viewers
  (distinct addresses, gzip frames) 0.80 % / 47.5 MB; back to 0 viewers 0.60 % / 44.6 MB; threads 8 throughout.
  Per viewer: 30 frames / min, 68 KB / min.
- Latency rings: ~870 B per node pair (60 rounds), e.g. 200 pairs ≈ 170 KB; pairs not measured for 2 h are dropped.
- Series (720 points): 1 h 11 ms cold / 3 ms cached (28 KB), 6 h 10 / 3 ms (32 KB), 24 h 15 / 5 ms (32 KB);
  at most 2 queries at a time.
- Probe bundle: 77.6 KB gz JS (React + page) + 36 KB gz shared CSS; chart chunk (recharts) ~151 KB gz, fetched when idle.

### Security self-review (Komari CVE class)
- **Auth bypass on public routes:** the public surface is exactly 4 GET routes; they read memory / averaged metrics only.
  The only write in the probe code is `UpsertConfig` inside the admin-only save handler (middleware `requiresAdmin` +
  `ensureAdminAccess`). No route on the probe path can reach `SendCommand`, diagnostics, upgrades or files.
- **Remote exec / web terminal / file access:** none exists or was added; client WS frames are read (≤ 512 B) and dropped.
- **WS hijack / origin:** same Origin rule as `/system-info`; no cookie or token is used by the probe stream.
- **IDOR:** no numeric ids accepted or returned; HMAC keys; hidden / unknown keys 404; old public endpoints now return
  opaque numbers too.
- **Leaks:** whitelist structs, exact key-set tests and byte scans (IP, secret, id, version, port range, hidden names).
- **DoS:** per-address buckets (bounded key maps), global WS cap, slow-client drop, series semaphore, bounded caches,
  builder idle without viewers, no per-request logs (nginx `access_log off` on probe paths).
- **Probe domain:** outermost gate, every non-probe path 404 on that host, lock-out guard. Note: if the config read
  fails, the gate falls back to "no probe domain" (fail-open for the gate, fail-closed for the probe itself) — the
  admin API on that host is still behind JWT, so this is a convenience boundary, not an auth boundary.
- Pre-existing, unchanged, noted: the global CORS middleware reflects any Origin with credentials (all APIs); the probe
  does not rely on cookies, so it adds no exposure. Not changed here (out of scope).

### Not done / limits
- 「今日流量」 not shown (no cheap source); the header shows 累计流量 since boot of the online machines.
- Load bar scale = 4.0 (cores are not reported); the number itself is exact.
- Phase 2 latency (agents pinging 三网 / Telegram / Google) not in this build, as agreed.
