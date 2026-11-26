# 315 — App 授权「我的入口」 + 专线头「端内」不挤名字 + 订阅节点名固定

User 2026-11-26 evening (voice, after trying im.gamer App 1.0.10-panel on fork.174). Build + screenshots only:
no release, no OTA, no production change. Branch `feature/3.0.27-app-grant-entry` from fork.174 (e604e65d),
worktree `/root/projects/flvxx/flvxx-315`. Gallery `/root/mmwx-preview/app-grant-entry/`
(http://104.145.236.29:8791/app-grant-entry/).

## A. 「端内」 tag breaks the 专线 header on phones
- H5 `AdminPhone` 专线 header: the name never wraps (nowrap, no shrink), the header stays one line at 360 px /
  125 % text (the user's phone font). 端内 moves into the right cluster, just before the count bubble.
- If it does not fit at 360 px / 125 %, the route pill leaves the H5 header and goes to the expanded area's
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
- [x] r2: re-shoot phone screens at 100 % + 125 % (360 / 390), re-measure A at 125 %, re-run browser proof, gallery

## Decisions
- **A, recommended layout built**: H5 header = chevron + name (nowrap, no shrink) + ⚠ + spacer + 「端内」 + count;
  the route pill moved to the first chip row of the opened 专线 (H5 only; with no line data a single route pill
  row). Measured in the lab (r2, text 100 % / 125 % = the user's phone font): fork.174 names break into 2 lines
  at 390 / 100 % (header 61 px), 3–4 lines at 360 / 100 % (84–106 px) and 5 lines (one character each, header
  157 px) at 360 and 390 / 125 %; the branch keeps every 专线 name on 1 line, header 52 px, one row, count bubble
  inside the card, no overflow at 360 / 390 × 100 / 125 %. The alternative
  (端内 as a small second line under the name, route pill kept) also fits — rendered as a mock only. Chosen the
  recommended one: 端内 stays the same readable amber pill as 诊断 / desktop, and the header width no longer
  depends on the route label. Desktop: name `whitespace-nowrap` only (no visible change).
- **A, rule rows**: fork.174 wraps the rule name next to its 到落地 tag (2 lines: 「…Seednet」 at 360 / 100 %
  and 390 / 125 %, both 广台 rules at 360 / 125 %). Name and tag now
  sit in one wrapping row: the name keeps words whole (`word-break: keep-all`), the tag stays right of it when
  it fits and drops under it (right-aligned) when not — r2: names 1 line at all four sizes, tag right of the name at
  390 / 100 %, under it where it does not fit (360 / 125 % both rules), never outside the card. 诊断: no name
  squeeze at 100 / 125 %, unchanged.
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
  gallery http://104.145.236.29:8791/app-grant-entry/.
- Round 2 (11-26, docs only, no code change): phone shots re-taken at exactly two text scales, **100 % and
  125 %** (125 % = the user's phone font; 115 % / 130 % dropped and deleted), 360 and 390 px for every A header /
  open line / rule row / alternative / 诊断 and every B 手机本地 state (first, connecting, starting, connected,
  failed) and 入口机 → 手机本地, before (fork.174) / after pairs where a before exists; desktop unchanged
  (87 images). Measurements above are from r2. Browser-unchanged proof re-run (100 %): 授权 / 入口机 desktop
  cards and H5 授权 pixel-identical, H5 入口机 only the 「更新于 HH:MM」 label, page text identical on all three;
  desktop 专线树 differs only by the live 端内 value and ≤ 2/255 background-gradient noise. 125 % showed no
  layout break (cosmetic only: 「利群客户端」 label wraps to 2 lines and the App version pill truncates with …
  in 入口机 → 手机本地 at 360 / 125 %).

## Round 3 — 「v3 简洁版」 (user 11-26 ~23:15 TW: 「信息量太大了，不够简洁，没有层次感」)
Brief /root/flvx-workers/briefs/app-grant-simple.md. Build + screenshots only (no release / OTA / prod change);
part C untouched. Gallery: new top section 「v3 简洁版」 on /root/mmwx-preview/app-grant-entry/ (old shots below).

- A: H5 专线 header shows the line name without 「利群·」 (display only); the route pill goes back IN the header:
  chevron + name + route pill + 端内 pill (+ ⚠) + count bubble on ONE line at 360 / 125 % for every prod line;
  compact route pill if needed (never wrap, never push the bubble out); the r1 route row in the opened 专线 goes.
- B: 授权 → 我的入口 with 手机本地 = three layers under the dropdown —
  1. the old App's first-run cards (PubOnboarding + OnboardingViews): 电池 / 节点地址 127.0.0.1 / 分应用代理里绕过,
     onboarding ring on the first untapped card, tap → sheet (tip text) → 「我知道了」 → shatter (~600 ms) + reflow;
     battery card leaves only when the battery is 无限制 (never back); localStorage; reduced motion → fade;
  2. connect row: 商家账号 (tap = editCredentials) + big 连接 / 断开 (no account → account first; battery not
     无限制 → quick-fix sheet 去设置 / 仍然连接);
  3. status only while connecting (steps) / failed (one red line) / connected (per-forward list, one pill);
     device-limit amber line only when the vendor device count is full.
  Removed: checklist, 快捷设置, notes, 「更多」; 自动连接 / 预热池 / 运行日志 / 换面板地址 behind a gear in the card
  header (手机本地 only). 入口机 → 手机本地: fix 「利群客户端」 wrap + version pill ellipsis at 360 / 125 %.
- Browsers unchanged for B (proof vs fork.174).

### Round 3 decisions
- **A**: H5 专线 header = chevron + name without 「利群·」 (`dropLeiKwan`, display only; a leading flag stays,
  a bare 「利群」 stays) + route pill 「CAN→HKG」 (`routeTight`, 10 px text, 4 px padding) + 「端内 X ms」 (compact:
  11 px, 6 px padding) + ⚠ + count bubble; header gap 6 px / padding 10 px, inner gap 4 px. Name and 端内 never
  shrink; only the route pill could cut (…) as a last resort; the count bubble is outside the clipped group, so it
  always stays in the card. The first try (11 px route, standard 端内 pill) cut the route at 360 / 125 % by 11–16 px;
  the compact version leaves 18–23 px at 360 / 125 % (enough for the ⚠, 14 + 4 px). The opened 专线 is fork.174's
  chip row again (no route row). Rule rows, desktop and normal users unchanged.
- **B layer 1** (`phone-cards.tsx`): the old App's cards ported — 2-column grid (3 columns on desktop), card =
  tinted badge (amber battery / blue server / teal split) + one chip (电池 未设置 / ✓ 已设置, others 必看) + face
  (max 2 lines, whole words: breaks only at 电池设为 | 「无限制」, 节点地址 | 127.0.0.1, 分应用代理 | 里绕过 — the
  first shots broke 「无限 / 制」 and 「绕 / 过」 at 360 / 125 %). Ring = `OnboardingRing` (plan 146/169 component, lap + rest) on the first untapped card, not while
  a sheet is open or cards leave / reflow. Sheets = bottom sheet on phones (centred dialog ≥ 640 px) with r2's tip
  texts (battery: 我知道了 + 去设置; node: 127.0.0.1 + per-forward port table; bypass: client list).
  Shatter = 5 × 3 cells → 30 triangles, each a `clip-path` copy of the card in a fixed layer, Web Animations
  (transform + opacity, 620 ms, App physics: outward + up, gravity, spin, fade after 25 %); reflow at 60 % = FLIP
  glide of the other cards + the block's height (320 ms). Reduced motion → 180 ms fade, no glide. Storage
  `flvx_phone_card_done_<k>` / `flvx_phone_card_seen_<k>` (try/catch). Battery: retired at first sight if already
  无限制; on screen when it turns 无限制 → ✓ 已设置, shatters after 650 ms; its 「我知道了」 only closes the sheet.
- **B layer 2**: left 商家账号 (「填写商家账号」 amber, or small 「商家账号 / 已填 ·…407」) → `editCredentials`; right
  big 连接 (primary solid) / 断开 (danger flat). 连接 without an account → the App's account dialog first (connect
  only after `saved`); battery not 无限制 → sheet 「电池没设成「无限制」」 with 仍然连接 / 去设置 (back with 无限制 →
  connects by itself).
- **B layer 3**: connecting = the App's real four steps (读取账号 / 连接商家 / 等待商家授权 / 启动入口 — the words
  1.0.10-panel sends in `stage`; the brief's 「申请线路授权 / 确认本地入口」 are not App stages) with the time on
  the current step; failed = one red line 「连接失败：…」 + code (only if the message lacks it); connected / partial = 「已连接 7/7 · 21:02 起」 and
  one row per forward (name without 利群 on top, 127.0.0.1:port small under it — on one line 「广台 Seed…」 was cut at
  360 px — and ONE pill on the right: 失败 / 正走备线 / 快切 (= ready with 日志快切
  armed) / 就绪 (ready, no 快切) / 连接中 / 未连接; a failed forward shows its reason under it). Idle = nothing.
  Device limit: one amber line only when the vendor count is full and this phone is not connected (when connected
  the phone holds one of the slots itself).
- **B gear** (amber round button, 手机本地 only) → sheet 手机本地设置: 打开 App 自动连接 (+ one-line note), 预热池,
  运行日志 (size, 查看 / 复制 / 分享), device name + 换面板地址. Removed: checklist, 快捷设置, the three notes,
  「更多」, the r2 「用之前看一下」 block and its `flvx_phone_ok_once` / `flvx_phone_bypass_ok` flags (old keys
  stay in storage, unused).
- **Desktop App mode** (same card in the narrow 授权 column): always 2 card columns (a viewport `sm:grid-cols-3`
  squeezed 3 cards into ~230 px and the chips spilled out — fixed in the second commit); the header subtitle
  「这台手机用哪个入口」 truncates instead of pushing the gear to a second line.
- **Lab data**: the fake vendor reports 4 / 5 devices for the gallery (prod-like 5 / 5 shot separately), so the
  device-full line shows only in its own shots.
- **入口机 → 手机本地**: compact labels 92 px + nowrap (「利群客户端」 one line at 360 / 125 %); version pill
  「App 1.0.10-panel」 (versionCode moved into its title) — no ellipsis.

### Round 3 checklist
- [x] Lab r3 (`/root/flvx-workers/runs/app-grant-simple/lab`): new read-only prod-DB snapshot, branch paneld (backend
      unchanged; same leikwan API as fork.174 → one backend for both dists)
- [x] A: header name without 「利群·」 + route pill back in the header, one line at 360 / 390 × 100 / 125 %; open-area
      route row removed
- [x] B layer 1: first-run cards (grid, chips, ring, sheets, shatter + reflow, battery rule, storage, reduced motion)
- [x] B layer 2: 商家账号 + 连接 / 断开 (credentials first, battery quick-fix sheet)
- [x] B layer 3: steps / failed line / per-forward list / device-full line
- [x] B gear sheet (自动连接, 预热池, 运行日志, 换面板地址); checklist / 快捷设置 / notes / 更多 removed
- [x] 入口机 → 手机本地 360 / 125 % nits
- [ ] tsc + eslint (changed files) + npm build
- [x] Shots (360 / 390 × 100 / 125 %) + browser proof vs fork.174 + gallery 「v3 简洁版」 section
- [x] Commit, push, CI green (45852510374 on e1bd5e8c, 45854586829 on 8b943a13); ledger row 315 head; summary

### Round 3 tests
- `tsc --noEmit` ok; eslint on the 7 changed / new files: 0 problems; `npm run build` ok; CI Build Check
  45852510374 green on e1bd5e8c, 45854586829 green on 8b943a13. Go untouched (no Go run).
- Lab `/root/flvx-workers/runs/app-grant-simple/lab` (fresh read-only prod-DB snapshot, `unshare -n`, branch
  paneld = fork.174 leikwan API, mocked App bridge, 100 % / 125 % text emulation), 0 page errors in every pass:
  - A: every prod 专线 header one line, 52 px, route pill complete, bubble inside — slack 57–60 / 18–23 / 87–90 /
    48–53 px at 360·100 / 360·125 / 390·100 / 390·125 % (phone browser 360·125 % the same).
  - B behaviour: 连接 without account → only `editCredentials` (no connect); battery not 无限制 → quick-fix sheet,
    no connect call; 我知道了 → 30 shards, the next card gets the ring, 2 → 1 card; battery flips to 无限制 → card
    gone, flag set, still gone after reload with the battery back to 「not 无限制」; reduced motion → 0 shards,
    card removed; server entry → no gear; 入口机 → 手机本地 labels all one line and no cut pill at 360 / 390 × 125 %.
  - Browser proof vs fork.174 (same DB / backend): 授权 + 入口机 desktop cards and H5 授权 pixel-identical, H5 入口机
    only 「更新于 HH:MM」; page text identical on all four.
- Gallery: section 「v3 简洁版」 on top of http://104.145.236.29:8791/app-grant-entry/ (64 images in v3/, old r1 / r2
  sections below; previous index kept as index-r2.html).

## Round 4 — 「v4」 (user 11-26 night after the v3 shots)
Brief /root/flvx-workers/briefs/app-grant-v4.md. Build + screenshots only (no release / OTA / prod change); part C
untouched. Gallery: new top section 「v4」 on /root/mmwx-preview/app-grant-entry/ (v3 and older below).

- A: 专线 collapsed header = fork.173's again (name as stored WITH 「利群·」, route pill, count; no 端内) on H5 and
  desktop (undo r1 nowrap / r3 prefix strip, compact pills, header 端内). 端内 moves into the opened 专线: H5 = the
  chip row's right end, right edge on the forward cards' 到落地 tags (same pill size, same tooltip); desktop = the
  转发 column of the opened 专线, left edge on the rows' 到落地 tags.
- B: 商家账号 = 4th first-run card (电池 → 商家账号 → 节点 → 分应用代理, 2 × 2): tap → the App's account dialog,
  leaves only when the account is really saved (live), never back; 127.0.0.1 sheet leads with bold
  「推荐使用订阅：订阅里的节点地址已经是 127.0.0.1」; connect = small bar (dot + 未连接 / 连接中 / 已连接 …, mint flat
  连接 / red flat 断开) that glides up as cards leave; 连接 with something missing → one quick-fix sheet.

### Round 4 checklist
- [x] A: H5 + desktop header back to fork.173 (端内 out); 端内 in the opened 专线 aligned with 到落地 tags
- [x] A: drop r3 helpers (routeTight, LineEndInTag compact)
- [x] B: 商家账号 card (live set → ✓ 已填 → shatter, retired unseen if already set), 2 × 2 grid
- [x] B: 127.0.0.1 card face 「节点 127.0.0.1」 + sheet text (+ 生成订阅 button)
- [x] B: connect bar (dot + word + mint 连接 / red 断开), reflow end without a jump
- [x] B: quick-fix sheet for missing account and / or battery; 商家账号 row in the gear sheet
- [x] tsc + eslint (changed files) + npm build
- [x] Shots (360 / 390 × 100 / 125 %) + fork.173-look header proof + browser proof + gallery 「v4」
  (r2 worker app-grant-v4-r2: 61 shots on 315 + fork.174 run, 0 errors. Collapsed H5 header = fork.174 minus its
  header 端内 pixel-identical at 360/390 × 100/125 %; every name one line, header 52 px at 360/125 % — no nowrap
  needed. Desktop tree: same layout (only ≤2/255 background-gradient noise). Opened: 端内 edge diff 0 px vs every
  到落地 tag, same 18 px pill / font. Browser without App: 授权 + 入口机, H5 + desktop, identical text; pixels
  identical except the 「更新于」 clock on the H5 入口机 tab. Bar glides 94–108 px over 19 eased frames; 30 shards;
  reduced motion = no shards; server entry = no cards / gear.)
- [x] Commit, push, CI green; ledger row 315 head; summary — code ef98223e (CI 45862372500 green)
