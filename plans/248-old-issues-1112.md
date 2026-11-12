# 248 — old issues seen during the fork.129 release (build only)

Base: tag `3.0.27-fork.129` + plan 247 (`feature/3.0.27-warm-dial-quiet` @ d2da306b, fast-forward).
Branch: `fix/3.0.27-old-issues-1112`. Build only: push for CI, no tag / release / prod change.
User 11-12 20:50 TW: 「那几个老问题…安排一个修复吧，还是要解决一下」.

## Checklist
- [x] 0. Branch + worktree, 247 merged, plan doc
- [x] 1. P1 node online/offline race (panel): find path, reproduce in test, fix (stale conn can't mark offline), self-heal, contract tests, same pattern elsewhere
- [x] 2. OTA timeout on slow download (panel): follow real agent progress, no false failure; mirror lists in sync
- [x] 3. Flaky `TestSocketCapSharedByProbeAndPin` (go-gost/x): cause, deterministic, ≥200× -race
- [x] 4. `go vet` RouteRef warnings in go-backend → `go vet ./...` clean
- [x] 5. 4 a11y lint errors in vite-frontend tunnel.tsx → 0 errors, no visible change
- [x] 6. Tests: go-backend `go test ./...` (SQLite + PG contract), go-gost + go-gost/x, `npm run build`
- [x] 7. Ledger row 248 (ready, rides with 247), push branch, CI green
- [x] 8. Summary (Chinese) + cleanup

## Notes

### 1. Node 在线/离线 race (panel)
- Path: `internal/ws/server.go` handleNode. New session registers in `s.nodes` then writes 在线
  (`UpdateNodeOnline`); the old session's close defer removes itself from `s.nodes` (only if still current)
  then writes 离线 (`UpdateNodeStatus 0`) — both writes outside any lock.
- Root cause: SQLite pool has ONE connection (`SetMaxOpenConns(1)`) and Go's database/sql hands a freed
  connection to a RANDOM waiter (`connRequests.TakeRandom`). 11-12 01:59 TW the panel stalled ~45 s
  (SetQuotaGroups / flow upload timeouts, both agents' pong timeout); when it resumed, the old 离线 write
  of Mkcloud 沪日ixp 440 and Bestvm HK 年付 was queued next to the new 在线 write and landed last.
  Side effects while stuck: the 10 s reconnect redeploy timer and the redeploy retries read the stored flag
  (`nodeRuntimeOnline`) and skipped those nodes; `failPendingForNode` of an old session could also fail
  the new session's commands.
- Fix: every node.status write runs under a per-node status lock and re-checks the live session map
  (`internal/ws/node_status.go`): old session writes 离线 only if no newer session exists (and then also
  skips its offline hook); the new session's 在线 waits for the old write and lands after it; browser status
  broadcasts are in the same order. Pending commands are tagged with their session (only the closing
  session's commands fail). `nodeRuntimeOnline` checks the live session first.
- Self-heal: a live session's metric report checks the stored flag ≤ once per 30 s per session (one
  single-row read, async) and writes 在线 if it is 离线; the 2-min cleanup clears 在线 of local nodes with
  no session (60 s grace after a session end; federation nodes untouched).
- Tests: contract `TestNodeStatusStaleOfflineWriteCannotOverrideNewSessionContract` (GORM callback holds
  the old 离线 write until the new session registered — fails 3/3 on the base, passes 20/20 after),
  `TestNodeStatusSelfHealsFromLiveSessionContract` (fails on base), ws unit tests for both heal
  directions, the per-node serialisation and the per-session pending fail.

### 2. OTA false failure (panel)
- 火山广州: `node/upgrade` = synchronous `SendCommand` with a fixed 300 s timeout (= http WriteTimeout =
  nginx `/api/v1/` timeout; browsers via Cloudflare are cut at 100 s anyway). The agent's handler is
  synchronous too (download → checksum → restart script → reply), so a >300 s download = false failure.
- Fix (panel only, agents unchanged): OTA job per node (`internal/http/handler/upgrade_jobs.go`). It keeps
  waiting while UpgradeProgress reports arrive (idle 6 min > one source's 5 min), survives a lost reply /
  a WS reconnect mid-download, ends with the version the agent reports when it connects again (target →
  已升级到 X; old version after 「重启中」 → failed), max 30 min. The request answers within 90 s with the
  result or 「节点仍在升级（…），完成后自动刷新」 (`pending: true`); the end is pushed to the node page as
  upgrade_progress (percent 100) and readable at `POST /api/v1/node/upgrade/status {id}`. Batch uses the
  same jobs (5 at a time). Single-upgrade toast shows the panel's message. Mirror lists untouched.

### 3. Flaky TestSocketCapSharedByProbeAndPin (go-gost/x/landing)
- Cause: the test's countingNet counted a socket as open only from dial return to Close (µs window), so
  `peak ≥ 2` depended on luck: base fails 42/300 with -race (all `peak open sockets = 1`).
- Fix (test only): count from dial start (like a real fd) + a deterministic rendezvous (the first socket
  stays open until the other side's socket is open) + assert probe and pin overlapped. 300× -race and
  600× (-cpu 1,4) pass.

### 4. go vet RouteRef
- `internal/leikwan/testapi/fake.go` unkeyed `leikwan.RouteRef` literals → keyed. `go vet ./...` clean.

### 5. tunnel.tsx a11y (4 errors)
- Click-to-copy tunnel name / remark (table + card): `role="button"`, `tabIndex={0}`, Enter/Space copy —
  the repo's existing pattern (forward.tsx chips). The card title `<h3>` became a `<div data-card-title>`
  with the same classes; globals.css styles `[data-tunnel-card] h3` (14px / weight 800 / colour / dark
  colour / letter-spacing 0), so `[data-card-title]` was added to those selector lists (first lab run
  without it: card titles differed by font weight). eslint: 4 → 0 errors, warnings 1347 → 1343.
- Lab check (never committed; run dir `/root/flvx-workers/runs/old-issues-1112/lab`): paneld on a synthetic
  DB copy (plan 244 lab seed + 4 remarks) in a loopback-only netns, base dist vs new dist, Chromium:
  list/card × desktop/H5 × light/dark, every tunnel group opened in turn → 30/30 full-page screenshots
  byte-identical; mouse click copies the same text, element screenshot after click identical, no focus
  ring (`:focus-visible` false); keyboard Enter / Space copy and do not open the row.

### Tests (11-12)
- go-backend: `go build ./...`, `go vet ./...` clean; `go test ./... -count=1` (SQLite) all ok.
- PostgreSQL variant (`FLVX_POSTGRES_TEST_DSN`, local postgres:18-alpine, fresh DB, `-p 1`):
  internal/store/repo, internal/http/handler, tests/contract all ok. (A first attempt with a 300 MB
  tmpfs container failed with backends OOM-killed — test environment, rerun clean.)
- go-gost: `go vet ./...` clean, `go test ./...` ok; go-gost/x: `go test ./...` ok (26 packages).
- New tests: contract race + self-heal (fail on base), ws node_status (4, -race), handler OTA jobs
  (3 + 6 subtests + HTTP end-to-end through the real WS server, -race).
- vite-frontend: `npm run build` ok; eslint tunnel.tsx 0 errors; node.tsx errors 8 = base 8 (pre-existing).

### Release notes (for the release worker; ships together with 247 from this branch)
- Panel: yes (image swap + restart). DB: no schema change (one new read query only). New admin endpoint
  `POST /api/v1/node/upgrade/status {id}`.
- Agents: 248 has no agent runtime change (go-gost/x change is a test file). 247 needs OTA of the entries
  with 端口转发 rules (warm-pool refill dial failures → debug).
- OTA API behaviour (release scripts): `node/upgrade` now answers within 90 s. `code 0` + `message
  已升级到 X` = the agent reconnected on X (verified). `code 0` + `data.pending: true` = still running
  (slow download) → poll `node/upgrade/status` (state queued/running/succeeded/failed) or the node list
  version. `code -2 升级失败：…` = real failure (agent error, no progress 6 min, back on the old version,
  not back 3 min after 「重启中」). Batch: per-node `pending` field.
- Watch after release: `[ws] node N status self-heal …` log lines (each one = a stale flag fixed; expected
  rare), stored 在线/离线 vs live sessions after the next panel stall / reconnect wave.
- Rollback: image swap back to fork.129 (`rollback.sh` of the release); no DB restore needed.

### Build result
- Branch `fix/3.0.27-old-issues-1112` (247 d2da306b + 248 ac778dce cfb0082a 28bdcdc3 57a33fed bbf5ac85 + docs),
  CI 43238099973 (57a33fed) and 43239164527 (3024e22b) green: backend, PG contract, frontend, agent.
- Ledger row 248 = ready, released together with 247. No tag, no release, no prod change.
- Summary: /root/flvx-workers/runs/old-issues-1112/summary.md
