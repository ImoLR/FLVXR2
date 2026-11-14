# 256 · 订阅二期：订阅页「外部节点」入口 + 每条订阅的「分流」列表

Base: tag `3.0.27-fork.131`, branch `feature/3.0.27-sub-rules`. **Build only** (no tag / release / prod change);
waits for the user's screen approval. Design (source of truth): `:8791/sub-rules-design/` (v2), brief
`/root/flvx-workers/runs/sub-rules/`.

## Decisions

### Part A — 外部节点 entry (frontend + two small read APIs)
- 订阅 header pill 「外部节点 (N)」 → dialog with the user's external nodes (same table, same cap 20). Reuses
  `AddExternalDialog` / `ExternalManageList` from `landing/chain-proxy.tsx` via optional props only, so the 链式代理
  dialog renders exactly as before.
- Usage per node: 「链式代理在用」 (delete still refused by the backend, as today) and 「N 个订阅在用」 (delete allowed
  after a confirm). `landing/external/list` gains `subUses` (one query over the user's subscriptions' `items`).
- 「检测全部」 = `POST /api/v1/sub/external/probe`: TCP connect time from the panel to each external node (address
  re-checked with the same guard as adding), 4 at a time, 3 s timeout, UDP-only protocols marked 「UDP，无法检测」,
  rate-limited per user. No background probing.
- Editor left column: 「外部节点」 group always shown, header 「＋ 添加」 opens the same add dialog; the picker reloads
  only its pool (unsaved selection kept).

### Part B — 分流
- Column `user_subscription.routing` (nullable TEXT, JSON list). `rule_mode` stays the 「国内网站」 row.
- Entry: `{type:"builtin",key,exit}` | `{type:"custom",name,lines,exit}`; exit = `select|auto|direct|reject|node:<ref-key>`.
- NULL / `[]` → every format byte-identical to fork.131 (golden files untouched).
- New subscription (create without `routing`) = default 8: AI, YouTube, 流媒体, Telegram, GitHub, 谷歌, 微软(直连),
  苹果(直连). Stored order is normalized: custom entries (user order) first, then builtins in rule order.
- Caps: 30 entries, 300 custom lines total, line ≤ 253 chars, custom name ≤ 20 chars, unique, no `,` `"` / control
  chars, not a node name / reserved group name.
- A `node:` exit must name a node of this subscription (a picked node, or a node of a followed source). On save an
  exit whose node is not in the subscription is stored as 节点选择 (the editor shows that row yellow first); at render a
  node missing from that format's output falls back to 节点选择.
- Catalog = one table row per category (`sublink/routing.go`), served to the UI by `sub/pool` (no UI work for a new
  category). Data = MetaCubeX meta-rules-dat (meta branch `.mrs` + `.list`, sing branch `.srs`); every key checked
  to exist in both branches (11-14):

  | key | name | sets (upstream name) | default exit |
  |---|---|---|---|
  | ads | 🛑 广告拦截 | geosite category-ads-all | reject |
  | ai | 🤖 AI | geosite category-ai-!cn | 节点选择 |
  | youtube | 📹 YouTube | geosite youtube | 节点选择 |
  | streaming | 🎬 流媒体 | geosite netflix, disney, hbo, primevideo + geoip netflix | 节点选择 |
  | tiktok | 🎵 TikTok | geosite tiktok | 节点选择 |
  | spotify | 🎧 Spotify | geosite spotify | 节点选择 |
  | discord | 🕹️ Discord | geosite discord | 节点选择 |
  | social | 💬 社交 | geosite twitter, facebook, instagram | 节点选择 |
  | telegram | 📲 Telegram | geosite telegram + geoip telegram | 节点选择 |
  | github | 🐱 GitHub | geosite github | 节点选择 |
  | google | 🔍 谷歌 | geosite google + geoip google | 节点选择 |
  | microsoft | Ⓜ️ 微软 | geosite microsoft | 直连 |
  | apple | 🍎 苹果 | geosite apple | 直连 |
  | games | 🎮 游戏 | geosite category-games-!cn | 节点选择 |

  Games = `category-games-!cn` alone: it already contains every line of `steam` (61/61) and `epicgames` (30/30) plus
  Blizzard / EA / Nintendo / PlayStation / Xbox…, with the `@cn` (mainland CDN) domains removed — one rule set instead
  of three.
- Rule order (fixed): private/LAN (+ Windows NCSI direct, plan 226) → custom (list order) → ads → ai → youtube →
  streaming → tiktok → spotify → discord → social → telegram → github → google → microsoft → apple → games →
  (cn_direct: existing CN rules) → MATCH 节点选择.
- Groups: after 节点选择 / 自动选择 one select group per entry (`<icon> <name>`, custom `📌 <name>`), members = exit
  first, 节点选择, 自动选择, the nodes, DIRECT (+ REJECT for reject exits). sing-box has no block outbound since 1.13,
  so a `reject` entry is a `"action":"reject"` route rule there (no selector). DNS unchanged.
- Custom lines: `example.com` → DOMAIN-SUFFIX, IP / CIDR → IP-CIDR(6) no-resolve, token without a dot →
  DOMAIN-KEYWORD; `full:` `domain:` `keyword:` `ip:` prefixes; invalid line → error with its line number.
- Rule sets from the panel (`/s/rs/<id>.<ext>`, plan 226 mechanism): `.mrs` (mihomo, behavior domain/ipcidr),
  `.srs` (sing-box, download_detour direct), `.txt` (Stash `format: text` — Clash text list as MetaCubeX ships it),
  `.list` (Surge / Surfboard RULE-SET, classical lines derived from the text list). All catalog files embedded at
  build time; refresh on demand at most once a day per file, one download at a time (single queue worker, no ticker),
  size cap, format check, last good copy kept. A catalog id is never 404.
- Formats: Clash/mihomo + Stash, sing-box, Surge/Surfboard get groups + rules; Loon / QX / Base64 unchanged (the 分流
  tab says so).
- UI: editor tabs 节点 | 分流 (desktop), 可选 / 已选 / 分流 (H5); the old footer 「分流 国内直连 / 全部走代理」
  switch moves into the list as the fixed 「🇨🇳 国内网站」 row (design v2 ②: 「不再是单独的开关」). Card chip 「分流 N」.

## mmwx: 妙妙屋做法里不合理 / 有争议的地方 (design ⑥) — and what we do
| mmwx | problem | FLVX |
|---|---|---|
| Several template systems; prod packages bind none → everyone gets the alphabetically first `fake_ip__v3` | nobody chose it; no Telegram / YouTube / AI / 流媒体 | one visible list per subscription, defaults in code |
| Rule files on testingcf.jsdelivr / gh-proxy.com / 1ms.cc / api.dler.io / raw.githubusercontent | third-party mirrors die; raw.githubusercontent blocked in CN | every rule file from the subscription's own origin |
| Follows `@main` | upstream changes reach users silently, unchecked | daily refresh with format/size check, last good copy kept, embedded fallback |
| Group name mismatch 🚀 节点选择 vs 🚀 手动选择 | DNS `#🚀 节点选择` breaks on built-in templates | group names are package constants, used by DNS and rules alike |
| Steam interval 2880; `pttracker` key; yaml format with `.mrs` URL | unmaintained details | one interval constant (86400); provider fields generated from one table; golden tests per format |

## Tasks
- [x] 1. Fetch catalog snapshot (MetaCubeX, slow sequential), verify keys in both branches, embed files
- [x] 2. `sublink/routing.go`: catalog, entry model, parse/validate/normalize, custom line parser
- [x] 3. Rule-set store: catalog ids × formats, queue worker refresh, validation, Surge list derivation; serve `/s/rs/<id>.<ext>`
- [x] 4. Clash / Stash / sing-box / Surge rendering with routing; empty routing byte-identical
- [x] 5. Model column + repo + handler (pool / save / list / render cache key) + external `subUses` + probe API
- [x] 6. Go tests: golden unchanged, routing per format, caps, validation, injection, fallback, order, ruleset serve/refresh, usage count
- [x] 7. Frontend Part A: 外部节点 pill + dialog (reuse), editor 外部节点 group always + 「＋ 添加」
- [x] 8. Frontend Part B: 分流 tab (desktop + H5), add dialog (常用 / 自己写), bottom sheet, card chip
- [x] 9. `npm run build`, eslint (0 new errors)
- [x] 10. Real clients: mihomo `-t`, sing-box `check`; mainland run on leikwan-sh (first start downloads via DIRECT, all nodes dead, domains → right groups, CN direct, TTFB vs fork.131)
- [x] 11. Render cost (100 nodes + 30 entries + 300 lines) vs fork.131, binary size, panel RSS
- [x] 12. Gallery `/root/mmwx-preview/sub-rules/` (desktop 1366×900 + 390×844, light + dark) + Clash Verge text excerpt
- [x] 13. Ledger row 256 (待发布, waits for screen approval), push branch, CI green, summary

## Results (11-14 TW, build only)
- Commits on `feature/3.0.27-sub-rules` (base `3.0.27-fork.131`): backend 4a2b73ff, tests 98d48d24, frontend 990b7308 +
  8f4e3227, perf 7fb6e56e, PG contract 9a76e738, this doc. CI (backend, agent, frontend, PG contract incl. the new
  `TestPostgresSubRoutingContract`) green.
- Go: `go test ./...` all ok (27 packages). New: routing_test.go (empty list byte-identical for Clash/Stash/sing-box/Surge/
  Surfboard; golden routed files per format; rule order; Gemini/YouTube/… resolve to AI/YouTube not 谷歌 using the embedded
  lists; caps; validation; injection for YAML/JSON/Surge; node-exit fallback; name collision; rule-set snapshot for every
  id × format; text list cleaning), handler sub_link_routing_test.go (save/defaults/old editor keeps list/NULL = [],
  per-origin cache, every catalog file served never 404, refresh queue one download at a time + bad download kept,
  subUses, probe incl. private address never dialled + rate limit), PG contract (column added nullable on upgrade, round
  trip). Old golden files unchanged.
- Real clients: mihomo 1.19.32 `-t`, sing-box 1.12.25 / 1.13.21 / 1.14.3 `check` on routed configs ok.
- Mainland (leikwan-sh, all nodes dead, rule sets from the subscription origin): mihomo 22/22 sets in 5.6 s; sing-box
  1.14.3 / 1.12.25 start after downloading 24 sets (12–15 s over the test tunnel); every test domain in the right group;
  CN TTFB A/B routed vs fork.131-style: baidu 50/48 ms, bilibili 76/79 ms, taobao 20/19 ms.
  Harness note: through a bare SSH tunnel sing-box's 24 parallel downloads tripped the panel's 5 s ReadHeaderTimeout once;
  in production nginx / cloudflared buffer requests in front of the panel (re-run with a buffering proxy: ok). Real
  `https://flvxx.imgamer.top/s/rs/geosite-cn.srs` from Shanghai: 57 KB in 1.8 s.
- Cost: Clash 100 nodes + 30 entries + 300 lines ≈ 0.9 ms / 452 KB / 3.4k allocs per cold render (fork.131 100 nodes:
  158 KB / 1.9k), sing-box ≈ 2.1 ms / 1.16 MB; unrouted subscriptions allocate exactly as fork.131. Binary +0.59 MB
  (gzip +0.31 MB). Panel RSS under the same load 41.1 → 44.6 MB. No ticker, no access log, refresh ≤ 1/day per file.
- Gallery: http://104.145.236.29:8791/sub-rules/ (63 screens, 56/56 checks; the 链式代理 dialog screenshots are byte-identical
  to the fork.131 build in all 4 combos).

## Decisions taken without asking (reported in the summary)
- Stash gets `format: text` lists (Stash documents mrs only in recent versions); mihomo gets `.mrs`.
- Surge / Surfboard use RULE-SET with classical lines (one format for both, `no-resolve` on IP sets except Surfboard).
- sing-box 拒绝 = `"action":"reject"` route rule, no selector (block outbound removed in 1.13).
- 社交 has no geoip sets (domains only, as the brief lists); 流媒体 / Telegram / 谷歌 include their geoip sets.
- 游戏 = `category-games-!cn` only (it contains Steam's download CDN `steamcontent.com`: game downloads go through the
  chosen exit; pick 直连 for 游戏 to keep downloads off the nodes).
- 检测全部 measures TCP connect time from the panel (users without a landing have no other vantage point).
- Saving a `node:` exit whose node is not in the subscription stores 节点选择 (the editor shows that row yellow first).

## H5 展开行（用户 11-14 反馈）

User (11-14 TW, gallery): desktop OK; on H5 the names in some lists are covered by the latency chip / usage tags /
改名 / 删除 buttons (`externals-h5-light.png`). Rule for mobile lists with per-row actions: a row shows only the name +
one small protocol chip (+ tiny status dot) and a round ＋ at the far right (− when open); address, latency, usage tags,
full error text and the action pills live in the expanded area under the row; one row open at a time. Desktop stays
pixel-identical. Task `sub-rules-h5` (Claude high), same worktree/branch, build only.

- [x] h1. Shared H5 component `ExpandRow` (+ accordion hook, field line, action row) — 52 px row, ellipsis name,
      short protocol chip, status dot, ＋/− toggle (≥ 40 px tap target), tinted expanded area at the name indent,
      ≤ 150 ms height animation (none with prefers-reduced-motion), collapsed content inert
- [x] h2. 外部节点 dialog (订阅) — H5 rows via ExpandRow (地址, 延迟 + 检测, 使用情况, 改名 / 删除, blocked delete
      disabled with its reason)
- [x] h3. 落地 链式代理 管理 list — same shared list, H5 only (desktop byte-identical to fork.131)
- [x] h4. 外部订阅源 dialog — H5 rows: name + 「N 个节点」 + red dot on failed update; expanded: masked URL, 更新状态 /
      full error, 立即更新, 删除 (+ inline confirm)
- [x] h5. Editor 「已选」 tab — H5 rows: drag handle + name + type chip; expanded: 协议 + 地址/入口, 来源, 移除;
      drag keeps working, a drag start closes the open row
- [x] h6. Sweep the other H5 screens of the 订阅 feature for covered / clipped text and fix
- [x] h7. `npm run build` + eslint 0 errors (Go untouched)
- [x] h8. Screens `-v2` (390×844 + 360×780, light + dark, collapsed + expanded, ≥ 30-char name) + desktop re-shots
      compared with the current gallery files; gallery index section 「H5 展开行」
- [ ] h9. Commit + push, CI green, ledger row 256 note 「H5 展开行 r2 done」, summary

H5 展开行 results (11-14):
- `src/components/expand-row.tsx` (ExpandRow / useExpandOne / ExpandChip / ExpandField / ExpandActions / shortProtocol),
  used by ExternalManageList (订阅「外部节点」 + 落地「链式代理」管理, H5 branch only), 外部订阅源 SourceRow (H5 branch),
  editor SelectedRow (H5 branch; DndContext onDragStart closes the open row). The 外部节点 「检测」 in the expanded row
  reuses the existing 检测全部 request (no Go change). A blocked delete (链式代理在用) is disabled on H5 with the reason.
- Sweep at 360×780 with ≥30-char names found and fixed 3 H5-only overflows: 订阅 list bottom buttons (「新建订阅」 ran
  off-screen → the row wraps when it doesn't fit), 分流 sheet exit list (long source name pushed the node name out →
  source capped at 45 % + ellipsis), 分流 row 「· N 条」 hidden behind a long exit node name (now always visible);
  可选 filter chip with a long source name wrapped to two lines (H5: one line + ellipsis). 可选 / 分流 otherwise unchanged.
- Checks: H5 90/90 (row geometry: name never overlaps chip/dot/＋, ＋ tap target ≥ 40 px, row ≥ 52 px, short names not
  cut, no horizontal scroll, accordion, drag reorder closes the open row, reduced motion = no transition), sweep 45/45
  (incl. a clipped-text detector that flags the old build's sheet overflow), 0 page errors.
- Desktop: 34 desktop screens shot with the d701e37b build and this build on the same data: 32 byte-identical, 2 differ
  only in the 「最近拉取：昨天 HH:MM」 text (shot a few minutes apart); 链式代理 desktop still byte-identical to fork.131.
  (The older gallery desktop files differ from both builds alike — the demo DB changed after they were shot.)
- Gallery: section 「H5 展开行」 at the top of http://104.145.236.29:8791/sub-rules/ (40 `-v2` H5 screens + 23 sweep
  `-360-v2` screens; old files kept for before/after).
- eslint: 0 errors in changed files (full `src` has 90 errors, all in untouched files = already there); `npm run build` ok.
