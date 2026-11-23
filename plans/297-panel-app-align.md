# 297 — 面板 App 第二轮（修正版）：利群汇聚 App = 网页 + 手机本地 — BUILD + PREVIEW ONLY

Worker `panel-app-local` (Claude xhigh, coordinator 0bf8e482, 11-23 TW). Brief: /root/flvx-workers/runs/panel-app-local/brief.md
(replaces the stopped `panel-app-align` brief). Previous round: plan 294 (`panel-app-leikwan-v0`, archived as branch
`archive/3.0.27-panel-app-v0` = 1136c6e4).

Hard limits: no FLVX tag/release, no prod upgrade, no merge to main, no OTA; no Android release / public upload /
zc-update; no real LeiKwan vendor connect from dev box / emulator / CI; prod panel API read-only at most; pub 重仓利群 /
lab 利群原版 behaviour and versions unchanged.

## User decisions (11-23, final for this round)
- App 利群汇聚 == web (fork.162); the ONLY difference is one extra entry 「手机本地」 (App only, admin only). Browser
  output must stay pixel-identical to fork.162 (no web 「本地」 option, name stays 手机本地).
- 入口机 tab = fork.162 (tap / ＋ = details drawer, no selecting). App: 手机本地 row first, its drawer = modules +
  phone parts.
- Switching entry only in 授权 → 换主入口; in the App the options gain 手机本地 (phone-only choice, stored in the App).
- Admin is not locked by 「只开主入口」 (any / several entries); normal users unchanged.
- Subscription follows the entry it was made / updated under: column `user_subscription.entry_mode` main | local;
  local = 利群 nodes on 127.0.0.1 + 「手机本地」 badge (App + web 订阅 list). No 手机专用订阅 / `?lk=local`.
- Manual connect by default; 「打开 App 自动连接」 per phone, default OFF; connect panel + three tip blocks.

## Checklist
- [x] 1. Read context (briefs, round-1 final, memories, fork.162 plan 295 code, WIP of the stopped worker)
- [x] 2. Branch on fork.162 (WIP commit 514bc1a3 = stopped worker's port onto fork.162)
- [x] 3. Backend: admin not locked by 只开主入口 (entryLocked + rule view `ownerAdmin`); tests
- [x] 4. Backend: `user_subscription.entry_mode` (AutoMigrate add-column, default main); local render = 127.0.0.1;
      drop `?lk=local`; 利群 sub endpoint: entry / id / action convert|create; list views carry entryMode; tests
- [x] 5. Backend: keep link `local`, grant/preview `local`, rule/list `phone` (admin only); contract tests ported
- [x] 6. Frontend: 入口机 — 手机本地 row first (App), drawer = modules + phone parts; browser = fork.162
- [x] 7. Frontend: 换主入口 — 手机本地 option (App, admin own grant), preview, phone-only save, server pick clears it
- [x] 8. Frontend: 授权 card 主入口 = 手机本地 + connect panel (state, 连接/断开, three tips, notes, auto switch)
- [x] 9. Frontend: forward rows follow the phone (127.0.0.1 / 「手机本地未连接」 / chips), 复制链接
- [x] 10. Frontend: 订阅 — entry badge 「手机本地」 (生成订阅 dialog + 订阅 page), App: new / convert; web: convert back
- [x] 11. Frontend: AllEntriesPill for admin-owned rules (所有入口可用, static)
- [ ] 12. Checks: tsc, eslint, `npm run build`; `go test` touched packages + contract tests (vs base)
- [ ] 13. Android: entry choice (getEntry / setEntry + state), auto-connect default OFF (battery gate for auto only),
      manual connect with battery restricted, setEntry(server) disconnects; version consistent; tests; signed APK;
      check-apk-private; push branch (no release)
- [x] 14. Bridge contract `panel-app-bridge.md` updated, identical in both repos
- [ ] 15. Lab (prod-DB copy, own ports): fork.162 vs branch browser screenshots identical (desktop + 390 H5, admin +
      user); web vs App gallery /root/mmwx-preview/panel-app-v1/
- [ ] 16. Push branch (force), CI Build Check green; summary.md + final.md; lab copies deleted

## Notes / decisions
- Admin unlock: `lkGen.admins` (role 0) → `entryLocked` false; rule / grant views carry `ownerAdmin` → static amber
  「所有入口可用」 pill, 换主入口 note 「管理员的转发不受「只开主入口」限制」 (browser too: the behaviour changed).
- Sub entry: `user_subscription.entry_mode varchar(10) not null default 'main'` (AutoMigrate add-column, SQLite + PG).
  Render: local → subLoad ctx 127.0.0.1, cache key `|lk=local`, no grace end on pull. Client title (profile-title, file
  name, browser page) = name + 「 · 手机本地」 derived from the mode (addendum 11-23); the editable name never carries it.
- 生成订阅 API `leikwan-rule/sub {entry?, id?, action?}`: entry local (admin, own) picks the newest local sub;
  create = new 「利群汇聚 N」 local sub; convert = sub follows `entry`. Browser dialog = fork.162 + badge + 「改成跟主入口」
  only for a local sub. 订阅 editor: 「利群入口」 row (跟主入口 / 手机本地) for a local sub, or in the App for an admin;
  `sub/update {entryMode}` (absent = keep). App path allow-list gains /sub (reached from 「详细设置」 only, nav untouched).
- Android 1.0.8-panel vc2: `entry` pref (server default), auto-connect default off, manual connect without battery
  gate, setEntry(local) + auto on → autoConnect(); setEntry(server) → stop; coming back to the App retries a blocked
  auto-connect. Build env /root/.cache/leikwan-android-build recreated (setup.sh) — delete again at the end.
