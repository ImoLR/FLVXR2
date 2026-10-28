# 159 — limits-server-speed: per-server 速率 + user default + binding cleanup (BUILD ONLY)

Design: `/root/flvx-workers/contracts/limits-unify-decisions.md` §3 (user decisions 2026-10-28, binding).
Branch `feature/3.0.27-limits-speed` from `origin/feature/3.0.27-limits-conns` (plan 158, 5868e060).
Build only: no tag, no release, no prod writes, no OTA. Screens go to the user (with 157/158) before a batch release.
Contract for the mmwx side: `/root/flvx-workers/contracts/landing-user-limits-v1.md` §7 (speed, added here).

## Facts verified in code (before the change)

- A gost service references ONE traffic limiter by name (`service.limiter`). The registry hands out a lazy
  wrapper, so a name may be referenced before it exists (no limit until it is registered). A limiter's `$`
  (service scope) bucket is one object per registered limiter: every service referencing the same name on a
  machine shares it (`limiter/traffic/traffic.go` In/Out ScopeService → `inLimits["$"]`).
- Each service wraps that reference in `cachedTrafficLimiter` (30 s refresh, "equal" = same Limit()):
  `UpdateLimiters` re-creates the object; services that cached the old one with the same rate keep it
  (bucket split), and a deleted limiter stays in force until the service is re-created.
- Forward handlers do not use the handler-level traffic limiter, so a second limit on a service needs agent code.
- nftables rules: one inline policer per rule and direction (`queueAccounting`), no shared object.
- Landing Core 2bf03c9a (`common/mmwxcustom/connection/tracker.go`) and mmwx Custom v1.4.78: connection
  limits only (total / inbound / IPs / outbound new-per-second), no bandwidth limit; the vision limiter hook is
  a Go hook nobody registers in the standalone Core → per-user landing rate is NOT possible without a Core change.
- Rule speed (`forward.speed_limit` dynamic, `forward.speed_id`) is admin-only in the API. `user.speed_limit_id`
  and `tunnel.speed_id` are only copied into NEW rules; `node.speed_id` and `user.speed_limit` are never enforced.
  Shop packages: the admin UI entered/showed `speedLimit` as Mbps, only a backend comment said MB/s; not enforced.
- Shared landing servers are never deleted by FLVX: 妙妙屋 revoking a share is the terminal state (`revoked`).

## Design

### Data (add-only, SQLite + PostgreSQL, rollback to fork.94 ignores it)
- `user_server_quota.rate_mbps` INT NOT NULL DEFAULT 0 + `rate_override` BOOL NOT NULL DEFAULT false
  (false = follow the user default; true = own value, 0 = 不限).
- `user.server_rate_mbps` INT NOT NULL DEFAULT 0 — user default 速率 for every server (0 = 不限).
- `node.user_rate` INT NOT NULL DEFAULT 0 — agent capability (shared per-user limiter v1), from the handshake.

### Effective rate of user U on entry server N
`row(U,N).rate_override ? row.rate_mbps : user.server_rate_mbps` — applies to U's forward entries on N
(relay / exit hops and landing Cores are not limited).

### Agent (gost services)
- One limiter per user per machine `flvx_user_<U>_rate` (`$ <MB> <MB>`, Mbps/8) while the rate > 0.
- Capable agents (handshake `userRate=1`): every forward service carries `metadata.userLimiter`; the listener
  limiter = rule's own limiter (cached as before) + the user limiter looked up on every call → both apply, all
  the user's services on the machine share one bucket, rate changes / removal are hot (limiter only, no
  service restart).
- Older agents: rules WITHOUT an own limiter reference the user limiter as their `limiter` (shared bucket);
  rules with an own limiter keep it (not counted in the shared bucket). The panel never re-creates an unchanged
  user limiter (memo per node session) so the gost cache cannot split the bucket.
- nftables-mode rules: per-rule policer = min(own, row rate) (kernel rules cannot share a bucket here).
- Reconnect diff: expected user limiters are part of the node's expected objects; stale `flvx_user_*_rate`
  limiters are deleted.

### Panel
- Row save / user default change → recompute (U,N) rates → push / update / delete limiters (+ re-push services on
  old agents or nft rules only when their payload changes).
- Rule speed set by an admin must be ≤ the row rate of each entry server (inherited defaults are not checked; the
  shared limiter caps them anyway).
- No surprise on upgrade: all new fields start empty; service payloads of agents without the capability are
  byte-identical; legacy `user.speed_limit_id` keeps its old meaning (default for new rules) until an admin sets the
  new default (then it is cleared).

### Bindings / units
- User dialog: 「总限速规则」 select → 「默认速率 (Mbps)」 (user default); a legacy binding is shown as a note with 清除.
- 限速规则 page: 用户 / 节点 bindings hidden (retired, note where to set it); 隧道 / 规则 bindings unchanged.
- Units: Mbps everywhere (backend comments of package / user speed corrected; values were always entered as Mbps).

### Landing
- Native landing: no per-user rate (Core cannot) → row shows 「—」 for landing-only servers; 「需要用户决定」.
- Shared: contract §7 (`max_rate_mbps` per user, capability version 2); FLVX sends it only to shares advertising it.

### Found in the lab and fixed
- The agent's config report (`/flow/config`, on connect + every 10 minutes) ran `cleanOrphanedLimiters`, which
  deletes every limiter whose name is not a numeric 限速规则 id. A user rate limiter is looked up per call, so the
  rate would have been lifted at the next report. It is now kept while the user has a rate on that node.
  (Pre-existing, unchanged: the same sweep also deletes `forward_<id>_speed` / `arena_speed_*` limiters; gost's
  per-service cache keeps them in force for running services.)

### Ride-along: orphan per-server rows
- Node delete already removes `node` rows (quota + pool members) in its transaction (plan 157); shared share
  revoked → its `landing` rows (quota + pool members) removed in the sync transaction; startup cleanup of rows whose
  node / shared server is gone or revoked (idempotent, logs the count). Empty pools stay.

## Checklist

- [x] Plan doc
- [x] Model + AutoMigrate (3 columns), repo: effective rates, save/get rows + user default, capability persist
- [x] Orphan rows: revoke cleanup in tx + startup cleanup + tests (SQLite + PostgreSQL)
- [x] Panel runtime: limiter push/delete, service payload (capable / old / nft), reconnect diff + stale cleanup
- [x] Rate change propagation (row save, user default) + rule speed ≤ row validation
- [x] Agent: handshake capability, composite listener limiter, tests (shared bucket across services)
- [x] Shared: contract §7 + FLVX side (capability v2, `max_rate_mbps`), fake-Custom test
- [x] Frontend: row 速率 column, user default field + legacy note, 限速规则 bindings cleanup, labels; tsc/build/lint
- [x] Full `go test ./...` SQLite + PostgreSQL; go-gost / go-gost/x tests
- [x] E2E lab: panel + real agent (netns), two rules sharing a 50 Mbps row (throughput), old-agent fallback
- [x] Resource numbers (agent CPU at full rate before/after, backend RSS/CPU)
- [x] Screens 390 + 1440, light + dark → `/root/mmwx-preview/limits-server-speed/`
- [x] Push branch, CI green
- [x] Summary → `/root/flvx-workers/runs/limits-server-speed/summary.md`

## Results

- Lab (netns, offline, synthetic data; `runs/limits-server-speed/lab/`): panel + real agent; FLVX Mbps = B/s / 131072.
  Row 50 → two rules 24.8 + 24.8 = 49.6 (steady), one rule 49.6; rule own 20 under row 50 → 29.6 + 20.0 = 49.6;
  rule own 60 > row 50 rejected; row removed → unlimited at once, 0 service restarts; user default 30 → 30.4;
  older agent (plan-158 build) fallback 15.1 + 15.2 = 30.4; nftables rule 49.7 per rule (not pooled with gost);
  rollback: fork.94 panel boots on the migrated DB.
- Agent CPU (CPU-s per GB at full rate, median of 3): base 0.81, new without rate 0.86 (noise level), one active
  limiter 1.00–1.04 (= any existing rule limiter), rule + user limiter together 1.11. At 50 Mbps shared: base 6.0–6.6 %,
  new 5.3–6.2 %. Agent RSS 48–55 MB both. Panel idle (fresh, 60 s): fork.94 0.20 % / 35.1 MB, 159 0.20 % / 34.7 MB.
- Screens: `/root/mmwx-preview/limits-server-speed/` (20 shots, 36/36 checks, 0 page errors).
- Tests: go-backend `go test ./...` SQLite 19/19 and PostgreSQL 19/19 packages ok (new: repo rate/orphan/revoke
  tests on both dialects, handler push/reconnect/fallback/config-report tests, shared v2 contract test on both);
  go-gost/x 19/19 (new: shared bucket across services, combine), go-gost 1/1; frontend tsc 0, build ok, eslint no
  new findings in the 6 touched files; CI (backend, PG contract, frontend, agent) green on every push.
- Measuring tool: `runs/limits-server-speed/lab/tput/main.go` (iperf3 is not installed on this host; nothing was installed).
