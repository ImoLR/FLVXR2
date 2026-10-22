# 130 — Enforce the user's total 规则数, hide per-tunnel 规则数, anti-abuse (fork.71)

Base: `maintenance/3.0.27-fork.69-abuse-defaults` (8ba076f0, = fork.70 content).
Branch: `feature/3.0.27-user-rule-limit` → release branch `maintenance/3.0.27-fork.71-user-rule-limit`, tag `3.0.27-fork.71`.
Brief: `/root/flvx-workers/briefs/user-rule-limit.md` (user approval 2026-10-23, no screenshot gate).

## Decisions (user)
- Only the user's total `user.num` is enforced. Count = every forward row of the owner (any status) + 「我自己的节点」
  chain creates that have no rule yet. `num <= 0` or `>= 99999` = unlimited.
- Refuse new rules over the limit: 「已达到规则数上限（N 条），请删除不用的规则后再新建」. Existing over-limit rules
  stay untouched (edit / pause / resume / delete keep working).
- Admin actor bypasses the check; normal users are limited.
- Per-tunnel `user_tunnel.num`: hidden in the UI, DB column + API field kept, not enforced.
- Count + insert in one transaction with the owner row locked (SQLite single conn / PostgreSQL `FOR UPDATE`).
- Per-user in-memory rate limit on normal users' forward / landing-node mutations (admin exempt), bounded state.

## Checklist
- [x] Baseline: untouched backend `go test ./...` failure set recorded
- [x] Repo: shared rule-limit check inside the create transaction (forward create + chain create)
- [x] Handler: forward create / import / copy / idempotent create / chain create use one helper + one message
- [x] Contract test `forward_count_limit_contract_test.go` flipped (3rd create refused) + resume/edit of over-limit rules still work
- [x] Authorization sweep + contract tests (other users' forwards / landing nodes, ungranted tunnel / landing server, admin-only fields); holes fixed and listed
- [x] Concurrency tests: 20 parallel creates vs total 规则数 and vs landing caps, SQLite + PostgreSQL
- [x] Rate limiter (token bucket + one in-flight per user per resource type, admin exempt, bounded, no refusal logs/DB writes) + tests
- [x] Frontend: hide per-tunnel 规则配额 on the home page; no per-tunnel 规则数 input anywhere; tsc / lint / build
- [x] Full backend suite: no new failures vs baseline; contract tests on SQLite + PostgreSQL
- [x] Stress: 1000 create/delete cycles as a normal user, before/after rate limit (panel + agent CPU/RSS/fds/goroutines, logs, DB growth)
- [x] Screenshots (desktop + H5, light + dark): grant modal, over-limit toast, home page 规则数
- [ ] Commit, push, CI green, tag `3.0.27-fork.71`, release verified (Latest, same assets as fork.69/70)
- [ ] Prod: rollback dir `pre-fork71-<ts>`, prune to 2 backups, upgrade panel only (no agent OTA)
- [ ] Prod verify: health/version, 29/29 nodes online, forward counts unchanged, user 7's 12 rules untouched
- [ ] Prod live check with a temporary normal user (num=1): 2nd create refused, rapid fire rate-limited; test user removed
- [x] journald 200M cap on SSH-reachable nodes (skip node 36, leave node 48's existing cap)
- [ ] Plan marked complete, summary written

## Notes
- Baseline (untouched 8ba076f0, worktree layout): 15 FAIL lines (10 tests + 5 subtests) — backup import/export,
  federation dual panel x3, legacy migrations x2, renewal TZ, service monitor, tunnel address-in-use, federation
  non-TLS nodelay. Same set as earlier forks.
- Count = `forward` rows of the owner (any status) + `forward_chain` rows `state=creating, op=create, forward_id=0`.
  Chain replays skip the check (the pending chain already holds the slot, checked under the user lock in
  `CreateForwardChain`). Converting an existing rule into a chain rule (attach) no longer checks the limit (edit).
- Race proof: with the in-transaction check disabled the 20-parallel test let 19–20 creates through on SQLite and
  PostgreSQL; with it exactly 5 (= num).
- Authorization holes found (all reproduced on base 8ba076f0 by the new contract test, fixed here):
  1. `forward/update-order` reordered any user's rules (no owner check).
  2. `forward/batch-reset-traffic` let a user reset counters of a rule with an admin per-rule traffic limit → limit lifted.
  3. `forward/traffic-reset-log/delete` deleted any user's reset log.
  4. `user/update-order` (admin user list order) open to normal users.
  5. `tls-template/list` (alias outside `/node/`) returned TLS templates incl. Reality/TLS private keys to any user.
  6. `user/toggle-auto-renew` / `toggle-auto-buy-traffic` changed any user's setting by id.
  7. `payment/config` returned the full payment channel config (易支付 merchant key, USDT secret/token) to any user —
     enough to sign fake payment callbacks. Now only `network`/`currency`.
  8. `user/groups` returned any user's group ids.
  9. `order/status` / `payment/pay` worked on any user's order id.
- Rate limit: forward burst 30 + 1/s, landing burst 10 + 1/3 s, one running call per user per type (others queue
  ≤ 20 s), admin exempt, refusal = code 429 「操作太频繁，请稍后再试」, no DB work, no access-log line, one summary
  log line per minute max; idle users evicted after 10 min.
- Full backend suite after the change: the same 15 FAIL lines as the baseline (diff empty). Contract package with
  `FLVX_POSTGRES_TEST_DSN` (local test container): 13 FAIL lines, all in the baseline set; new PG subtests pass.
- Frontend: `tsc --noEmit` ok, `vite build` ok; eslint on dashboard.tsx shows 1 error (jsx-a11y at the 规则配额
  card's click area) that exists identically on the base file — pre-existing, not touched.
- Per-tunnel 规则数 in the UI: the only place it was shown is the user's home page per-tunnel card (规则配额 +
  progress bar); removed there, 已用规则 kept. No grant/assign modal had an input for it (edit modal sends the stored
  value back unchanged; batch assign sends none). DB column and API field untouched.
- Stress (local, real fork.70 agent binary `gost-amd64` + panel in an isolated netns, panel 127.0.0.1:16371; one
  normal user, rule create → delete on the same port as fast as possible; scripts `runs/user-rule-limit/stress/`):

  | run | cycles done | refused (429) | wall | panel CPU | agent CPU | panel log | agent log | DB growth | agent goroutines / fds at end |
  |---|---|---|---|---|---|---|---|---|---|
  | idle (reference) | – | – | – | 0.01 s | 0.01 s | 0 | 0 | 20 KB | 38 / 6 |
  | fork.70, 1 thread | 1000 | 0 | 16.8 s | 10.1 s | 4.1 s | 2002 lines / 133 KB | 8001 lines / 565 KB | 2.0 MB | 38 / 7 |
  | fork.71, 1 thread | 15 | 985 | 0.7 s | 0.6 s | 0.08 s | 31 / 2.1 KB | 120 / 8.4 KB | 0.6 MB | 38 / 6 |
  | fork.70, 4 threads | 748 (+252 failed) | 0 | 24.9 s | 11.7 s | 4.9 s | 1750 / 118 KB | 12297 / 793 KB | 2.0 MB | **56 / 13** |
  | fork.71, 4 threads | 16 | 1035 | 2.1 s | 0.6 s | 0.09 s | 35 / 2.4 KB | 129 / 9.1 KB | 0.6 MB | 38 / 7 |

  Panel RSS stayed 34–36 MB in every run, panel goroutines 35–36. The fork.70 4-thread run hit
  `bind: address already in use` 252 times and left 3 services (TCP+UDP listeners, observeStats) running on the agent
  although the panel had deleted every rule (pre-existing agent/panel race under parallel churn; fork.71 makes one
  user's mutations sequential, so a single user can no longer trigger it). Sustained worst case with the limit:
  ≤ 0.5 cycle/s per user ≈ 0.5 % panel CPU, 0.2 % agent CPU, ~1 MB/h agent log per abusive user.
- journald (`/etc/systemd/journald.conf.d/flvx-size.conf`, `SystemMaxUse=200M`, journald restarted) applied on the 4
  SSH nodes without any cap: 20 (980 MB → 147 MB), 42 (474 MB → 172 MB), 47 (587 MB → 170 MB), 52 (3.8 GB → 160 MB).
  Already capped (128 MB, untouched): 21 30 31 34 35 45 53; node 48 keeps its own 200 MB cap; node 36 never touched.
  No SSH (skipped): every other node. Script + JSON: `runs/user-rule-limit/journald/`.
- Screenshots (16, desktop + H5, light + dark): `runs/user-rule-limit/screens/` — home page (账号 规则配额 2 / 已用 3,
  per-tunnel card without 规则配额), grant list + edit modal (no 规则数), over-limit toast. All scripted checks passed.
