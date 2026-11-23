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
- [x] Push branch, tag 3.0.27-fork.164, CI + Images + 10 assets, Latest
- [x] Rollback check on a migrated prod-DB copy (fork.164 migrate → fork.163 starts clean)
- [x] Prod backup + rollback script, prune backups, swap
- [x] After swap: nodes, forwards + 利群 tables identical, 4 利群入口 states, log 15 min, 5 busiest forwards diagnose
- [x] 279 / 281 reachable on all online 利群入口 (expected change)
- [x] Inventory 利群 forwards (owner, manual target, real machine/port)
- [x] Backup affected rows + undo script; switch forwards to native panel landings via admin API
- [x] Verify changed forwards diagnose OK; admin 利群汇聚 sub lists nodes (主入口 address); temp 手机本地 sub → 127.0.0.1 (deleted)
- [x] App private prerelease (ImoLR/leikwan-local-bridge)
- [x] Cleanup (worktrees, lab DB copies, build caches), ledger rows 297 + 299, memory

## Cleanup
Test PG container removed, temp dirs removed, rollback rehearsal copy removed (script), 297 build worktree flvxx-294 and
this worktree removed (branches pushed), Android build env removed by the App build. Ledger rows 297 + 298 → 已发布,
next free fork.165 / plan 300. Memory `fork-release-and-deploy` + `panel-app-leikwan-align` updated.

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

## Release (11-23 TW)
- Tag `3.0.27-fork.164` → 08e02ed2 (branch maintenance/3.0.27-fork.164-panel-app). CI Build Check 45183318426 +
  Build and Push Images 45183322105 green; Latest, not prerelease, 10 assets, compose images :3.0.27-fork.164,
  PINNED_VERSION / REPO ok, gost-amd64 sha256 ok.
- Rollback rehearsal on a prod-DB copy (`--network none`): fork.164 migrated (8 subs → entry_mode main), fork.163 then
  started healthy on it, API reads ok, no log errors.
- Prod swap 21:37:53–21:38:00 TW. Rollback `/opt/flvx-svc/rollback/pre-fork164-20261123T133753Z/rollback.sh` (→ fork.163).
  Pruned to pre-fork163 + pre-fork164.
- After: 29/31 online as before (火山广州 / 火山上海 offline as before), 65/65 forwards, forward_port + 利群 tables
  identical, 4 利群入口 states identical; 5 busiest forwards diagnose 3/3 before and after, RTT within noise.
- Expected change verified: 279 (:10019) and 281 (:10016) were blocked on 阿里广州 before, open after
  (火山上海 / 火山广州 offline → they get it when they reconnect).
- 15 min after the swap (21:53 TW): panel log clean (only 火山上海's known connect-then-45 s-pong-timeout), state vs
  before differs only by the step-6 switch (forward 595 target, 利群 rules 2/4/6, new landing node 219), all 8 subs
  entry_mode main, backend RSS 74 → 66 MiB. 備線·264 (595) diagnose ok after its retarget.

## Forward switch (step 6, 11-23 21:41–21:45 TW, admin API only)
Inventory: all 7 利群 forwards belong to imolr (the only admin); all 7 were 手动落地.
| Forward | Manual target (what it really is) | Native FLVX landing? | Done |
|---|---|---|---|
| 280 Boil Hinet 马年 :12311 (广台) | boilhinet2:12311 = FLVX native server 马年, node 215 「SS2022 12311（備線 / 救命版）」 | yes, same host:port + same key | relinked → 215 (no vendor write, ETag same, filter key fp same, client link unchanged) |
| 282 Boil Hinet 马年 :12311 (福台) | same | yes | relinked → 215 (same as 280) |
| 264 Bestvm JP B :60061 (沪日) | 188.253.118.132:60061 = mmw-agent (mmwx) inbound | native server 13 Bestvm JP B | new node 219 「利群264 沪日」 ss2022 :31004 for imolr; vendor target PATCH → :31004; linked 備線·264 (595) followed; renamed 「264 Bestvm JP B :31004」. **New key → admin's old 264 client config must be replaced by the new link / sub.** |
| 263 Boil HKT 99u :10016 | boilhkt99u:10016 (mmwx) | only shared server 11 (「利群263/540 99U」 are on it) | left (never shared mmwx) |
| 540 Boil HKT 99u :10019 | boilhkt99u:10019 (mmwx) | only shared server 11 | left |
| 279 AWS HK :10022 | 43.199.143.188:10022 (mmwx on the prod host) | no landing server on AWS HK | left |
| 281 Fachost seednet 黑五 :10015 | facseednet:10015 (mmwx) | no landing server on Fachost seednet | left |
Checks: 利群 diagnose 264 / 280 / 282 ok on 百度云广州 + 阿里广州 (only the offline 火山 entries fail); real SS2022 proxy
test through 106.13.17.23:10015 / 10020 / 10017 (xray client, temp config deleted) → 204, exit IPs Bestvm JP B /
马年 Hinet. Admin 利群汇聚 sub (id 27): 3 利群 nodes on 106.13.17.23; temp 手机本地 sub → 127.0.0.1:10015/10020/10017,
title 「利群汇聚 2 · 手机本地」, deleted afterwards.
Backup + undo: runs/release-panel-app/work/switch-backup.json + `python3 lkswitch.py undo` (manual targets back, vendor
264 → :60061, node 219 deleted).

## App private prerelease
ImoLR/leikwan-local-bridge `v1.0.9-panel` (prerelease, not Latest) → feature/panel-app 2ca415f: APK
imgamer-panel-1.0.9-panel-vc3-release.apk sha256 83572970…71ec1, cert 6a78…0367, scan 0 hits. Panel unit tests 26/26
(+5 new); pub 492 / lab 590 ok; panel full suite 14 failures in shared AppIconTest / BridgeNotificationTest = same on
261dbfd (pre-existing: they assert pub/lab launcher + 停止 text).
