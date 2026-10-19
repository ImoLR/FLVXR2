# 111 — Landing monitor (监控 「落地」 tab)

Requested by the user on 2026-10-19 (~12:40Z): shared landing servers / landing nodes must show up in 监控 with
at least their live connection count and live traffic. Base: `maintenance/3.0.27-fork.57-landing-share-fix`
head d1d5e5f3 (tag 3.0.27-fork.57). Branch `feature/3.0.27-landing-monitor`. NO release until the user approves
the screenshots (memory `ui-add-only-preview-first`).

## Decisions
- Add-only: a third 监控 tab 「落地」 next to 节点 / 隧道. The existing tab bar buttons, node and tunnel views stay
  untouched. The tab content uses the node monitor card/table look, wrapped in the plan 106 `flvx-landing-text`
  scope (no text below 16px).
- One endpoint `POST /api/v1/landing/monitor` (raw JWT, `{code,msg,data,ts}`), gated like the other monitor
  endpoints (admin, or a user with monitor permission). A fixed number of indexed queries per request (servers,
  nodes, owners, native node states, integration, user + own server quotas), no N+1.
- Visibility: admin = all servers + all nodes (with owner). Normal user (同步 and 全部 monitor modes alike) =
  active servers they may use (direct grant OR enabled group, same `landingAccessCond` as the 落地 page) and only
  their OWN nodes on them; server aggregates are sums over those own nodes only; the server-wide connection cap
  and the mmwx share totals are admin-only. The user's per-server quota (用户 × 服务器 限额) is shown as their cap.
- Live rates: the panel already receives cumulative counters (native agent flow upload every 5 s, shared Custom
  status poll once per minute per server). After a successful `commitFlowWithLanding`, the counters are fed into an
  in-memory tracker (last two samples per landing node). No new agent/Helper/Custom polling, no new DB writes.
  - rate = delta / dt of the two newest samples; epoch change (native) or a per-direction decrease (shared) =
    counter reset → delta is the new value (bytes since reset), never negative; native same-epoch decrease or an
    older epoch → no rate; samples < 1 s apart don't replace the base sample.
  - sample time: panel receive time (native) / Helper `collected_at` (shared). The UI shows 「数据 N 分钟前」 when
    the newest sample is older than 30 s; rates older than 5 min are not shown.
  - Memory: map keyed by landing node id, evicted on node delete, swept every minute by the existing landing
    reconcile loop against the live node id list (plus a 24 h TTL), hard cap 50 000 entries.
- UI refresh: poll every 5 s only while the 「落地」 tab is open and the page is visible.

## Tasks
- [x] Backend: rate tracker (observe / snapshot / forget / retain / cap) + hook into `commitFlowWithLanding`,
      node delete and the landing reconcile loop
- [x] Backend: repository snapshot method (fixed query count) + `landing/monitor` endpoint + view builder
- [x] Backend tests: rates (reset / epoch / stale / min gap), visibility (admin / granted / ungranted / other
      users' nodes), sample age, eviction + cap; SQLite + PostgreSQL
- [x] Frontend: API types, `LandingMonitorView` (grid + list, desktop + H5), 「落地」 button in 监控
- [x] Build + lint (no new warnings), `go vet`, full `go test ./...` vs baseline
- [x] Local env with real backend + fake counters (agent uploads + fake Custom), screenshots desktop/H5,
      light/dark, admin + user, before/after tab bar → `/root/flvx-workers/runs/landing-monitor/screens/`
- [x] Resource numbers: endpoint latency 50 servers × 200 nodes, tracker memory
- [x] Commit, push branch, CI green (run 39306570482: frontend, agent, backend, PostgreSQL contract all success), summary
- [ ] User approves the screenshots → separate release task (not part of this plan's worker run)

## Results (2026-10-19)
- Commits: e652b3f3 (endpoint + tracker + tests), 893284bb (「落地」 tab), bd21338c (lean snapshot + memory test).
- Tests: new `TestLandingMonitor*` (repo, SQLite + PostgreSQL: visibility admin / group user / direct-grant user /
  ungranted / revoked-grant node / disabled server, user quota periods, fixed query count, no credentials loaded)
  and `TestLandingRateTracker*` + endpoint test (handler, SQLite + PostgreSQL: native epoch reset, older epoch,
  same-epoch decrease, shared per-direction reset, duplicate collection, min gap, staleness, cap / forget / retain /
  TTL, fed only by successful flow commits, 403 without monitor permission, no other-user data in user JSON).
- Full `go test ./...` (PostgreSQL DSN set, `-p 2`): 15 failure lines = identical to the unmodified base
  d1d5e5f3 run in the same environment (known baseline); 0 new. `go vet` clean. Frontend `tsc && vite build` OK;
  ESLint on changed files: 0 new warnings (2 existing in `api/landing.ts`).
- Screenshots (21 + index.html, 0 page errors): real local panel (port 16391) seeded with 3 native + 2 shared
  servers, fake agent counter uploads every 5 s, fake mmwx Custom; desktop + H5, light + dark, admin + user,
  before/after tab bar and 「节点」 tab. `/root/flvx-workers/runs/landing-monitor/screens/`.
- Resource numbers: demo size (5 servers, 11 nodes) p50 2.6 ms / max 16.5 ms server-side, panel RSS 31 MB;
  50 servers × 200 nodes (10 000 nodes, SQLite): admin ~200 ms p50 (2.6 MB JSON, 0.2 MB gzip), user with 2 000 own
  nodes ~27 ms p50; tracker 1.2 MiB per 10 000 nodes (126 B/entry, cap 50 000 ≈ 6 MiB); 5–6 SELECTs per request,
  no writes; one extra `SELECT id FROM landing_node` per minute for eviction; no new agent/Helper/Custom calls.
- Observed but out of scope (existing code): at 10 000 nodes the per-minute native landing sync loads every landing
  node once per server, and native counter billing costs ~0.8 s per 200-counter upload on SQLite. Irrelevant at
  prod size (5 servers, a handful of nodes); worth a look before landings grow large.
- Plan 107 / 108 branches need these three commits (expect small conflicts in `landing.go` route map,
  `handler.go` struct fields, `flow_ingest.go`, `landing_runtime.go`, `api/landing.ts`).
