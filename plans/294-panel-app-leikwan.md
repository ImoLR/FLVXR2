# 294 — 面板 App 第一版（利群汇聚 + 手机本地入口）— BUILD + PREVIEW ONLY

Worker `panel-app-leikwan-v0` (Claude xhigh). Brief: /root/flvx-workers/briefs/panel-app-leikwan-v0.md.
Design discussion: memory `panel-app-research.md` (11-22).

Hard limits: no FLVX tag/release, no prod upgrade, no merge to main, no OTA; no Android release / public upload /
zc-update; no real LeiKwan vendor connect from dev box / emulator / CI (user has 5/5 device slots); prod panel API
read-only at most; pub 重仓利群 / lab 利群原版 behaviour unchanged.

## Where
- FLVX: worktree `/root/projects/flvxx/flvxx-294`, branch `feature/3.0.27-panel-app` (from tag 3.0.27-fork.161).
- Android: private repo `/root/Documents/Codex/2026-10-12/v4/implementation` (ImoLR/leikwan-local-bridge), branch
  `feature/panel-app` from `origin/main` (v1.0.7), own worktree; third flavor `panel`.

## Checklist
- [x] 1. Read context (memories, Android rules, both repos) and map the code
- [x] 2. Bridge contract doc (same file in both repos)
- [x] 3. Frontend: App-mode detection (bridge object) + App nav = only 利群汇聚
- [x] 4. Frontend: per-entry capability/state schema (front machines from host status; 手机本地 from bridge)
- [x] 5. Frontend: 入口机 tile tap = select, small 「详情」 opens drawer; 手机本地 tile (App only, first)
- [x] 6. Frontend: 专线·转发 tree per-entry chips (on / grey off / hidden unsupported / 「此入口未开」 row)
- [x] 7. Frontend: 复制链接 / 订阅 follow the selected entry (手机本地 → 127.0.0.1:<vendor port>)
- [x] 8. Frontend: 手机本地 详情 drawer (status, connect/disconnect, auto-connect, warm, battery, slots, log, versions)
- [x] 9. Frontend: H5 「入口：xx」 chip on the 规则 tab → jumps to 入口机
- [x] 10. Backend: small additive per-entry capability data if needed (no DB migration) + tests
- [x] 11. Frontend build + backend `go test` for touched packages; push branch (no tag)
- [x] 12. Android: worktree + branch `feature/panel-app`, flavor `panel` (applicationId, label im.gamer, brand icon)
- [x] 13. Android: first-run panel-address page (https only) + WebView host + origin-locked bridge
- [x] 14. Android: bridge → pub connect engine (state, connect/disconnect, auto-connect, warm, log, battery, info)
- [x] 15. Android: foreground notification 「手机本地已连接 N/M 条 [断开]」 + panel-unreachable fallback page
- [x] 16. Android: Destinations gains exactly the saved panel origin host; tests (origin, bridge, allowlist)
- [x] 17. Android: assemblePanelDebug/Release, pub + lab unit tests of touched classes, check-apk-private.py; push branch
- [x] 18. Screenshot gallery /root/mmwx-preview/panel-app-v0/ (mocked bridge + native renders)
- [x] 19. Summary /root/flvx-workers/runs/panel-app-leikwan-v0/summary.md; plan ticked

## Notes / decisions
- App vs browser = `window.panelApp` (injected by androidx.webkit addWebMessageListener for the saved panel origin
  only). Different name from the old wrapper's `window.JsInterface` (utils/panel.ts isWebViewFunc), so the old
  WebView code path (network.ts base URL switch) never triggers.
- Per-entry capability data for front machines is computed in the frontend from what the admin already gets:
  `host/list` (snapshot.forwards / lines / warm, manage.state.forwards live running + udp) and `rule.hostValues`
  (per-host warmMain / logswitch / backup) + `entryLock` / `allEntries`. 手机本地 = the bridge's PhoneState.
  No DB migration.
- Backend (additive): `leikwan-rule/link {id, entry}` (entry = "local" or a node id of the entry group; users only
  "local" or their own 主入口) and the public sub `?lk=local|<nodeId>` (nodeId only for admin-owned subs), cache
  key includes it.
- Credentials: the panel stores only the vendor account API token (leikwan_account.token, never returned). 客户端 ID
  + Agent Token stay on the phone (CredentialStore, AndroidKeyStore AES-GCM), entered in a native dialog; the App
  does not need the 转发管理令牌 (forwards are managed by the panel; 在线设备 x/y comes from the panel's usage view).
- Device identity: AndroidKeyStore key + no_backup files are per app UID → applicationId
  `com.localbridge.leikwan.panel` = one more vendor device (user is at 5/5).
- Bridge object = `window.panelApp` (not `imgamerApp`: the APK privacy scan bans the string `imgamer`).
- 手机本地 selected → the 专线 header hides 备线 / 预热 / 快切 / SS 过滤 (host-side features), keeps 单向/双向.
- Tests: `go test ./internal/http/handler/ ./tests/contract/` ok (new TestLeiKwanEntryPickContract: link local / own / other
  entry, admin vs user, 只开主入口, ?lk= sub + cache split, ?lk pull keeps the 换主入口 grace); tsc + eslint + `npm run build` ok.
- Android (ImoLR/leikwan-local-bridge `feature/panel-app` @ 37eceda, private): flavor `panel` =
  `com.localbridge.leikwan.panel`, label im.gamer, versionName 1.0.7-panel / vc 1, androidx.webkit for this flavor
  only. Shared hooks Edition.screen()/notice(), Destinations.setPanel/panel, RunLog site labels (pub/lab defaults
  unchanged). Tests: testPanel 28/28; pub 77/77 + lab 77/77 for the touched shared classes. Signed release APK
  (local only, after the fallback fix a14d4f1) sha256 e98d0601…99a4, `check-apk-private.py --panel` 0 hits. Emulator screenshots via `panel-e2e/v0`.
- Finding: on the agents' live reports every front (火山广州 / 百度云广州 / 阿里广州 / 火山上海) runs all 7 forwards,
  540 included; 540 on 阿里广州 only lacks a warm entry (→ grey 不预热). The real 「此入口未开」 today = 279 / 281 on
  every non-main entry (只开主入口 on, not exempt).
- Open: a panel with login captcha (Cloudflare Turnstile) cannot log in inside the App (one-origin rule); prod has no
  captcha configured.
- Native pages (first-run address, panel-unreachable fallback) = real Android views in the panel flavor;
  screenshots from the CI emulator (panel-e2e/*, no credentials → no vendor call).

## Result (2026-11-23 01:3x TW)
- Gallery http://104.145.236.29:8791/panel-app-v0/ — 26 PNG: panel pages from this branch's dist on a prod-DB copy in a
  loopback-only netns (lab scripts /root/flvx-workers/runs/panel-app-leikwan-v0/lab/, 20 Playwright checks OK, the
  DB copy /root/lk294-lab deleted afterwards) + native pages from CI emulator run 45048650542 (branch panel-e2e/v0).
- Summary (Chinese): /root/flvx-workers/runs/panel-app-leikwan-v0/summary.md. No tag, no release, no prod change.
