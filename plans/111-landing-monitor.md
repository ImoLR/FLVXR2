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
- [ ] Frontend: API types, `LandingMonitorView` (grid + list, desktop + H5), 「落地」 button in 监控
- [ ] Build + lint (no new warnings), `go vet`, full `go test ./...` vs baseline
- [ ] Local env with real backend + fake counters (agent uploads + fake Custom), screenshots desktop/H5,
      light/dark, admin + user, before/after tab bar → `/root/flvx-workers/runs/landing-monitor/screens/`
- [ ] Resource numbers: endpoint latency 50 servers × 200 nodes, tracker memory
- [ ] Commit, push branch, CI green, summary
