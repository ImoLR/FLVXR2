# Plan 176 — release-rule-row-tree: ship plan 175 as `3.0.27-fork.100` + release-order hotfix `3.0.27-fork.101` (panel only)

User approval 2026-10-31 TW (session f3025275): 「可以 发布吧 就选择A那个挺好看的」 — variant A (tree line, the built default);
other open choices keep the worker defaults (24px phone indent, one remembered open group page-wide, Clash/Surge blocks shown).

Branch `maintenance/3.0.27-fork.100-rule-row-tree` from tag `3.0.27-fork.99` + fast-forward of
`origin/feature/3.0.27-rule-row-tree` (16436c43 + 41d01eed) + fork.99 plan-doc commit b2f40f8a. Panel only: no backend
behaviour change, no DB change, no agent OTA. Run dir `/root/flvx-workers/runs/release-rule-row-tree`.

## Checklist
- [x] 1. Branch + merge (fast-forward, no conflicts)
- [x] 2. First three-digit fork number audit (fork.100 > fork.99 everywhere) + regression tests
- [x] 3. Gates: go build, backend tests SQLite + PostgreSQL (compare with fork.99), frontend tsc / build / lint (no new errors)
- [x] 4. Pre-release prod snapshot (nodes, forward_port, per-node runtime objects, backend RSS/CPU)
- [x] 5. Push, CI green, tag `3.0.27-fork.100`, Images green, release validated (Latest, same 10 assets, pins, sha256, arches)
- [x] 6. Panel backup + rollback dir, upgrade `/opt/flvx-svc`, health + version.json, prune backups (keep 2)
- [x] 7. After: nodes online as before, forward_port + per-node runtime objects identical
- [x] 7b. Found at fork.100 on prod: panel says latest = fork.99 (GitHub list order ≠ version order) → fix + tests
- [x] 7c. Gates again, branch `maintenance/3.0.27-fork.101-release-order`, CI, tag `3.0.27-fork.101`, release validated
- [x] 7d. Backup + upgrade to fork.101, prune, state diff, check-updates shows fork.101 current / latest
- [x] 8. Prod read-only UI checks 390 + 1440 (screenshots in run dir), sidebar update entry shows the running fork as current
- [x] 9. Resources: backend RSS/CPU before/after, 0 new non-request log lines in 10 min
- [x] 10. Plan complete commit + summary

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

### fork.100 release + upgrade (steps 4–7)
- Pre snapshot: 30/30 online, forward_port 38, 42 tunnels, 235 runtime objects on 30 nodes, 0 orphans/missing;
  agents 29× fork.98 + node 36 fork.95. Backend 7–12 % CPU, RSS 38.8 MiB, DB 57.3 MB.
- Branch CI 41192707884 green (4 jobs) on 3e3d7049; tag `3.0.27-fork.100` → 3e3d7049; Images 41192835825 green;
  validate-release 13/13 (Latest, not pre-release, 10 assets = fork.99 set, compose/install pins, gost sha256, amd64+arm64).
- Backup `/opt/flvx-svc/rollback/pre-fork100-20261031T104024Z` (quick_check + integrity_check ok); upgraded 18:40 TW,
  healthy in 10 s, version.json `3.0.27-fork.100-mvw9f8bs`. Prune: pre-fork98 dir/tags + fork.97 images removed.
- State diff pre → post: forward_port 38 = 38 identical, forwards identical, 30 → 30 online, runtime objects identical.

### Release-order bug (step 7b) — found on prod right after fork.100
- `system/check-updates` + `panel/upgrade/check` on fork.100 answered `latestVersion 3.0.27-fork.99`. GitHub's
  `GET /releases` is not version-ordered: `fork.99, fork.100, fork.98, …, fork.16, fork.9, fork.8, fork.15, …`.
  The backend took the first (channel-matching) entry as "latest" (`resolveLatestReleaseFrom`, `systemCheckUpdates`).
- Effect without a fix: fork.100 shows no bogus update (100 > 99 numerically) but reports fork.99 as newest, would never
  offer fork.101+, and 「更新」 without an explicit version would resolve to fork.99 (a downgrade).
- Fix: `sortReleasesNewestFirst` (total order: numeric base, then numeric fork revision) applied before picking the latest
  and before building the check-updates release list. Tests with the real GitHub order fail on the old code and pass now.
- Gates on the fix: `go build` ok, vet output identical to fork.99/100; `go test ./...` SQLite 19/19 + PostgreSQL 19/19.
  No frontend change (frontend gates from step 3 still apply).

### fork.101 release + upgrade (steps 7c–7d)
- Branch `maintenance/3.0.27-fork.101-release-order` (fork.100 head + d50122a1). CI 41195267924 green (4 jobs);
  tag `3.0.27-fork.101` → d50122a1; Images 41195414517 green; validate-release 13/13 (same 10 assets as fork.100).
  GitHub still lists `fork.99, fork.101, fork.100, …` → the fix is needed.
- Backup `/opt/flvx-svc/rollback/pre-fork101-20261031T111928Z` (quick/integrity ok); upgraded 19:19 TW, healthy in 9 s,
  version.json `3.0.27-fork.101-mvwas9hz`. Prune kept pre-fork101 + pre-fork100 (removed pre-fork99 dir/tags, fork.98 images).
- Version endpoints on prod: `system/check-updates`, `panel/upgrade/check`, `system/version` → current = latest =
  `3.0.27-fork.101`, hasUpdate false; release list 101, 100, 99, 98.
- State pre-fork100 → post-fork101: forward_port 38 identical, forwards identical, 30 → 30 online, per-node runtime objects
  identical (235), agents unchanged (29× fork.98, node 36 fork.95). No OTA, no DB change.

### Prod UI checks (step 8) — `runs/release-rule-row-tree/prodcheck100.cjs`, 41/41
- Read-only: API whitelist (only list/get/links/check-updates style reads reach prod; 42 other calls answered locally,
  e.g. `config/update-single`, `package/store-status`); admin JWT + user 8 JWT minted in memory; 0 page errors.
- 1440 + 390: admin own groups collapsed; pick user @jiuwei → collapsed (重置 shown); + keyword 「SSH」 → both matching groups
  open; 重置 → own rules, collapsed, no filter badge; open group: tree items = rules, last elbow rounded (7px), header stub,
  sticky header, footer 「收起 · 隧道名」, accordion; rule 156 复制链接 → 3 cards, all purple 「经规则转发」 (admin and user 8);
  user 8 落地页 → 链接 → 直连 + 3× 经规则转发; no page-level horizontal scroll on 规则/主页/隧道/节点/落地/用户/设置;
  sidebar 「当前版本 3.0.27-fork.101」 without the red update dot. Screenshots: `runs/release-rule-row-tree/shots/`.
- Phone groups have one extra `flvx-rule-tree-pass` segment (line passing the 「本组全选」 row) — by design.

### Resources (step 9)
- Backend before (fork.99): 7–12 % CPU (Go tests running on the host), RSS 38.8 MiB; fork.100: 2.5–5.5 %, RSS 39.9 MiB;
  fork.101 after the browser checks: 2–6 %, RSS 46.7 MiB. DB 57,335,808 bytes unchanged.
- Logs: per upgrade only the 30 startup `redeploy: node N done (diff …)` reconnect lines + the existing periodic
  `node_metric maintenance` line; 0 other non-request lines.
- 10-min window after fork.101 (11:21–11:30Z): 7 non-request lines, all the existing 10-min nftables domain refresh of
  rule 98 (`[nftables-dns] forward 98 域名IP已更新`, same as fork.99) → 0 new lines. Backend 0 restarts, 30/30 online.

**Status: DONE 2026-10-31 19:31 TW — prod runs `3.0.27-fork.101` (panel only; fork.100 = plan 175, fork.101 = release-order fix).**
Rollback: `/opt/flvx-svc/rollback/pre-fork101-20261031T111928Z/rollback.sh` (→ fork.100) or
`pre-fork100-20261031T104024Z/rollback.sh` (→ fork.99); no `--restore-db` needed.
