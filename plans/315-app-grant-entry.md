# 315 — App 授权「我的入口」 + 专线头「端内」不挤名字 + 订阅节点名固定

User 2026-11-26 evening (voice, after trying im.gamer App 1.0.10-panel on fork.174). Build + screenshots only:
no release, no OTA, no production change. Branch `feature/3.0.27-app-grant-entry` from fork.174 (e604e65d),
worktree `/root/projects/flvxx/flvxx-315`. Gallery `/root/mmwx-preview/app-grant-entry/`
(http://104.145.236.29:8791/app-grant-entry/).

## A. 「端内」 tag breaks the 专线 header on phones
- H5 `AdminPhone` 专线 header: the name never wraps (nowrap, no shrink), the header stays one line at 360 px /
  130 % text. 端内 moves into the right cluster, just before the count bubble.
- If it does not fit at 360 px / 130 %, the route pill leaves the H5 header and goes to the expanded area's
  chip row (`LineChips`). Alternative rendered for comparison: 端内 as a small second line under the name.
- Check the other plan 306/310 tags on H5 (rule rows 到落地, 诊断) for the same wrap.

## B. App: connecting 手机本地 lives in 授权, 入口机 → 手机本地 is info only
- 授权 tab top card 「我的入口」 (App + admin's own grant only): dropdown 手机本地 + server 利群入口.
- Under it when 手机本地 is picked: setup + connect panel (tips first time, checklist, 快捷设置, 连接 / 断开 +
  stage block like the old App, 专线状态, 更多 = 运行日志 / 换面板地址, short notes).
- 入口机 → 手机本地 (H5 row + desktop drawer): info only + 「连接和设置在 授权 → 我的入口」.
- Browsers and normal users: unchanged (pixel check vs fork.174).

## C. Subscription node names stay the same when the entry changes
- `user_subscription.entry_names` (nullable bool): NULL / true = today's names (byte-identical output),
  false = fixed names (forward name; 「 · 入口名」 only when the same forward is in this sub through ≥ 2 entries).
- New subscriptions (editor, 利群汇聚 生成订阅) default = fixed names (switch off); existing rows stay NULL.
- Editor switch 「节点名带入口名」 + hint; read-only in the admin 全部用户 view.

## Checklist
- [x] Read code, plan doc
- [x] Lab: prod-DB snapshot (read-only copy), fork.174 + branch binaries/dists, bridge mock
- [x] A: H5 专线 header (recommended + alternative), check rule rows / 诊断
- [x] B: 授权 「我的入口」 card + dropdown + 换主入口 confirm for another server entry
- [x] B: setup / connect panel (tips, checklist, 快捷设置, stage block, 专线状态, 更多)
- [x] B: 入口机 → 手机本地 info only (H5 row + desktop drawer/tile)
- [x] C: DB column + model + repo + handler (create defaults, update, views)
- [x] C: naming in SubPanelLinks / resolve (landing / chain / external forwards)
- [x] C: editor switch + admin view read-only
- [x] C: golden tests (old unchanged, fixed: single entry, entry switched, two entries)
- [x] C: prod-copy count of subscriptions whose names would change
- [x] go vet + go test SQLite all (ok); npm build + tsc + eslint (changed files) ok
- [x] PG handler/repo/contract (`-p 1 -timeout 40m`, FLVX_POSTGRES_TEST_DSN) — all ok (13:43–14:26 UTC)
- [x] Screenshots + browser-unchanged pixel check + gallery
- [x] Push, CI green (CI Build Check 45831993601 on da84f021)
- [x] Ledger row 315, summary

## Decisions
- **A, recommended layout built**: H5 header = chevron + name (nowrap, no shrink) + ⚠ + spacer + 「端内」 + count;
  the route pill moved to the first chip row of the opened 专线 (H5 only; with no line data a single route pill
  row). Measured in the lab: fork.174 names break into 2 lines at 390 px / 100 % and 4–5 lines (one character
  each) at 115–130 %; the branch keeps 1 line (header 52 px) at 360 / 390 × 100 / 115 / 130 %. The alternative
  (端内 as a small second line under the name, route pill kept) also fits — rendered as a mock only. Chosen the
  recommended one: 端内 stays the same readable amber pill as 诊断 / desktop, and the header width no longer
  depends on the route label. Desktop: name `whitespace-nowrap` only (no visible change).
- **A, rule rows**: at 360 px / 130 % 「🇹🇼 利群广台 Hinet」 wrapped next to its 到落地 tag. Name and tag now
  sit in one wrapping row: the name keeps words whole (`word-break: keep-all`), the tag stays right of it when
  it fits and drops under it (right-aligned) when not. 诊断: no name squeeze, unchanged.
- **B**: 「我的入口」 is the first card of 授权 (H5 tab and desktop card), App + admin's own grant only. Picking
  手机本地 / the server 主入口 calls `setEntry` at once (phone-only, as the old 换主入口 path; same toasts, page
  reload so rows / phone plan follow; auto-connect is the App's own reaction to setEntry). Another server entry
  opens the existing 换主入口 dialog preset to it (the dialog now prefers a preset server entry over 手机本地).
  Offline entries are disabled in the dropdown. The grant-row 换主入口 button / chip work as before.
- **B, flags**: 「第一次」 = `localStorage.flvx_phone_ok_once` (set when the page sees connected with ≥ 1 ready
  forward); 分应用代理 manual tick = `localStorage.flvx_phone_bypass_ok` (WebView storage, per phone).
  No App change needed; works with 1.0.10-panel (stage words 读取账号 / 连接商家 / 等待商家授权 / 启动入口
  come from its `stage`; the elapsed time is counted by the page).
- **B, 入口机 → 手机本地**: info only — 127.0.0.1 (+ 起 time), 利群客户端 (status pill, 手机 App, device), 版本,
  支持功能, 日志快切, 预热池 (read-only pill), 转发 就绪 x/y, and 「连接和设置在 授权 → 我的入口 ›」 (jumps
  there; desktop closes the drawer). Removed from it: 连接 / 断开, 自动连接, 电池, 商家账号, 在线设备, error box,
  专线状态, 运行日志, 设为入口…, 换面板地址 (all now in 我的入口). 专线状态 rows reworked: rule name + state
  pills, then 127.0.0.1:port + 专线 on a second line.
- **B, browsers**: DOM text identical to fork.174 on 利群汇聚 desktop + H5 (授权 / 入口机); pixels identical
  except live values (「更新于 HH:MM」, 端内 ms).
- **C, API default**: a create **without** `entryNames` keeps today's names (old cached editors, API clients,
  all existing tests / goldens byte-identical); the new editor sends `false` for a new subscription, 利群汇聚
  生成订阅 creates with false. An update without the field keeps the stored value; the editor sends it only
  when the switch changed (an old NULL row stays NULL until touched).
- **C, fixed mode** also lets a forward_link item follow its forward when the picked entry node no longer
  serves it and the forward now has exactly one entry address (else 失效 as today); a follow onto a link
  already in the subscription is dropped (no duplicate node). Only in fixed mode — old subscriptions behave as
  before (the node disappears after an entry switch). The editor's 已选 shows the client name live from the
  switch (pool rows keep 「转发名 · 入口名」 to tell entries apart). Only subscriptions; 复制链接 unchanged.
- **C, prod impact** (prod-DB copy, read-only): 8 subscriptions; if the switch were turned off, 6 would rename
  (4 usb2 ×2, 28 liuchen ×2, 29 test-ns-fx ×1, 31 ns-hajimi 芜湖 ×1, 32 起飞 ×2, 33 imolr ×2 — all
  「转发名 · 入口名」 forward nodes); 21 (sources only) and 27 利群汇聚 (利群 nodes carry no entry name) unchanged.
  Nothing changes unless a user flips the switch.
- **App 1.0.11-panel**: nothing needed for this plan.

## Tests
- `go vet ./...` clean; `go test ./... -count=1` (SQLite) all ok; PostgreSQL `go test -p 1 -timeout 40m` on
  `internal/http/handler`, `internal/store/repo`, `tests/contract` all ok. New `sub_link_names_test.go`:
  default / explicit-on byte-identical, fixed single entry (golden), entry switched (golden, follows + editor
  rekey + save), two entries (golden, unique names, no double after one goes), toggle on / off / keep.
  Existing sublink goldens and the contract test's 「游戏加速 · 上海联通入口」 unchanged.
- Frontend: `tsc --noEmit` ok, eslint on changed files ok, `npm run build` ok; CI Build Check green.
- Lab (`/root/flvx-workers/runs/app-grant-entry/lab`, prod-DB copy in `unshare -n`, fork.174 vs branch
  binaries + dists, mocked App bridge, WebView textZoom emulated by scaling font-size declarations):
  gallery http://104.145.236.29:8791/app-grant-entry/ (57 images).
