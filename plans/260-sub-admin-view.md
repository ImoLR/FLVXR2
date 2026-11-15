# 260 — 订阅页 admin 只读「全部用户」视图（build + gallery）

Base: tag `3.0.27-fork.133`, branch `feature/3.0.27-sub-admin-view`, worktree `flvxx-sub-admin`.
Build only — no tag, no release, no prod change. Panel only, no DB change, no OTA.

## Goal (user 11-15 TW, approved design 「按你说的做」)
Admins see, on the same 订阅 page, which users created which subscriptions and, per
subscription, its nodes and 分流. Read-only. Non-admin page and the admin's 我的订阅 tab stay
exactly as fork.133.

## Design
- Admin only: segmented switch 「我的订阅 ｜ 全部用户」 at the top (default 我的订阅, choice kept per
  browser in localStorage, try/catch).
- 全部用户 tab: summary line `N 位用户 · N 条订阅 · 24 小时内有拉取 N` + 搜索用户名 (client-side);
  users accordion (one open, collapsed by default, rules-page teal accent + sticky header), sorted by
  latest pull (never pulled last); row = dot · username · `N 条订阅 · 外部订阅源 N` · relative time · ＋/−.
  Dot: green ≤24 h, grey 1–7 d, hollow grey >7 d / never.
- Expanded user: one line per subscription `名字  客户端 · N 个节点 · 分流：短词  相对时间  查看 ›`,
  then one line per external source `名字 · N 个节点 · 最近更新 OK/失败` (error text in a tooltip; H5
  in the expand area).
- 查看 → read-only dialog (desktop modal, H5 full-screen like the editor): title `用户名 / 订阅名`,
  facts 格式 / 最近拉取 / 创建时间; tabs 节点 (items in order, source chip + protocol chip, 整组 row,
  已失效 muted) and 分流 (same visuals as `routing.tsx`, read-only, fixed rows, 我的规则 counts; NULL →
  muted 「未设置分流（使用旧版规则…）」 + rule mode).
- H5: user row = username + `N 条订阅` chip + dot + ＋ (shared ExpandRow).

## API (admin only, role==0 checked server-side, others → 403 「权限不足，仅管理员可操作」)
- `POST /api/v1/sub/admin/list` → `{enabled, users:[{id,name,subs:[…],sources:[…]}]}`. One pass:
  3 queries (subscriptions w/o token, sources w/o url/cache, user names). No render, no resolution.
- `POST /api/v1/sub/admin/detail {id}` → one subscription: refs resolved with the **owner's**
  permissions via `subLoad(ctx, owner, refs, false, refresh=false)` (never fetches a source).
- Never returned: token/link, source URL / urlHint, node credentials, server addresses/ports, ref keys
  (a forward-link key holds the entry address — node exits are mapped to node names server-side).

## Decisions (worker, to confirm with the user)
- Summary `N 个节点` = item count with a followed source counted as its node count (cheap, no
  resolution); deleted refs are only detected in 查看 (shown 已失效).
- `24 小时内有拉取 N` counts subscriptions pulled in the last 24 h.
- 分流 短词: empty list (NULL **or** `[]`, both render the old output) → 「未设置」; exactly the default
  8 builtins with default exits → 「默认 8 组」; else 「自定义 N 组」. 查看 shows the 未设置 line for both.
- Source error text: URL-looking parts are stripped (defence in depth; fetch errors are already friendly).
- Desktop open user = rules-page group look (teal accent, sticky header) incl. its tree line from under
  the status dot into every subscription / source line; no 「收起」 footer (≤ 7 lines per user).
- H5 expanded user shows the full 用户名 line only when the name is long (> 14 chars, truncated in the row).
- `/api/v1/sub/admin/` is also in the JWT middleware's admin-only prefixes (403 before the handler).
- The 全部用户 view is a lazy chunk (`admin-view-*.js`, ~15 kB): non-admins never download it.
- 查看 「节点」 on H5 uses ExpandRow like the editor's 「已选」 (name + one kind chip; 协议 / 来源 inside).

## Results (build)
- `go test ./...` EXIT 0 (incl. new `TestSubAdminView`, `TestSubRoutingKind`, `TestSubAdminViewContract`);
  local PG 18 run of `TestSubAdminViewContract` / `TestSubLinkContract` / `TestPostgresSubRoutingContract` PASS.
- `npm run build` OK; eslint on the changed files 0 problems. sublink golden tests untouched.
- Gallery 82 screens, 102 automatic checks all pass: non-admin 我的订阅 = fork.133 DOM-identical and
  pixel-identical (desktop / 390 / 360, light / dark); admin 我的订阅 DOM-identical below the switch and
  pixel-identical with the switch hidden; API responses free of tokens / URLs / credentials / addresses.
- Demo latencies (synthetic SQLite): admin/list ≈ 1 ms (3 queries), admin/detail 1–11 ms.

## Checklist
- [x] Backend: repo `AdminSubscriptionRows` / `AdminSubSourceRows` (column-limited, no token/url/cache)
- [x] Backend: handler `sub_link_admin.go` (`admin/list`, `admin/detail`), role check, enabled handling
- [x] Backend tests: handler unit test (admin OK, user denied, no secrets, no source fetch, routing labels)
- [x] Contract test sqlite + postgres (admin OK, user denied, no secret fields)
- [x] Frontend: api types + calls (`api/sub-link.ts`)
- [x] Frontend: admin switch in `sub-link.tsx` (non-admin markup unchanged)
- [x] Frontend: `sub-link/admin-view.tsx` (summary, search, accordion, sub/source lines, H5 ExpandRow)
- [x] Frontend: `sub-link/admin-detail.tsx` (节点 / 分流 tabs, read-only routing list)
- [x] `go test ./...`, `npm run build`, eslint 0 errors; sublink golden tests untouched
- [x] Local dev backend (free port, temp DB) + seed ≥5 users fake data
- [x] Gallery `/root/mmwx-preview/sub-admin-view/` (index + all required screens, light/dark,
      1440×900 / 390×844 / 360×780, long name) + fork.133 before/after compare for 我的订阅
- [ ] Commit + push, CI green (incl. PG contract)
- [ ] Ledger row 260 in `/root/flvx-workers/RELEASE-LEDGER.md` (待发布)
- [ ] Clean temp files / dev servers; summary.md
