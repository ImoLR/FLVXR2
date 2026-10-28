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
- [ ] Shared hook `useTapTooltip` + apply to all recharts charts (node monitor metrics + latency, tunnel monitor, dashboard flow)
- [ ] Dropdown bridge: touch/pen open on click only
- [ ] Sweep other hover-opened popups (list changed / checked-and-left)
- [ ] tsc + lint + build (no new errors; bundle delta), backend `go build ./...`
- [ ] Playwright proof: phone 390 touch swipe / tap / tap-outside on charts + 落地 chips; desktop 1440 mouse move / click / click-outside
- [ ] Before/after frame strip of phone swipe on 监控 连接数; screens 390 + 1440 light/dark in `/root/mmwx-preview/tap-only-tooltips/`
- [ ] Commit, push, CI green
- [ ] Summary + 还没解决
