# 156 — tap-only tooltips (chart tooltips + dropdown chips open on click/tap only)

User 2026-10-28 ~16:00 TW: chart tooltips (e.g. 监控 「连接数」) pop up when a finger swipes over the chart while scrolling,
covering the page; desktop hover shows them too. Want: show only on click/tap, on phone AND desktop.
2nd report ~16:10 TW: 落地 协议 chip (and other dropdown chips) open as soon as a finger touches them (Radix
`DropdownMenuTrigger` opens on `pointerdown`), so a scroll that starts on a chip opens it.

BUILD ONLY — ships in the 10-28 batch release (fork.94 = 151 + 154 + 155 + 156). Base:
`origin/feature/3.0.27-tunnel-single-entry`. Branch `feature/3.0.27-tap-only-tooltips`.

## Design
- `src/hooks/useTapTooltip.ts`: one hook per chart. Recharts 3 `<Tooltip trigger="click">` (hover/touchmove only move the
  hover state, which the click-trigger tooltip/cursor/active dot ignore). The hook keeps an `open` flag:
  click a point → open; click another point → moves; click the same point again → close; pointerdown outside the chart or any
  scroll → close (listeners attached only while open, removed on close/unmount). Closed → `Tooltip active={false}` and
  `Line activeDot={false}` (recharts keeps the last click index in its store, so both must be gated).
- `shadcn-bridge/heroui/dropdown.tsx`: `Dropdown` becomes controlled (open state in context). `DropdownTrigger` composes
  `onPointerDown`: for `pointerType !== "mouse"` it calls `preventDefault()` (Radix skips its pointerdown-open) and remembers
  the pointer type; `onClick` then toggles. Mouse keeps Radix's press-to-open; keyboard (Enter/Space/ArrowDown) unchanged.

## Checklist
- [x] Investigate recharts 3.10 Tooltip trigger/active/activeDot behaviour and Radix trigger pointerdown
- [x] Shared hook `useTapTooltip` + apply to all recharts charts (node monitor metrics + latency, tunnel monitor, dashboard flow)
- [x] Dropdown bridge: touch/pen open on click only
- [x] Sweep other hover-opened popups (list changed / checked-and-left)
- [x] tsc + lint + build (no new errors; bundle delta), backend `go build ./...`
- [x] Playwright proof: phone 390 touch swipe / tap / tap-outside on charts + 落地 chips; desktop 1440 mouse move / click / click-outside
- [x] Before/after frame strip of phone swipe on 监控 连接数; screens 390 + 1440 light/dark in `/root/mmwx-preview/tap-only-tooltips/`
- [x] Commit, push, CI green (run 40665963934: backend, PG contract, frontend, agent all success)
- [x] Summary + 还没解决

## Sweep — changed
- `hooks/useTapTooltip.ts` (new) → used by `pages/node/monitor-view.tsx` (节点指标 CPU/内存/磁盘/网络/负载/连接 chart + 服务监控 延迟
  chart), `pages/node/tunnel-monitor-view.tsx` (隧道流量), `pages/dashboard/components/flow-chart-card.tsx` (主页 流量). These are all
  recharts users (`grep recharts`).
- `shadcn-bridge/heroui/dropdown.tsx` → every `DropdownTrigger`: 落地 `pill.tsx` ProtocolPill (协议 chip), `server-card.tsx` 「+N」,
  `landing/monitor-view.tsx` 在线 IP (desktop branch), `egress-preset-picker/-profile-editor/-rule-list`, node badges
  `ipv6-ra-badge`, `admin-notice-badge` (⚠ 节点提示, plan 154), `ddns-badge`, `leikwan-badge`, `machine-conn`, node list/monitor
  action menus, `tunnel.tsx`, `admin-payment.tsx`, `layouts/admin.tsx` + `h5.tsx` (user menu), `date-presets.tsx`.

## Sweep — checked and left
- `shadcn-bridge/heroui/select.tsx`: not Radix; opens on `click` (custom list) / native `<select>` → already tap-only.
  (`components/ui/select.tsx` Radix Select is unused by pages and Radix Select already opens touch on click.)
- `components/ui/tabs.tsx` (Radix Tabs, `onMouseDown`): switches tabs, no popup; touch compat mousedown only fires after a tap.
- `accordion.tsx`, `modal.tsx`, `checkbox/radio/switch`: click-driven, no hover content.
- `pages/node.tsx` 节点信息 button: `onMouseEnter`/`onFocus` only pre-compute the popover placement; opens on click.
- `pages/node/node-list-view.tsx` expiry popover: opens on click, closes on click/scroll/resize.
- `pages/limit.tsx`, `pages/forward.tsx` `onPointerDown`/`onTouchStart`: only stopPropagation (drag handles).
- `node-deploy-modal.tsx` `group-hover:` only recolours an icon. No CSS hover-to-show rules in `styles/`.
- Native `title=` attributes (select section tooltips, chips) stay — never shown on touch.

## Results
- tsc 0 errors; eslint 0 errors (monitor-view.tsx has 267 pre-existing prettier warnings in a mis-indented block at
  L1920–2190 on the base; my 8 new lines inside it follow the block's indentation → 275); other changed files 0 warnings.
- vite build OK; main bundle 3,006,095 → 3,007,848 B (+1.75 KB, gzip +0.6 KB). `go build ./...` OK (backend untouched).
- Resource use: no timers; per chart one `pointerdown` + one `scroll` document listener, attached only while its tooltip is
  open and removed on close/unmount. Dropdown: one ref + one state per dropdown, no listeners.
- Proof (Playwright, demo panel in no-network netns on a prod-copy DB, base vs new build): 176/176 checks, 0 page errors.
  Base reproduces both bugs (finger swipe over 连接数 pops tooltip + cursor + dots; swipe starting on 协议 chip opens the menu).
  New: phone 390 touch — vertical swipe scrolls with nothing shown, horizontal drag shows nothing, tap shows, tap other point
  moves, same point closes, tap outside closes, scroll closes, only one chart open at a time; desktop 1440 — mouse move shows
  nothing, click shows/moves/closes, click outside + wheel close. Chips (协议, +N, 整机连接, DDNS on 390; 在线 IP on 1440+touch):
  swipe from chip scrolls and opens nothing, tap opens, tap outside / tap trigger again closes; mouse click + Enter/↓/Esc work.
  Gallery `/root/mmwx-preview/tap-only-tooltips/` (44 shots incl. before/after strip).

## Still open
- Tapping a chart focuses recharts' `svg.recharts-surface` (tabIndex for its keyboard layer), so the browser draws a focus
  outline around the chart; same on the base build (pre-existing), more visible now that tap is the only way. Left unchanged
  (UI look unchanged rule); a one-line CSS `.recharts-surface:focus:not(:focus-visible){outline:none}` would hide it if wanted.
