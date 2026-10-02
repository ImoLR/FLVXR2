# 057 nftables domain-refresh job query fix (3.0.27-fork.11)

## Problem
Production backend logs every 10 min:
`[nftables-dns] 查询活跃 nftables 转发失败: SQL logic error: no such table: forward_records (1)`.

`Repository.ListActiveNftablesForwards()` (`go-backend/internal/store/repo/repository_control.go`)
ran `Find(&[]model.ForwardRecord)`. `ForwardRecord` is a view struct without `TableName()`,
so GORM queried the non-existent table `forward_records`. `runNftablesDomainRefreshJob`
(`handler/jobs.go`) therefore never ran and nftables-mode forwards with a domain target
never got their resolved IP refreshed.

## Fix
Query `model.Forward` (table `forward`) and map to `model.ForwardRecord`, like the sibling
functions `ListForwardsByTunnelTx` / `ListForwardsForCNCheck`. Job logic unchanged.

Audit of other repo queries into `ForwardRecord` / view structs: none found
(all other `Find(&forwards)` calls already use `[]model.Forward`).

## Checklist
- [x] Fix `ListActiveNftablesForwards` to query `forward` table
- [x] Repo-level test (SQLite) covering active/inactive nftables + non-nftables forwards
- [x] Full `go test ./...` and compare with 19 known pre-existing failures
- [x] Preview: list production nftables forwards and which will be re-synced
- [ ] Commit, push branch, tag `3.0.27-fork.11`, CI + release verified
- [ ] Backup production (rollback dir, validated sqlite, image tags, metadata)
- [ ] Upgrade `/opt/flvx-svc` to fork.11
- [ ] Verify health, node metrics, nftables-dns job run, error gone
- [ ] Update memory, write report

## Preview (production DB snapshot, 2026-10-02 ~23:10Z)
6 nftables forwards; only those with status=1 and a domain in remote_addr are re-synced
on the first job run (in-memory cache empty after restart):

| id | name | user | status | remote_addr | tunnel | entry node (online) | re-sync? |
|----|------|------|--------|-------------|--------|---------------------|----------|
| 6  | hy | aminuouz2246885 (3) | 1 | 82.152.163.228:34749 | 3 | 2 Mkcloud 深港ixp 1 (yes) | no (literal IP) |
| 14 | HK5 | muwubbq (2) | 0 | 154.3.38.231:28310 | 3 | 2 (yes) | no (paused) |
| 31 | HK4 | muwubbq (2) | 0 | 198.176.54.233:45823 | 3 | 2 (yes) | no (paused) |
| 32 | TW2专线 | muwubbq (2) | 0 | 82.152.91.122:18174 | 4 | 2 (yes) | no (paused) |
| 47 | TW1专线 | muwubbq (2) | 0 | 82.152.91.122:44790 | 4 | 2 (yes) | no (paused) |
| 98 | mk1 | mraruer (12) | 1 | dmit-jpt1.597000.xyz:36661 | 39 | 23 Mkcloud 沪日ixp 440 (yes) | **yes** → 179.253.252.112:36661 |

The job's first run is 10 min after backend start (`time.After(10 * time.Minute)`), then every 10 min.

## Test results
- New `TestListActiveNftablesForwardsQueriesForwardTable` fails on unfixed code with the exact
  production error (`no such table: forward_records`) and passes with the fix.
- `go test ./... -count=1`: 19 failing tests/subtests, identical set to `/root/flvx-fork10/base.fails`
  (pre-existing; no new failures).
