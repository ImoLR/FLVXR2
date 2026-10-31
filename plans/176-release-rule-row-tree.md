# Plan 176 — release-rule-row-tree: ship plan 175 as `3.0.27-fork.100` (panel only)

User approval 2026-10-31 TW (session f3025275): 「可以 发布吧 就选择A那个挺好看的」 — variant A (tree line, the built default);
other open choices keep the worker defaults (24px phone indent, one remembered open group page-wide, Clash/Surge blocks shown).

Branch `maintenance/3.0.27-fork.100-rule-row-tree` from tag `3.0.27-fork.99` + fast-forward of
`origin/feature/3.0.27-rule-row-tree` (16436c43 + 41d01eed) + fork.99 plan-doc commit b2f40f8a. Panel only: no backend
behaviour change, no DB change, no agent OTA. Run dir `/root/flvx-workers/runs/release-rule-row-tree`.

## Checklist
- [x] 1. Branch + merge (fast-forward, no conflicts)
- [x] 2. First three-digit fork number audit (fork.100 > fork.99 everywhere) + regression tests
- [x] 3. Gates: go build, backend tests SQLite + PostgreSQL (compare with fork.99), frontend tsc / build / lint (no new errors)
- [ ] 4. Pre-release prod snapshot (nodes, forward_port, per-node runtime objects, backend RSS/CPU)
- [ ] 5. Push, CI green, tag `3.0.27-fork.100`, Images green, release validated (Latest, same 10 assets, pins, sha256, arches)
- [ ] 6. Panel backup + rollback dir, upgrade `/opt/flvx-svc`, health + version.json, prune backups (keep 2)
- [ ] 7. After: nodes online as before, forward_port + per-node runtime objects identical
- [ ] 8. Prod read-only UI checks 390 + 1440 (screenshots in run dir), sidebar update entry shows fork.100 as current
- [ ] 9. Resources: backend RSS/CPU before/after, 0 new non-request log lines in 10 min
- [ ] 10. Plan complete commit + summary

## Notes

### fork.100 version audit (step 2) — nothing compares versions as strings
- Backend `upgrade.go` `compareVersions` / `splitForkVersion`: regex `^(\d+(?:\.\d+)+)-fork\.(\d+)$`, revision parsed as int →
  numeric. Used by `system/check-updates` + `panel/upgrade/check` (`HasUpdate`), so fork.99 → offers fork.100, fork.100 →
  no update. `stableVersionPattern` `-fork\.\d+` (any digits) → fork.100 is the stable channel. `quotaAgentSupport`
  (`revision >= 13`) numeric. Latest-tag pick = first non-draft release in GitHub API order (newest first), not a sort.
  Release list sorts by `publishedAt`. Panel-OTA "no downgrade" = button disabled unless `HasUpdate` (numeric).
- Frontend `utils/version-update.ts` `compareVersions` (sidebar update entry, version footer, node OTA picker in
  `node.tsx`): fork regex + `Number.parseInt` → numeric; ran the real file with Node 22 type stripping:
  99 vs 100 → -1 / hasUpdate true; 100 vs 99 → 1 / false; 100 vs 100 → 0 / false.
- `version.json` / build freshness: build id `3.0.27-fork.100-<ts>` checked only for equality + `[0-9A-Za-z._-]{1,128}`.
  SW kill switch `stale-client-reset.js` has no version logic.
- Agent (go-gost): no version comparisons (only comments mention fork numbers). Panel→agent version = exact equality.
- CI: `docker-build.yml` triggers on tags `[0-9]*`, uses `github.ref_name` verbatim; `PINNED_VERSION` = sed of the full tag;
  prerelease only for alpha/beta/rc. Install scripts take `PINNED_VERSION` verbatim.
- Our tooling: `prune-backups.sh` sorts rollback dirs by UTC timestamp, not fork number.
- Added regression cases to `upgrade_release_channel_test.go` (fork.99 < fork.100, update offered 99→100 only, stable channel,
  quota support).

### Gates (step 3)
- `go build ./...` rc 0; `go vet` = the same 2 pre-existing `leikwan/testapi` unkeyed-field notes as fork.99 (log identical).
- `go test ./...` SQLite 19/19 packages ok, PostgreSQL 19/19 ok (fork.99 baseline: 19/19 both), 0 failing tests.
- Frontend: `tsc --noEmit` 0 errors; `vite build` ok; eslint 94 errors / 3723 warnings (fork.99: 94 / 3730) → no new errors.
