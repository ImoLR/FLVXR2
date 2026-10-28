# 157 — limits-traffic-pools: 共享流量池 + related-servers picker (BUILD ONLY)

Design: `/root/flvx-workers/contracts/limits-unify-decisions.md` §1 (user decisions 2026-10-28, binding).
Branch `feature/3.0.27-limits-pools` from `maintenance/3.0.27-fork.94-batch-1028` (fork.94 = fa5fe61c).
Build only: no tag, no release, no prod writes, no OTA. Screens go to the user before a later batch release.

## Design (as built)

- Tables (add-only, AutoMigrate, SQLite + PostgreSQL, rollback to fork.94 ignores them):
  - `user_traffic_pool` (id, user_id, name, limit_bytes, used_in, used_out, period_key, paused, created/updated_time).
  - `user_traffic_pool_member` (id, pool_id, user_id, server_kind node|landing, server_ref_id) with a UNIQUE
    (user_id, server_kind, server_ref_id) → a server belongs to at most one pool per user (DB-enforced).
  - Server identity = the existing `user_server_quota` key: `node:<node id>` (FLVX node, also native landing server),
    `landing:<landing_server id>` (shared server).
- Billing (inside the existing flow transaction, no second loop): every forward flow item records one `PoolFlow`
  (user, path tiers, billed path bytes, own-landing key + premium); every landing counter records one too. In
  `ApplyFlowBatch` one query loads the pool members of the batch's users; per flow and pool:
  - own landing server in the pool → the landing premium only (the Core counter brings the rest) → flow counted once;
  - else → per path tier sum of member shares, MAX across tiers → entry + relay + exit in one pool = counted once.
  Billed bytes (same unit as user total), period = user's reset day (`LandingPeriodKey`).
- Pause/resume: `ReconcileLandingPolicy` rolls pool periods/limits; a paused pool marks all its member keys paused in
  the same map per-server quotas use → same rules + landing nodes paused (`paused_server_quota` status reused, so a
  rollback understands it); auto-resume on period reset / raised limit / member removed. `LandingForwardBlockReason`
  refuses resume/create with 「共享流量池已用完」.
- Related servers (admin dialog picker for per-server list AND pools): entry servers of the user's granted tunnels
  (per-entry restrictions honoured) + entry nodes of the user's existing rules → 入口; landing servers the user may use
  or has nodes on (native → node key, shared → landing key) → 落地. Unsaved 可用落地服务器 ticks are merged client-side.
- UI: admin user dialog → 落地 section: per-server list unchanged except the add picker now lists related servers
  with 入口/落地 tags; new 「共享流量池」 block directly under it. User side: read-only 「共享流量池」 card on 主页,
  shown only when the user has at least one pool.

## Checklist

- [x] Plan doc
- [x] Models + AutoMigrate (`user_traffic_pool`, `user_traffic_pool_member`)
- [x] Billing: PoolFlow in FlowBatch, tier-aware coverage, landing counters, apply in the flow tx
- [x] Reconcile: pool period roll + paused → same pause set as per-server quota; forward block reason
- [x] Repo: get/save pools (validation: name, limit ≥ 0, one pool per server per user, existing servers, caps),
      related servers, user read-only view; cleanup on user delete / node delete / user flow reset
- [x] Handler: `/landing/user/get` returns `pools` + `relatedServers`; `/landing/user/save` accepts `pools`
      (absent = keep, for older clients); `/user/package` returns `trafficPools`
- [x] Unit tests (repo + handler) on SQLite and PostgreSQL: once-per-pool accounting (entry+relay+exit, entry+landing,
      split tiers), one-pool-per-server, period roll, pause/resume of rules + landing nodes, block reason
- [ ] Contract test (HTTP save/get + user view)
- [ ] Frontend: related-servers picker (per-server list), 共享流量池 block, user 主页 card; tsc/build/lint
- [ ] Full `go test ./...` SQLite + PostgreSQL vs base
- [ ] End-to-end on a local panel + real agent (loopback): pool exhausts → rule paused on the agent; raise limit → resumed
- [ ] Resource numbers (backend RSS/CPU, agent RSS/CPU before/after)
- [ ] Screens 390 + 1440, light + dark → `/root/mmwx-preview/limits-traffic-pools/`
- [ ] Push branch, CI green
- [ ] Summary → `/root/flvx-workers/runs/limits-traffic-pools/summary.md`
