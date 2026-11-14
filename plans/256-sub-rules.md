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
- [ ] 6. Go tests: golden unchanged, routing per format, caps, validation, injection, fallback, order, ruleset serve/refresh, usage count
- [ ] 7. Frontend Part A: 外部节点 pill + dialog (reuse), editor 外部节点 group always + 「＋ 添加」
- [ ] 8. Frontend Part B: 分流 tab (desktop + H5), add dialog (常用 / 自己写), bottom sheet, card chip
- [ ] 9. `npm run build`, eslint (0 new errors)
- [ ] 10. Real clients: mihomo `-t`, sing-box `check`; mainland run on leikwan-sh (first start downloads via DIRECT, all nodes dead, domains → right groups, CN direct, TTFB vs fork.131)
- [ ] 11. Render cost (100 nodes + 30 entries + 300 lines) vs fork.131, binary size, panel RSS
- [ ] 12. Gallery `/root/mmwx-preview/sub-rules/` (desktop 1366×900 + 390×844, light + dark) + Clash Verge text excerpt
- [ ] 13. Ledger row 256 (待发布, waits for screen approval), push branch, CI green, summary
