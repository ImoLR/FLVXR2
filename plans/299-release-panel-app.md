# 299 — release plan 297 (im.gamer App 利群汇聚 = web + 手机本地) as fork.164 + App prerelease + 利群 forwards → panel landings

Task `release-panel-app` (Claude xhigh, coordinator 253904dd, 11-23 TW). User approved the 297 screens 11-23 ~20:50 TW
(「你先发布看看吧」), asked for a logic review first, and asked to switch the 7 利群 forwards from 手动落地 to FLVX native
panel landings (never shared mmwx landing servers). Q2 (App 详细设置 → 订阅页) kept as built; Q3 (CF challenge allowlist)
not done (open).

Base: tag `3.0.27-fork.163` (611bc087) + merge `origin/feature/3.0.27-panel-app` @ 5a61c650 (297, built on fork.162).
Branch `maintenance/3.0.27-fork.164-panel-app`, worktree `/root/projects/flvxx/flvxx-rel299`.
Panel only (no go-gost change → no OTA). DB: + `user_subscription.entry_mode` (main|local, default main).

## Checklist
- [x] Worktree from fork.163, merge feature/3.0.27-panel-app (clean merge, no conflicts)
- [x] Logic review of the whole 297 diff (backend + frontend + bridge doc + App); findings below
- [x] Fix real bugs with tests
- [x] Tests: go vet, go test (SQLite + PG DSN), npm build + lint; compare failures with fork.163
- [ ] Push branch, tag 3.0.27-fork.164, CI + Images + 10 assets, Latest
- [ ] Rollback check on a migrated prod-DB copy (fork.164 migrate → fork.163 starts clean)
- [ ] Prod backup + rollback script, prune backups, swap
- [ ] After swap: nodes, forwards + 利群 tables identical, 4 利群入口 states, log 15 min, 5 busiest forwards diagnose
- [ ] 279 / 281 reachable on all online 利群入口 (expected change)
- [ ] Inventory 利群 forwards (owner, manual target, real machine/port)
- [ ] Backup affected rows + undo script; switch forwards to native panel landings via admin API
- [ ] Verify changed forwards diagnose OK; admin 利群汇聚 sub lists nodes (主入口 address); temp 手机本地 sub → 127.0.0.1 (deleted)
- [ ] App private prerelease (ImoLR/leikwan-local-bridge)
- [ ] Cleanup (worktrees, lab DB copies, build caches), ledger rows 297 + 299, memory

## Logic review findings
Backend (handler / repo / model):
- OK — 手机本地 is admin-only everywhere: `leikwan-rule/link {local}` (role check), `leikwan-rule/sub {entry:local}`
  (role 0 **and** own subscriptions only), `sub/create|update {entryMode:local}` (subOwner = the caller, admin check),
  `leikwan/grant/preview {local}` (admin route group), `leikwan-rule/list {phone}` (admin only, own rules only). Covered
  by the 297 contract test (user calls refused).
- OK — 「只开主入口」 exemption keys off the rule owner (`g.admins[x.UserID]`, role_id 0), loaded only when the lock is
  on; the only consumer is the hosts' port block list. Normal users' rules on the same entries stay locked (test).
  Prod: one admin account (imolr) owns all 7 利群 forwards.
- OK — `user_subscription.entry_mode` NOT NULL DEFAULT 'main'; GORM skips the zero value on create → 'main'; every
  existing row → 'main'; `EntryLocal()` is `== "local"` so '' also behaves as main; UpdateSubscription writes named
  columns only (entry_mode untouched). Render cache key gets `|lk=local`; convert / editor change purge the sub.
- OK — a 手机本地 pull never ends the 换主入口 grace; 换主入口 to 手机本地 is App-side only (grant-dialog `savePhone`
  only re-saves the grant with the unchanged `mainEntryNodeId` when 备线 changed, then bridge `setEntry`).
- OK — fork.162 grace / switch flows untouched (grant/save diff = none in 297).
- OK — limits (流量 / 规则数 / 连接数 / 速率): no 297 change touches them.
- Test gap → **added** (contract test, sqlite + postgres): local sub keeps 127.0.0.1 after 换主入口; deleting a local
  sub (pull 404, App pick falls back to the next newest local sub, browser pick = 利群汇聚 sub); grant 停用 → no 利群
  nodes in the local nor the main sub.
- **Bug fixed (pre-existing since plan 272/276, found by the new test):** grant/save 停用 / 启用 did not purge the
  owner's rendered subscriptions → the 60 s render cache kept serving the 利群 nodes after 停用. Now status changes
  purge like 换主入口 (`leikwan_line.go` grant save).
Frontend:
- OK — every 手机本地 piece is behind `isAppMode()` (injected `window.panelApp`, never the UA) and the admin page;
  user-page untouched. Browser shows only the 「手机本地」 badge on an existing local sub (approved design).
- **Fixed (web side of App risk):** the panel's service worker serves the cached shell offline, so the App's native
  fallback page (手机本地 status / 断开 / 重连) never showed. In App mode `admin-main.tsx` now registers no service
  worker and unregisters an old one. Browsers unchanged.
App (ImoLR/leikwan-local-bridge feature/panel-app, review of 37eceda..261dbfd):
- OK — auto-connect default off; only with 手机本地 as entry; battery gate only for auto; entry stored on the phone
  (default server, bad values refused); bridge origin-locked, no secrets to JS, debug-only WebView debugging; other two
  flavors unchanged; bridge doc byte-identical in both repos.
- Bugs → fixed in 1.0.9-panel vc3: (1) fallback page overwrote 「换面板地址」 (address could not be changed while the
  panel was down); (2) no auto-connect retry on resume after the battery dialog (floating dialog = onPause only);
  (3) credentials dialog answered the wrong pending request; (4) WebView kept running/polling the panel in the
  background (resource rule) → paused in onStop; (5) connect / setEntry reported success when the service start/stop
  failed.
- Left open (nits): v0 testers who explicitly turned auto-connect on keep it on after upgrade; `battery.hint` never sent.

## Tests (11-23 TW, branch = fork.163 + 297 + 299 fixes)
- `go vet ./...` clean. `GOMAXPROCS=2 go test -p 1 ./... -count=1` with `FLVX_POSTGRES_TEST_DSN` (throwaway postgres:18
  container): 29/29 packages ok (first run: repo + contract failed only with "No space left on device" — my test PG sat
  on a 600 MB tmpfs; rerun on disk = ok). `TestForwardWarmTagContract` passed in this run.
- New contract cases (sqlite + postgres) fail without the grant-status purge fix and pass with it.
- `npm install --legacy-peer-deps` + `npm run build` (tsc + vite) ok; eslint on every 297/299 file clean.

## Forward switch
(filled in during step 6)
