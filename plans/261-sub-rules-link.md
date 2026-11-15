# 261 订阅「分流」规则集链接 (panel downloads + re-serves)

Base: tag `3.0.27-fork.133` (65885f9f). Branch `feature/3.0.27-sub-rules-link`, worktree `flvxx-sub-ruleslink`.
Build only (no tag / release / prod change). Ships with plan 260 after the user approves the screens.

## Goal
In 「＋ 添加分流」→ 自己写 a user can paste the link of someone else's rule list. The panel downloads it
(SSRF-guarded), keeps only validated domain / IP rules, stores them and serves them to the clients at
`/s/rs/u/<public_id>.(list|json)`, so mainland clients never need GitHub. Existing 分流 output and UI stay
byte-identical when no link entry exists.

## Design
- Routing JSON entry: `{"type":"link","ref":<user_sub_ruleset.id>,"name":…,"exit":…}` (old JSON parses as
  before; `ref` omitted on other entries → old lists re-serialize byte-identically). One entry = one link.
  Link entries count toward 30 entries, not toward the 300 自己写 lines; ≤ 5 link entries per subscription.
- Table `user_sub_ruleset` (auto-migrate, SQLite + PG): user_id, url_enc (same AES as 外部订阅源),
  url_hint, url_hash (HMAC of the URL, unique with user_id → one stored set per user + URL), public_id
  (128-bit random hex, unique), rules (normalized classical lines), counts, content_hash, fetched_at,
  last_try_at, last_error, fail_count, created/updated time.
- 获取 = fetch + parse + store (or refresh the user's existing set of that URL) → returns id, counts,
  sample. Nothing kept in memory. Unreferenced sets are GC'd on subscription save / delete (sets fetched
  < 30 min ago are kept as pending); user delete removes all of the user's sets. No ticker.
- Fetch: `landingsub.CheckURL` + `subFetcher(h)` (pinned public IP per hop, Forbidden list, ≤ 3 redirects,
  no proxy / cookies, fixed UA), 20 s, ≤ 4 MiB decoded (LimitReader on the decoded body).
- Parser (sublink/rulelink.go): text only (NUL / invalid UTF-8 / gzip / zstd(.mrs) / SRS magic rejected);
  Clash classical, Surge/Loon/QX lists (QX aliases), Clash YAML `payload:` (classical or domain/ipcidr
  behavior), plain one-per-line; keeps DOMAIN / DOMAIN-SUFFIX / DOMAIN-KEYWORD / IP-CIDR / IP-CIDR6 through
  the 自己写 validators; everything else counted as 忽略; policy dropped; deduped; ≤ 50 000 rules; 0 → error.
- Serving: `.list` (classical text for mihomo `behavior: classical`, Stash, Surge/Surfboard RULE-SET) and
  `.json` (sing-box source rule set); ETag = content hash; `nosniff`; Cache-Control like /s/rs/; unknown
  id → 404; per-id rate limit; respects `subLinkEnabled()`.
- Rendering: mihomo/Stash rule-provider `link-<pid>` (classical text, interval 86400, DIRECT from the
  panel), `RULE-SET,link-<pid>,<group>,no-resolve`; sing-box remote `format: source` rule set; Surge
  `RULE-SET,<url>,<group>,no-resolve` (Surfboard without no-resolve, like 自己写). Loon / QX / base64:
  unchanged (node lists only, as with 自己写).
- Refresh: served or subscription pulled and older than 24 h (backoff 24 h → 48 h → … max 7 d after
  failures) → one global background queue, one download at a time, ≥ 5 s between downloads to the same
  host, deduped per set; failure keeps the last good copy. 获取 / 立即更新 use `subTakeFetch`. Logs: host only.
- Privacy: owner APIs return urlHint + counts/status only; never the URL. Admin view (plan 260): name +
  🔗 + N 条 + status only.

## Tasks
- [x] 1. Plan doc
- [x] 2. Parser + caps + binary rejection (sublink/rulelink.go) + unit tests
- [x] 3. Model + repo (table, cap/dedupe, GC, user delete) + tests
- [x] 4. Routing: link entry type in CleanRouting / planRoutes; Clash/Stash/sing-box/Surge rendering; goldens
- [x] 5. Handler: fetch/refresh/list APIs, ownership, background refresh queue, public serving
- [x] 6. Frontend: 自己写 switch 手动输入｜规则集链接, preview, list row 🔗 + status, edit (urlHint / 立即更新 / 换链接), H5 expand row
- [x] 7. Tests: go test ./... + contract, npm run build, eslint; mihomo -t / sing-box check with rule sets fetched from a local panel
- [x] 8. Real-world smoke (≤ 6 URLs, one at a time, ≥ 5 s apart)
- [x] 9. Gallery /root/mmwx-preview/sub-rules-link/ (desktop + H5 390/360, light + dark)
- [x] 10. Admin view (plan 260) handling (merge if finished, else follow-up note)
- [x] 11. Push + CI green (incl. PG contract), ledger row 261, summary — CI 43698536757 green (frontend, Go backend, PG contract, agent) on 457319d1

## Results (11-15 TW)
- Branch `feature/3.0.27-sub-rules-link` (from fork.133) + merge of `feature/3.0.27-sub-admin-view` @ 3427a529
  (plan 260 was finished, ledger row 260): the admin's read-only 分流 tab shows link entries as name + 🔗 +
  `N 条` + status only (backend `subAdminRoute` gets ruleCount / fetchedAt / linkFailed / linkMissing, never
  URL / urlHint / ids; test `TestSubLinkAdminDetailNoURL`). → **release 260 and 261 together from this branch.**
- Go: `go test ./...` 28 packages ok (SQLite contract incl.). New: `sublink/rulelink_test.go` (shapes, dropped
  types, policy ignored, injection, IDN, CRLF/BOM, binary / HTML / empty / too big / 50 000 cap, round trip,
  link goldens), `handler/sub_link_rulelink_test.go` (fetch→save→serve, ETag/304/404, dedupe, ownership, caps
  incl. gzip bomb, SSRF incl. node IP / redirect to LAN / loop, single flight, keep last good, backoff, host gap,
  GC, user delete, hostile list normalization, admin detail), contract `TestSubRuleSetContract` SQLite +
  PostgreSQL 18 (local run ok; CI runs PG too). Old goldens untouched; 5 new `link-*` goldens.
- Real clients against a local panel (loopback netns): mihomo 1.19.32 `-t` ok (Clash + Stash), providers
  `behavior: classical` loaded from the panel (13 / 10 / 7 rules), www.netflix.com → `RuleSet/link-…` → the link
  group. sing-box 1.12.25 / 1.13.21 / 1.14.3 `check` ok, start downloads every rule set incl. the link
  `format: source` JSON (version 2) from the panel, netflix routed to the link group's exit; `rule-set compile`
  accepts the served JSON on all three. (1.14 prints the existing `download_detour` deprecation warning — same as
  the plan-256 catalog sets.)
- Real-world smoke (dev box, one request at a time, 6 s apart): blackmatrix7 Clash yaml 37 rules (1 ignored),
  blackmatrix7 Surge list 1156 (2 ignored), ACL4SSR Netflix.list 40, Loyalsoldier gfw.txt 4453, MetaCubeX
  netflix.list 24, MetaCubeX netflix.mrs rejected (「只支持文本规则集…不支持 .mrs / .srs」).
- Resource: no ticker, no idle goroutine (the refresh worker exits when its queue is empty); one background
  download at a time panel-wide, ≥ 5 s per host, 2 interactive downloads max; worst-case parse (4 MiB / 50 000
  rules) + sing-box JSON ≈ 106 ms, 56 MB short-lived allocations (60k allocs); serving reads the row (rules
  only on a cache miss of the client's ETag), 60 downloads / h per set.
- npm run build ok; eslint on the changed files 0 errors (project-wide 90 errors / 3889 problems = identical to
  fork.133, all in untouched files).
- Gallery: /root/mmwx-preview/sub-rules-link/ (63 screens, 69/69 checks; 「日常」 without links is pixel-identical
  to fork.133 on desktop + H5, light + dark).

## Decisions made here (for the user to confirm)
- Plain one-per-line lists follow Clash text semantics: `+.x` / `.x` / `*.x` = x and subdomains, bare `x` = exactly
  x (MetaCubeX .list, Loyalsoldier payloads); classical lines keep their type. Unicode (non-xn--) domains are
  dropped like in 自己写.
- `no-resolve` on every link RULE-SET line (Clash / Stash / Surge; not Surfboard) — same as 自己写's IP lines, and
  the config does not change when a refreshed list gains or loses IP ranges.
- 获取 stores the set at once (no big preview in memory); a set nobody saved is removed by the next save /
  page open after 30 min. 获取 of a URL the user already has = 立即更新 of that set.
- H5 link rows use the expand row (name + 🔗 + red dot on failure + ＋); other 分流 rows unchanged.

## Follow-ups
- Release (with 260): panel only, DB = new table `user_sub_ruleset` (auto-migrate), no OTA. Post-release check:
  获取 one public list through CF, pull Clash + sing-box, GET `/s/rs/u/<id>.list` / `.json` 200, delete the test sub.

## UI 跟进（用户 11-15）
用户看完 261 截图（其余都同意）：手机上「走哪里」列表混在一起 —— 节点选择 / 自动选择在最上，用户自己的节点在中间，
直连 / 拒绝在最下。直连、拒绝和节点选择、自动选择一样是系统内置的，应该放在一起；节点另成一组，加小标签 / 图标，
一类一类排，层次清楚、清爽。桌面同样处理。纯前端展示，保存的值和生成的订阅不变；不改 Go / API / 数据库
（节点来源 = 已选里本来就有的 group 字段，前端已经拿到）。

- [x] 「走哪里」分两组：**系统策略**（节点选择 · 自动选择 · 直连 · 拒绝，固定顺序，lucide 图标 + 同色调）和
      **订阅里的节点**（订阅顺序，名字 + 一个来源小标签 落地 / 规则 / 外部节点 / 订阅源；没有节点时一行灰字
      「订阅里还没有节点」）—— 桌面下拉、手机底部弹层、自己写 / 规则集链接表单的走哪里
- [x] 分流行上的出口 chip / 手机行里的出口文字带同样的小图标（节点 = 小节点标记），缺失节点逻辑不变
- [x] 「国内网站」固定行的选择（直连 / 走代理）用同样的图标；管理员只读视图用同样的 chip
- [x] npm run build + eslint（改动文件 0 错误）
- [x] 截图「走哪里分组（v3）」放在 /sub-rules-link/ 最上面（-v3 后缀，手机 390 / 360、桌面 1440、浅色 / 深色）
- [x] 提交、推送、CI 绿（含 PG 合约），台账 261 行更新 head commit（仍待发布）

结果（11-15 TW）：
- 只改前端 `vite-frontend/src/pages/sub-link/routing.tsx` + `editor.tsx`；Go / API / 数据库没动。节点来源取自「已选」里已有的
  `group`（rule / landing / external / source / source_all），桌面上订阅源节点多一个灰色源名字（原来就有的 detail）。
- 图标统一用 lucide：节点选择 Rocket（蓝）、自动选择 Zap（青）、直连 ArrowRight（绿）、拒绝 Ban（红）、指定节点 Server（紫）；
  缺失节点仍是黄色「节点选择」+ 原提示（多了 Rocket 图标）。每个系统策略后面一句灰色说明（在客户端里手动选 / 自动挑最快的 /
  不走代理 / 直接断开）。国内网站固定行：直连（国内不走代理）/ 走代理（全部走代理），原来的「走代理（全部走代理）」拆成名字 + 说明。
- 桌面「自己写 / 规则集链接」表单：两行「系统策略」4 个按钮 +「订阅里的节点」选一个节点 ▾（只列节点，带来源标签）。
- npm run build 通过；eslint 改动文件 0 错误 0 警告。
- 截图：/root/mmwx-preview/sub-rules-link/ 最上面「⓪ 走哪里分组（v3）」31 张（-v3 后缀），38/38 自动检查（分组顺序、四个系统策略顺序、
  来源标签顺序、360 宽无横向溢出、长名字截断、选节点后行内显示、选「拒绝」保存后存的就是 reject、空订阅提示、缺失节点、管理员只读
  11 行都有图标且没有选择器），页面报错 0。
- 代码 b71a4001，CI 43702343065 全绿（前端 / 后端 / agent / PG 合约）。只构建，不打 tag、不发布；等用户看 v3 截图同意后和 260 一起发布（fork.135，先合并 fork.134）。
