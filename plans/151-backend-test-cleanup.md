# 151 — Make the long-standing failing go-backend tests pass (build + CI only)

Task `backend-test-cleanup` (user approval 2026-10-24 ~23:00 TW 「按理推荐的修就可以了」).
No tag, no release, no prod, no OTA. Branch `feature/3.0.27-backend-test-cleanup` on the newest released
maintenance branch `maintenance/3.0.27-fork.93-rule-entry-bind` (734abae3). Run dir `/root/flvx-workers/runs/backend-test-cleanup/`.

## Checklist

- [x] Read memory, previous release test scripts (`runs/release-landing-chip-polish/tests-all.sh`), old list `/root/flvx-fork10/base.fails`
- [x] Baseline `go test ./...` on SQLite + PostgreSQL (unmodified fork.93), exact failing list recorded
- [x] Classify every failure (stale / environment-dependent / real bug) — table below
- [x] Fix stale tests (cite the commit that changed the behaviour)
- [x] Make environment-dependent / racy tests hermetic
- [x] Fix real product bugs with a minimal change + test (listed separately)
- [x] `go test ./...` green on SQLite and PostgreSQL (19/19 packages each, 0 FAIL); contract package ×3 and handler package ×2 repeated runs green
- [x] go-gost / go-gost/x suites and frontend tsc/build unchanged (no files touched there)
- [x] Push branch, CI green (run 40153930941: build backend / agent / frontend + PostgreSQL contract job all success)
- [x] Summary (`runs/backend-test-cleanup/summary.md`), plan marked complete

## Result

Commits on `feature/3.0.27-backend-test-cleanup`: `04f5a598` fix(backup) (product), `72863060` tests, plus this plan.
Not tagged or released; rides along with the next release. It is **not** test-only: the backup import change needs
the normal release path (backend-only, no UI change → no screenshots needed).

## Baseline (unmodified fork.93, `go test -p 1 -count=1 ./...` in go-backend)

Same list on SQLite and PostgreSQL (`FLVX_POSTGRES_TEST_DSN` = local postgres:18 test container), identical to
fork.87 → fork.91 and to the fork.10 reference list minus the two connectIp tests fixed earlier:

| # | Test | Package |
|---|------|---------|
| 1 | TestBuildFederationServiceConfig_NonTLSProtocol_NoNodelay | internal/http/handler |
| 2 | TestRunNodeRenewalCycleJob_AdvancesOverdueAnchorTimes | internal/http/handler |
| 3–5 | TestFederationDualPanel{MiddleExitAutoPort,RemoteDiagnosis,RemoteEntryRuntime}Contract | tests/contract |
| 6 | TestTunnelUpdateRecoversFromAddressInUseContract | tests/contract |
| 7 | TestBackupExportImportRestoreContracts (4 subtests) | tests/contract |
| 8–9 | TestOpenMigratesLegacyNodeDualStackColumns / TestOpenMigratesVeryLegacyNodeAndTunnelColumns | tests/contract |
| 10 | TestServiceMonitorResults (1 subtest) | tests/contract |
| 11 | TestForwardCreateRollbackWhenLimiterDispatchFailsContract — SQLite run only (intermittent) | tests/contract |

Peak RAM of the whole two-database run (cgroup memory.peak, incl. page cache): ~850 MiB; no leftover files in TMPDIR.

## After (this branch)

- SQLite 263 s, PostgreSQL 568 s: 19/19 packages ok each, 0 FAIL; peak 649 MiB (memory.peak of the run's cgroup).
- Repeats: `tests/contract` ×3 and `internal/http/handler` ×2 → 0 FAIL (peak 837 MiB).
- No processes, containers or TMPDIR files left behind (the shared postgres test container was already running and is
  reused; PostgreSQL tests drop their own schemas).

## Classification and changes

| Test | Case | Cause | Change |
|------|------|-------|--------|
| TestBuildFederationServiceConfig_NonTLSProtocol_NoNodelay | stale | upstream 2a0752f6 (2026-04-21) gives every relay handler `nodelay` + `udpTTL: 5s`, not only TLS (same as tunnel chain services) | renamed `…_NonTLSProtocol_AlsoSetsNodelay`, asserts exactly `{nodelay:true, udpTTL:5s}` |
| TestRunNodeRenewalCycleJob_AdvancesOverdueAnchorTimes | environment (host TZ) | `advanceByMonths` does calendar months in `time.Local` (container TZ=Asia/Shanghai); the test built its clock in UTC, so on this host (Europe/London) Nov→May crossed the BST switch and the anchor was 1 h off | test clock built in `time.Local`; also asserts the node without a cycle keeps NULL expiry; passes with TZ = UTC, Europe/London, Asia/Shanghai, America/New_York, Pacific/Auckland |
| TestFederationDualPanel… (3) | stale | a09a2027 (2026-06-06) added the SSRF guard to `/federation/node/import`; the provider panel in these tests is a loopback httptest server, rejected by design | `importRemoteNodeForContract` lifts the guard only for its own request via the existing `handler.DisableSafeRemoteAddrCheckForTesting` (restored right after); the guard itself stays covered by `TestNodeImportRejectsRestrictedRemoteURL`. All three flows now run to the end and pass |
| TestTunnelUpdateRecoversFromAddressInUseContract | stale | upstream 2a0752f6 makes a hop listen on its node's `tcp_listen_addr`; `connectIp` is only the address the previous hop dials (may be a NAT/public IP the node cannot bind) | expects both AddService retries on `10.41.0.9:43101` (tcp_listen_addr) and now also checks the entry's AddChains dials the connectIp `10.41.0.99:43101` (and not the listen/server IP) |
| TestBackupExportImportRestoreContracts (4 subtests) | stale + **real bug** | upstream 1b4e0c0e (2026-04-11) replaced typed import with a raw table dump/restore (version 2.0, additive: missing rows are inserted, existing keys kept) and removed the `/api/v1/api/v1/backup/*` routes. The tests still sent typed (version 1.0) payloads. Bug found: a typed 1.x backup (typed partial export, or any full export from before 1b4e0c0e) "imported" nothing but answered 操作成功 / UI 「导入成功：{}」 | tests rewritten to the 2.0 format: typed + full export work and the duplicate route is 404; import and restore put a deleted `vite_config` row back with the exported value (exactly 1 row restored); forward ports: typed export still lists forwardPorts, full-export restore brings both ports back and keeps a newer row. Product fix below + new subtest (typed backup refused on import and restore, value unchanged) |
| TestOpenMigratesLegacyNodeDualStackColumns / …VeryLegacy… | stale fixture | the legacy CREATE TABLE fixtures were indented with Go tabs; glebarez/sqlite's DDL parser counts `\t` as a quote character, so its AlterColumn table rebuild copied only half the columns → `NOT NULL constraint failed: node__temp.name`. No released schema used tabs (springboot `schema.sql` and go-backend `sqlite/sql/schema.sql` used two spaces) — with the real format the migration works | fixtures written like the historic schema.sql (two-space columns); both tests now also check the legacy node row keeps name/secret/server_ip/port/interface/version/http/socks/status |
| TestServiceMonitorResults / non-existent monitor | stale | upstream bdbbd689 (2026-05-25, per-node monitor permissions) loads the monitor first and answers 监控不存在 for an unknown id, like update and run | expects code -1 「监控不存在」 and no data |
| TestForwardCreateRollbackWhenLimiterDispatchFailsContract | environment (race) | `websocket.Dial` returns when the upgrade response is written, but the panel registers the node session and marks it online after that; a request sent in the gap sees the node offline → 「节点不在线，已跳过下发」 and code 0. Node rows were inserted with status 1, so the existing `waitNodeStatus(…, 1)` calls were no-ops | the 4 mock-node helpers now wait for a ping/pong round trip before returning — the panel answers pings only from the read loop it starts after registration |

### Product-code change (goes out with the next release)

- `go-backend/internal/http/handler/handler.go` `backupImport`: a backup whose `version` is 1.x (typed format) is refused
  with 「这是旧版（1.x）按类型导出的备份，无法导入；当前只支持全量导出的 2.0 备份，请重新导出后再导入」 instead of
  returning success after importing nothing. The version check now runs before the pre-import automatic full export,
  so a rejected file no longer costs a full-database export. 2.0 imports are unchanged.
  Visible only when someone imports an old typed file (error toast instead of a false 「导入成功：{}」).

### Not changed (noted)

- Raw import is additive (existing rows win). Checked with a throwaway test: on SQLite a row whose key already exists
  is skipped and the missing rows are inserted; on PostgreSQL the first such row aborts the transaction and the whole
  import rolls back (`commit unexpectedly resulted in rollback`, nothing imported, UI shows 导入失败). Prod runs SQLite;
  the backup contract tests always use SQLite. Pre-existing, left for the user to decide.
- CI (`ci-build.yml`) only builds go-backend and runs one PostgreSQL contract test, which is why these failures stayed
  unnoticed; adding a `go test ./...` job would keep the suite green (not done here — workflow change not requested).
- `internal/http/handler/dual_stack_test.go` and `node_extra_ips_test.go` are not gofmt-clean on the base (untouched).
