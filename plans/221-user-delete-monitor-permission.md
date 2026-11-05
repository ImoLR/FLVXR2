# 221 — User delete removes the monitor grant

## Background

2026-11-06: deleting user `ns_3rd` (id 63) on prod removed every forward, landing
node, tunnel grant, group membership, quota and statistic as intended (verified in
the DB and on Breadcloud US / Dmit Echo), but left its `monitor_permission` row.
Two older deleted users (ids 15, 17) had left one each. The rows are inert while
the id stays unused, but `monitor_permission.user_id` is unique, so a reused id
would inherit the monitor grant.

## Change

- `DeleteUserCascade` deletes the user's `monitor_permission` row in the same
  transaction.
- Startup runs `cleanupMonitorPermissionOrphans` (every start, idempotent, never
  blocks the start) to drop rows whose user no longer exists — removes the 3
  existing prod rows on upgrade (log line `[monitor-permission-cleanup] removed 3 orphan rows`).

No schema change, panel only, no OTA.

## Tasks

- [x] Cascade delete of `monitor_permission` in `DeleteUserCascade`
- [x] Startup orphan cleanup at both repository open paths
- [x] Test `TestMonitorPermissionUserDelete` (sqlite; postgres when `FLVX_POSTGRES_TEST_DSN` is set)
- [x] `go test ./...` in go-backend green
- [ ] Release with the next batch; after upgrade check the log line and that prod has 0 orphan `monitor_permission` rows
