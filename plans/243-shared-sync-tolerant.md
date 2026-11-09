# 243 — shared-sync-tolerant: one bad 妙妙屋 share no longer blocks all shared sync; vanished share → revoked

Base: prod tag `3.0.27-fork.127` (9318100f). Branch `feature/3.0.27-shared-sync-tolerant`. Release: panel only → `3.0.27-fork.128`, no agent OTA.

## Incident (11-09 20:04 TW)
mmwx Custom deleted server 7 (Breadcloud US 年付, FLVX shared landing_server 2) but kept its `mmwxc_flvx_shares` row, so
`GET /shares` sent a hostless item. `SyncSharedLandings` rejected the WHOLE list (`妙妙屋共享地址缺失`) → the 4 business
shares (Boil Hinet 马年, Dmit T1, PQS TW, Boil Hinet 158) stopped syncing for 13 min, and the integration-level error made
every shared card show 同步异常 / offline. Second gap: a share that disappears (or whose status poll answers 404
`not_found`) stayed 「可用」 forever.

## Design
- **Per-item tolerance** (`repo.SyncSharedLandingList`): each item validated on its own (id, dup id, ports, limits, status,
  protocols, hosts). Invalid items are skipped; their row keeps its last good config and gets the item's error in
  `shared_sync_error`; valid items apply in the same transaction. Envelope / non-200 / undecodable list still fails the
  round (unchanged).
- **Vanish → revoked** through the SAME branch as an explicit `status=revoked`: the vanished share is fed into the apply
  loop as its stored caps with `status=revoked` (no second revoke copy).
  - In-memory tracker (`repo.SharedVanishTracker`, no new column, resets on restart = safe direction).
  - Counts only after a fully successful list fetch: missing in ≥3 consecutive successful list rounds spanning ≥3 min,
    or status poll 404 `not_found` ≥3 in a row spanning ≥3 min (decided at the next successful list round).
  - Mass guard: candidates (missing ∪ not_found-qualified) > half of known non-revoked shares, or empty list while ≥1
    known → nothing revoked, counters frozen, per-server error + admin ⚠ (`sharedHold`).
  - Reappear before threshold / successful status poll → counter reset. Revoked stays terminal (existing rule: a revoked
    share reappearing as active stays revoked).
- **Per-share errors stay per share**: 404 `not_found` / 409 / 422 on a per-server call (status/push/user-limits) no
  longer trips the transport breaker or the integration-level error (that error used to mark every shared card).
  A revoked share answering `not_found` is final: no error, no further polls until restart.
- Logging: one line when an item's problem appears/changes/clears, when a hold starts/ends, on revoke. No per-round spam.
- Requests per round unchanged (reuses list + status polls); revoked+gone shares poll less.

## Tests (local, 11-09)
- `go test ./...` (go-backend, sqlite; PG variants run in CI): all packages ok, 0 failures.
- New: `repository_landing_shared_tolerant_test.go` (each validation rule, dup/bad id/unknown, vanish 1/4 after 3 rounds ≥3 min,
  same path as explicit revoke, explicit hostless revoke, mass 3/4 + empty, exactly-half, not_found streak + reset, reappear
  reset, orphan cleanup no-op) and `handler/landing_shared_tolerant_test.go` (incident end to end: 4 good shares synced, no
  integration error, breaker closed; mass hold → ⚠ field admin-only; revoked + not_found → no error, no more polls).
- Frontend: `tsc --noEmit`, eslint, prettier clean on the 2 changed files.

## Business baseline (pre, 11-09 21:40 TW) — `runs/shared-sync-tolerant/work/`
- 4 shared landings active, no error, synced ≤65 s; integration error empty.
- 45 active rules diagnosed (2 SSH targets skipped): all hops OK except 6/37/69 (non-selected HK exits fail TCP ping to target)
  and 77/95 (UDP-only targets) — baseline, must look the same after.
- Landing links (7 nodes): 6 OK (trace 2/2 + 1 MB); node 168 Dmit T1 is Mieru → checker's sing-box has no Mieru client (not an outage).
- 救命版 44/45 online (阿里深圳 offline), container untouched since 08-15.

## Checklist
- [x] Investigate: sync path, status poll, Custom 404 `not_found` semantics, revoked behaviour, prod state (landing_server 2 row already gone)
- [x] Repo: per-item validation + `SyncSharedLandingList` + vanish tracker + revoke through explicit-revoked branch
- [x] Handler: tracker wiring, not_found counting, scoped per-share errors, item/hold errors re-applied, `sharedHold` view field
- [x] Frontend: add-only ⚠ reason for `sharedHold` in `serverAdminAttention`
- [x] Tests: repo unit + handler tests (each validation rule, vanish 1/4, 3/4, empty, not_found×3, reappear reset, shared path)
- [x] `go test ./...` green (compare failures with base), frontend build
- [x] Commit, push, CI green, ledger row (7f12a2a5; CI 42627222572 green; PG variants of the new tests run locally against a temp postgres:18 — CI PG job only runs one contract test)
- [x] Tag `3.0.27-fork.128`, release assets verified
- [x] Prod backup + prune, upgrade panel, health + business checks (4 shares sync, landing links, rule groups, 救命版)
- [x] Ledger → 已发布, memory, plan complete

## Results (11-09 TW)
- Tag `3.0.27-fork.128` → 7f12a2a5 (annotated). CI 42627222572 green; Images/release 42627910229 green; release Latest,
  10 assets = fork.127 set, compose/install scripts pinned, gost-amd64 sha256 + `-V` fork.128 (validate-release-128.py ALL OK).
- Prod backup `/opt/flvx-svc/rollback/pre-fork128-20261109T135415Z` (quick/integrity ok); pruned → kept pre-fork127 + pre-fork128.
- Panel upgraded 21:54:24 → healthy 21:54:38 TW (frontend build 3.0.27-fork.128-mw9bcqry, 救命版 ro mount ok). No agent OTA.
  No schema change → rollback = image swap `rollback.sh` (`--restore-db` last resort).
- After 2 min: 4 shared landings active, no error, synced ≤21 s; integration error empty; 0 `[shared-landing]` log lines
  (clean list → no-op); 27/27 nodes online; backend 29 MiB RSS, 2.4 % CPU.
- Business post == pre: 45 rules identical hop-for-hop, counts identical, 6 landing links trace 2/2 + 1 MB, Dmit T1 Mieru
  node 168 Helper-reported active (checker has no Mieru client), 救命版 44/45 unchanged (started 08-15, restarts 0).
- landing_server 2 (Breadcloud US 年付) row no longer exists on prod → nothing for the new code to act on.
