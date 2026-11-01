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
| T1 | Auth bypass on public routes reaching admin/agent functions | probe routes only read in-memory snapshot / bucketed metrics; no handler on the probe path calls SendCommand, diagnose, writes, or the node control plane | code review + grep (no `SendCommand`/`repo.Upsert`/`Update` in probe files) |
| T2 | Data leak (IP, ports, ids, secrets, agent version, users/tunnels/rules) | dedicated whitelist structs; exact JSON key-set tests; byte scans for IP/secret/id/version | `TestProbeSnapshotKeySet`, `TestProbeNoLeak*` |
| T3 | IDOR via ids | opaque HMAC key; unknown/hidden key → 404; no numeric id accepted | `TestProbeSeriesRejectsUnknownAndHidden` |
| T4 | WS hijack / cross-site | Origin must match Host/X-Forwarded-Host or FLVX_WS_ALLOWED_ORIGINS (same as /system-info); stream is public read-only data anyway, no cookies used | `TestProbeWSOrigin` |
| T5 | Resource exhaustion | caps 5/IP + 200 total, queue 4, read limit 512 B, rate buckets with key caps, bounded caches (keys from node set × 3 ranges × 12 point steps), builder runs only with viewers | `TestProbeWSCaps`, `TestProbeSlowClientDropped`, `TestProbeRateLimit` |
| T6 | Command injection / exec / file access | nothing of the kind exists on the probe path; no shell, no file reads, client messages discarded | review |
| T7 | Switch off but data still served | 404 on all probe endpoints, WS refused, open streams closed within one tick | `TestProbeSwitchOff404` |
| T8 | Probe domain used to reach admin API | host gate 404 for every non-probe path on that host (API, WS, /flow, /s) | `TestProbeHostGate` |
| T9 | Log flooding / IP logging | probe requests suppress the request log; refusals counted (one summary line / min) | review |
| T10 | Admin settings abuse | admin-only endpoints (middleware + handler), title ≤ 40, display names ≤ 30, domain format check, lock-out guard | `TestProbeSettingsValidation` |

## Tasks
- [x] Plan docs 187 + 188
- [ ] Backend `internal/probe` (types, hub, latency rings, series helpers) + unit tests
- [ ] Handler glue: metric observe, node cache, settings, endpoints, host gate, path-latency hook, old endpoint tightening
- [ ] Router / middleware (`shouldSkip`, admin path), nginx.conf (WS upgrade block), vite dev proxy
- [ ] Contract tests SQLite + PostgreSQL (settings + endpoints + host gate + switch off)
- [ ] Frontend entry split + probe page (cards, table, KPI, detail, live hook)
- [ ] 设置: switch relabel + probe settings card
- [ ] Demo panel (25 nodes, fake live metrics, offline nodes, latency edges), screens 1440/390 light/dark, frames
- [ ] Resource numbers (snapshot bytes, CPU 0/1/50 clients, ring memory, series timing, bundle gz)
- [ ] Gates: go build/vet, go test (SQLite + PG), tsc/build/lint, push, CI green
- [ ] Security self-review written below
- [ ] release-notes.md + summary.md

## Results
(filled in as work completes)
