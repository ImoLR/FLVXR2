# 212 — 主页流量统计: user 「服务器流量」 + admin 「流量排行」 (BUILD ONLY)

Branch `feature/3.0.27-traffic-stats` from tag `3.0.27-fork.115`. Panel only (no agent change, no OTA). DB additive
(one new table `traffic_daily` + one `vite_config` marker). Screens first — no release, prod read-only.

## Decisions
- **Data source = the billing transaction.** `ApplyFlowBatch` already holds, per agent upload, the billed per-user deltas
  (`batch.Users`, the numbers added to `user.in_flow/out_flow`) and the billed per-user × per-server deltas
  (`batch.ServerQuotas`, the numbers added to `user_server_quota`, incl. landing counters). The rollup adds them to
  `traffic_daily` (day = UTC+8 date of the batch) in the SAME transaction with one sorted multi-row upsert:
  exactly-once with the counters (a rejected upload is resent and rolled back together), no snapshots, so restarts,
  monthly flow resets, quota resets and Core epoch changes cannot double count or lose bytes. No new job, no agent work.
- Rows: `server_kind` = `total` (ref 0, the user's billed total) | `node` (FLVX node: entry / relay / exit / native
  landing) | `landing` (shared landing server) — same kinds as `user_server_quota`.
- `traffic_daily_since` (vite_config, ms) = when the rollup started; windows that start earlier say 「统计从 MM-DD HH:mm 开始」.
  No back-fill (statistics_flow has no per-server split and resets; user_traffic_history is empty on prod).
- Retention 400 days, deleted by the existing 00:00:05 daily maintenance job (index on day).
- User card numbers are NOT taken from the rollup: 本期已用 / 限额 / 已用完 come from `user_server_quota` + the
  server default exactly like enforcement (`applyServerQuotasTx`): stale period → 0, no override → current default.
  Rows = `UserRelatedServers` (granted entry servers + usable landing servers, same list as the admin picker).
- Admin ranking: one request returns 今日 / 本周(近 7 天) / 本月 × 用户 / 服务器; click a user → one small request with
  that user's per-server split. Admin-only by middleware prefix + handler check.

## Tasks
- [x] 1. Model `TrafficDaily` + migration + since marker (SQLite + PostgreSQL)
- [x] 2. Rollup in `ApplyFlowBatch` (sorted upsert, same tx) + retention cleanup in daily job
- [x] 3. Repo queries: user server traffic (quota-exact), ranking (3 windows × users/servers), user drill-down
- [x] 4. API: `/traffic/servers` (own only), `/traffic/ranking`, `/traffic/ranking/user` (admin) + requiresAdmin
- [x] 5. Backend tests: rollup exactness vs counters, restart, monthly reset, epoch reset, failed tx, day boundary,
        retention, numbers == user_server_quota (period/default/override), permissions, SQLite + PG contract
- [x] 6. Frontend: `ServerTrafficCard` (user) + `TrafficRankingCard` (admin), lazy chunks, fetched after first paint
- [x] 7. Demo panel (prod-shaped synthetic data, 30 days fake history in DEMO only) + gallery
        `/root/mmwx-preview/traffic-stats/` (desktop 1440 + H5 390, light + dark, before/after of unchanged parts)
- [x] 8. Speed before/after (主页 JS gzip, Slow 4G + CPU 4×), card query timing, payload size, rows/day estimate
- [x] 9. Gates: `go test ./...`, tsc / build / lint vs base; push; CI green
- [x] 10. release-notes.md, RELEASE-LEDGER row, summary.md

## Results (build)
- Backend: `go test ./...` 25/25 packages ok (SQLite + PostgreSQL via FLVX_POSTGRES_TEST_DSN); new tests:
  rollup == counters (forward flow, landing Core counters incl. duplicate snapshot + epoch restart), rejected upload
  rolls the rollup back, monthly flow reset + server period roll, day boundary 23:59:59 / 00:00:01 UTC+8, 400-day
  purge, restart (since marker kept, no loss / repeat), card numbers == user_server_quota (default / override /
  stale period / admin / other user), ranking windows + partial + roles + drill-down + user delete, HTTP contract with
  real encrypted uploads (own servers only, 403 for users on the ranking, unknown fields refused).
- Benchmark: typical upload (3 users × 3 servers) ~5.2 ms, rollup part <= 0.25 ms.
- Frontend: tsc clean; eslint whole project 94 errors / 3789 warnings on fork.115 and on the branch (no new ones);
  主页 chunk gzip 58.50 → 59.04 KB; new lazy chunks user 2.8 KB / admin 4.5 KB gzip (loaded after first paint, on idle).
- Slow 4G + CPU 4×, base/branch interleaved, 7 runs, median: login → 主页 data demo 1311 / 1312 ms, admin 1223 /
  1282 ms (noise ±90 ms both ways); new card visible ~0.6–0.9 s after the data.
- Demo API (4029 day rows, 14 servers): /traffic/servers 1.3 ms / 2.5 KB, /traffic/ranking 3.8 ms / 14.6 KB,
  drill-down 1.2 ms / 1.7 KB.
- Screens: http://104.145.236.29:8791/traffic-stats/ — existing parts markup identical 36/36, pixels 29/36 (rest =
  translucent background shading only).
- CI Build Check 41902978455 green on c31759be (frontend, backend, agent, PostgreSQL contract).
- Prod sizing (read-only): ~60 rows/day → ~22 k rows, ~2–3 MB per year (400-day cap).

## Follow-up (user 2026-11-05 「都按你的建议做」) — BUILD ONLY
- [x] F1. 「流量排行 → 服务器」 lists only 入口 + 落地 servers (relay/exit-only nodes and deleted nodes left out; roles
        shown are 入口/落地 only); same filter for the user drill-down; note reworded (经入口到落地的流量两台各算一次).
- [x] F2. Admin 「流量排行」 card moved right under the 24 h chart (user 「服务器流量」 card and existing cards unchanged).
- [x] F3. Admin accounts' own traffic left out of the 用户 ranking (server totals still include it); UI says 「不含管理员」.
- [x] F4. 后台 「落地 → 用户设置」 已用 = current period: `GetLandingUserSetting` zeroes 已用 / 额度已用完 of rows whose
        period has elapsed (display only, stored counters untouched); test `TestLandingUserSettingUsedIsCurrentPeriod`.
- [ ] F5. Gates: `go test ./...`, tsc / build / lint vs base; push; CI green
- [ ] F6. Screens (moved card, server toggle, 用户设置 after reset; 1440 + 390, light + dark), release notes, ledger, summary
