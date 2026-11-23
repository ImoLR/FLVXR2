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
- [ ] 3. Backend: admin not locked by 只开主入口 (entryLocked + rule view `ownerAdmin`); tests
- [ ] 4. Backend: `user_subscription.entry_mode` (AutoMigrate add-column, default main); local render = 127.0.0.1;
      drop `?lk=local`; 利群 sub endpoint: entry / id / action convert|create; list views carry entryMode; tests
- [ ] 5. Backend: keep link `local`, grant/preview `local`, rule/list `phone` (admin only); contract tests ported
- [ ] 6. Frontend: 入口机 — 手机本地 row first (App), drawer = modules + phone parts; browser = fork.162
- [ ] 7. Frontend: 换主入口 — 手机本地 option (App, admin own grant), preview, phone-only save, server pick clears it
- [ ] 8. Frontend: 授权 card 主入口 = 手机本地 + connect panel (state, 连接/断开, three tips, notes, auto switch)
- [ ] 9. Frontend: forward rows follow the phone (127.0.0.1 / 「手机本地未连接」 / chips), 复制链接
- [ ] 10. Frontend: 订阅 — entry badge 「手机本地」 (生成订阅 dialog + 订阅 page), App: new / convert; web: convert back
- [ ] 11. Frontend: AllEntriesPill for admin-owned rules (所有入口可用, static)
- [ ] 12. Checks: tsc, eslint, `npm run build`; `go test` touched packages + contract tests (vs base)
- [ ] 13. Android: entry choice (getEntry / setEntry + state), auto-connect default OFF (battery gate for auto only),
      manual connect with battery restricted, setEntry(server) disconnects; version consistent; tests; signed APK;
      check-apk-private; push branch (no release)
- [ ] 14. Bridge contract `panel-app-bridge.md` updated, identical in both repos
- [ ] 15. Lab (prod-DB copy, own ports): fork.162 vs branch browser screenshots identical (desktop + 390 H5, admin +
      user); web vs App gallery /root/mmwx-preview/panel-app-v1/
- [ ] 16. Push branch (force), CI Build Check green; summary.md + final.md; lab copies deleted

## Notes / decisions
