# 297 — 面板 App 第二轮：利群汇聚 App = 网页 + 手机本地（对齐 fork.162）— BUILD + PREVIEW ONLY

Worker `panel-app-align` (Claude xhigh, coordinator 8af47ee0, 11-23 TW). Brief: /root/flvx-workers/runs/panel-app-align/brief.md.
Previous round: plan 294 (`panel-app-leikwan-v0`, archived as branch `archive/3.0.27-panel-app-v0` = 1136c6e4).

Hard limits: no FLVX tag/release, no prod upgrade, no merge to main, no OTA; no Android release / public upload /
zc-update; no real LeiKwan vendor connect from dev box / emulator / CI; prod panel API read-only at most; pub 重仓利群 /
lab 利群原版 behaviour and versions unchanged.

## User decisions (11-23)
- App 利群汇聚 == web (fork.162); the ONLY difference is one extra entry 「手机本地」. Browser output must stay
  pixel-identical to fork.162.
- Switching entry only in 授权 → 换主入口; in the App the options gain 手机本地 (phone-only choice, stored in the App,
  never written to the server grant).
- Manual connect by default; 「打开 App 自动连接」 per phone, default OFF; connect panel + three tip blocks.
- 手机专用订阅 (`?lk=local`) next to the unchanged normal subscription; switch-back reminder.

## Checklist
- [x] 1. Read context (round-1 final/brief/gallery, memories, fork.162 plan 295 code, Android round-1 code)
- [x] 2. Branch onto fork.162: `feature/3.0.27-panel-app` reset to tag 3.0.27-fork.162, round-1 App-only files ported
      (bridge util, App nav, sub `?lk=`), round-1 tap-to-select / 详情 split / per-entry chips / 「入口：xx」 dropped
- [ ] 3. Backend: keep `local` link param + `?lk=local` sub; drop `entryNodeId` / `?lk=<node>`; phone entry in
      `grant/preview` (`local`) + rule list `phoneLimits` (same capability schema); contract tests
- [ ] 4. Frontend: bridge v2 bits (PhoneState.entry, getEntry / setEntry, auto-connect default off)
- [ ] 5. Frontend: 入口机 list — 手机本地 row first (App only), 「＋」 drawer with modules + phone-only parts
- [ ] 6. Frontend: 换主入口 dialog — 手机本地 option (App, own grant only), preview for 手机本地, phone-only save,
      server pick = clear phone choice (+ normal fork.162 flow when different), switch-back reminder
- [ ] 7. Frontend: 授权 card 主入口 = 手机本地 + connect panel (state, 连接/断开, three tip blocks, notes, auto switch)
- [ ] 8. Frontend: forward rows follow the phone (127.0.0.1 address / 「手机本地未连接」 / phone capability chips), 复制链接
- [ ] 9. Frontend: 订阅 — 手机专用订阅 block next to the normal one
- [ ] 10. Frontend checks: tsc, eslint, `npm run build`; backend `go test` touched packages + contract tests
- [ ] 11. Android: entry choice (getEntry / setEntry, state event), auto-connect default OFF (battery gate only for
      auto), manual connect allowed with battery restricted, version name/code consistent; tests; signed APK;
      check-apk-private; push branch (no release)
- [ ] 12. Bridge contract `panel-app-bridge.md` updated, identical in both repos
- [ ] 13. Lab (prod-DB copy, loopback netns): fork.162 vs branch browser screenshots identical (desktop + 390 H5,
      admin + user); web vs App side-by-side gallery /root/mmwx-preview/panel-app-v1/
- [ ] 14. Push branch, CI Build Check green; summary + final.md; lab copies deleted

## Notes / decisions
