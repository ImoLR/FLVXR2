# 170 — speed-simplify: only two speed layers left (user default 速率 + per-user per-server 速率) — BUILD ONLY

Brief: `/root/flvx-workers/runs/speed-simplify/brief.md` (user 2026-10-30 ~23:50 TW 「现在的限速不要搞那么复杂」,
「就按你推荐的做」). Branch `feature/3.0.27-speed-simplify` from `origin/feature/3.0.27-entry-rate-tc` (plan 168, built,
unreleased; based on tag `3.0.27-fork.97` = prod). Build only: no tag, no release, no prod writes, no OTA. The batch release
worker `release-batch-1031` ships 168 + 169 + 170 together.

## Prod facts (coordinator, read-only SQL 10-30 23:50 TW)

`speed_limit` 0 rows (the user deleted every 限速规则 incl. 竞技场 at 23:43 TW); 0 forwards with speed_id /
speed_limit_enabled / speed_limit > 0; 0 tunnel / user_tunnel / node speed_id; 0 user.speed_limit_id / speed_limit /
server_rate_mbps; 0 user_server_quota rate rows; 0 package speed; 0 peer_share.max_bandwidth → nothing to migrate,
the release changes no live traffic. (`node.user_rate` / `landing_rate` are capability flags, not limits.)

## What stays

1. 用户默认速率 `user.server_rate_mbps` (default of every server row).
2. 用户在每台服务器上的速率 `user_server_quota.rate_mbps` / `rate_override` (entry AND landing servers incl. shared,
   plans 159 / 161). Entry: tc user class on plan-168 agents, gost user limiter / nft policer on older agents and
   kernels without tc. Landing paths unchanged.

## What is retired (DB columns stay, nothing reads them at runtime)

- 限速规则 (`speed_limit` table, 竞技场 `arena_mode`) and every binding to it: `forward.speed_id`,
  `tunnel.speed_id` (default for new rules), `user_tunnel.speed_id` (用户隧道权限 限速), `user.speed_limit_id`
  (legacy 总限速规则 default), `node.speed_id`.
- The rule's own dynamic speed `forward.speed_limit_enabled` / `forward.speed_limit`.

## Design

### Runtime (panel)
- `resolveForwardRuntimeInputs` resolves only tunnel / user tunnel / service base — no limiter id, speed, arena,
  dynamic limiter. Gost services never carry a rule `limiter`; nftables rules never carry a rule speed (the user's row
  rate still goes in on non-tc nodes via `capNftRulesByUserRate`, unchanged).
- `repo.EntryRateRules` (mirror for the plan-168 tree) no longer reads speed_id / user_tunnel.speed_id / dynamic speed
  (one query instead of four) → the tree only gets user classes; the plan-168 rule / arena class code stays (tested)
  but is never reached; `agentArena` is always empty so the cross-user-arena fallback cannot trigger.
- Older agents (fork.97 and before): a rule without a limit had no `limiter` and no nft speed before → payloads of
  such rules are byte-identical (prod has only such rules).
- Config-report limiter sweep (`cleanOrphanedLimiters`): numeric 限速规则 limiters are no longer kept (nothing
  references them any more); user 速率 limiters stay as before.

### Write APIs
- `speed-limit/create`, `speed-limit/update` (incl. bindings) → `{code:-1, msg:"限速规则已停用…"}` (no 500).
  `list` / `detail` / `options` stay read-only; `delete` keeps working (see bug fix).
- Forward create / update: `speedId`, `speedLimitEnabled`, `speedLimit` ignored; stored values are cleared
  (NULL / false / 0) on create and update; nothing copies `tunnel.speed_id` / `user.speed_limit_id` into new rules.
- Tunnel create / update: `speedId` ignored, stored NULL. User create / update: `speedLimitId` ignored, stored NULL.
  User tunnel grant / edit / batch: `speedId` ignored, stored NULL.
- Rule speed ≤ row validation (plan 159) removed with the field.

### Bug fix: deleting a 限速规则
- Before: the row was deleted, references in forward / tunnel / user_tunnel / user / node stayed, affected rules were
  not resynced. Now: one transaction clears every reference and deletes the row; afterwards the affected active
  rules (own speed_id or via their user tunnel) are re-pushed.
- Startup cleanup (idempotent, never blocks the start): NULL every speed reference to a missing `speed_limit` id
  (forward / tunnel / user_tunnel / node speed_id, user.speed_limit_id); logs the counts. Prod expected 0 rows —
  the release worker's pre-upgrade DB backup covers it.

### UI (hide, do not delete columns)
- 限速规则 page: menu (desktop sidebar, H5 tabs, profile shortcut) removed; `/limit` redirects home.
- Rule dialog 高级功能: 「规则限速」 select + plan-168 yellow hint under it removed; rule save sends no speed fields.
- 用户隧道权限 dialog: 「限速」 field removed (+ list column / chip if any).
- Tunnel form: 「隧道限速」 removed. User form legacy 限速 select / note removed. Shop / package speed display removed.
- Kept: 编辑用户 → 默认速率 + per-server 速率 rows with the plan-168 yellow fallback hints (texts about 「有单独限速
  的规则」 dropped — rule limits no longer exist).

## Checklist

- [x] Plan doc
- [x] Backend runtime: inputs / gost services / nft payloads / EntryRateRules / resync-for-rate / limiter sweep
- [x] Backend write APIs: speed-limit create/update reject; forward / tunnel / user / user_tunnel ignore + clear
- [x] Bug fix: delete clears refs in one tx + resync affected rules; startup orphan cleanup (idempotent)
- [x] Backend tests (stale speed_id / dynamic speed / user_tunnel speed_id → no limiter, no tc class; new rule copies
      nothing; delete clears + resync; cleanup idempotent; row + default → tc user class); existing tests adjusted
- [x] Full `go test ./...` SQLite + PostgreSQL; go-gost/x landing + socket
- [x] Frontend: hide all spots; tsc 0 errors, build ok, lint no new errors
- [x] Headless UI check on a local demo panel (PNGs in run dir), backend RSS / CPU idle before / after
- [x] Plan 168 doc note (decisions 2 / 3 moot)
- [ ] Push branch, CI green
- [ ] `runs/speed-simplify/release-notes.md`, `summary.md`

## Results

- Tests: go-backend `go test ./...` SQLite 19/19 packages ok, PostgreSQL 19/19 ok (`FLVX_POSTGRES_TEST_DSN`, local
  postgres:18). New: `speed_retire_test.go` (delete → only differing rules re-pushed, create/update refused, tree from
  row + default only), `repository_speed_retire_test.go` (delete clears refs + affected ids, startup cleanup idempotent,
  both dialects); updated: entry-rate / reconnect / user-rate handler tests, repo EntryRateRules, contract tests
  (forward speed fields, inherit nothing, limiter-dispatch tests → retired speedId ignored, speed-limit retired writes +
  delete clears references). go-gost / go-gost/x: not touched (no agent change, no OTA needed for 170).
- Old agents: every command the panel sends for rules without a limit (3 scenarios: no rate / rate on an old agent /
  rate on a userRate agent; 65 commands incl. gost services, nft rules, user limiters) is byte-identical to the base
  branch (plan 168).
- Frontend: tsc 0 errors, `npm run build` ok, eslint on the 12 touched files 28 errors / 1999 warnings vs base 29 / 2048
  (no new findings). Trial merge with plan 169 (`origin/feature/3.0.27-onboarding-hints-always`): no conflicts, tsc 0.
- Headless check (local demo panel 127.0.0.1:16701, synthetic SQLite data incl. stale speed refs): 21/21 checks —
  sidebar / H5 tabs / H5 「更多」 / 我的 have no 限速, `/limit` → `/dashboard`, rule 高级功能 has no 规则限速 / hint,
  用户隧道权限 list + edit have no 限速, tunnel form has no 隧道限速, 编辑用户 keeps 默认速率 + server 速率 rows (+ the
  plan-168 yellow hint). PNGs: `runs/speed-simplify/screens/` (release worker only).
- Startup cleanup on the demo DB: a dangling `tunnel.speed_id = 9` cleared, references to the existing limit kept
  (inert), second start silent.
- Resources (idle 60 s, same demo DB copy, 2 rounds): base 32.8 / 33.0 MB RSS, 1–2 CPU ticks; new 33.8 / 34.0 MB,
  1 tick (noise level). No new poller or per-tick query; per push fewer queries (`EntryRateRules` 4 → 1, no
  `GetSpeedLimit` lookups); startup cleanup = 5 UPDATEs once.
