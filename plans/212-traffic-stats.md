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
- [ ] 1. Model `TrafficDaily` + migration + since marker (SQLite + PostgreSQL)
- [ ] 2. Rollup in `ApplyFlowBatch` (sorted upsert, same tx) + retention cleanup in daily job
- [ ] 3. Repo queries: user server traffic (quota-exact), ranking (3 windows × users/servers), user drill-down
- [ ] 4. API: `/traffic/servers` (own only), `/traffic/ranking`, `/traffic/ranking/user` (admin) + requiresAdmin
- [ ] 5. Backend tests: rollup exactness vs counters, restart, monthly reset, epoch reset, failed tx, day boundary,
        retention, numbers == user_server_quota (period/default/override), permissions, SQLite + PG contract
- [ ] 6. Frontend: `ServerTrafficCard` (user) + `TrafficRankingCard` (admin), lazy chunks, fetched after first paint
- [ ] 7. Demo panel (prod-shaped synthetic data, 30 days fake history in DEMO only) + gallery
        `/root/mmwx-preview/traffic-stats/` (desktop 1440 + H5 390, light + dark, before/after of unchanged parts)
- [ ] 8. Speed before/after (主页 JS gzip, Slow 4G + CPU 4×), card query timing, payload size, rows/day estimate
- [ ] 9. Gates: `go test ./...`, tsc / build / lint vs base; push; CI green
- [ ] 10. release-notes.md, RELEASE-LEDGER row, summary.md
