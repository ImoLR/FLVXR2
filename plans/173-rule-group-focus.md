# 173 — Rule page tunnel groups: accordion + sticky header + open accent + footer (BUILD ONLY)

Base: tag `3.0.27-fork.98` (prod). Branch `feature/3.0.27-rule-group-focus`. Frontend only, no DB / backend change.
Follow-up to plan 163 (grouped by tunnel, collapsed by default) and 164 (「线路机不合规 N 条」 jump).

## User decisions (2026-10-31 TW)
1. Accordion: opening a group collapses every other open group back to the normal collapsed state (NOT hidden),
   page-wide incl. admin view across users. Tapping an open header closes it. After opening, the opened header is
   scrolled to the top of the viewport; the tapped header never ends up off-screen. Saved state stays the existing
   expanded map with at most one key; a legacy map with several keys shows only the first in display order.
   Filter/search active: unchanged (all matches open, clicks only for that filter session, no accordion).
   164 jump + 163 new-rule reveal follow the accordion.
2. Sticky header for the open group (name, 倍率, hints, N 条, chevron) until the next group scrolls in; tapping it
   collapses and keeps the viewport sensible. CSS sticky only (no scroll listeners).
3. Open group visually distinct: ~3 px calm accent bar (header + body), tinted solid header, bolder title, stronger
   border; collapsed groups as today; a bit more gap between groups. Tinted/flat, less blue (teal accent).
4. Footer 「收起 · <隧道名>」 at the end of an open group: closes it and keeps the collapsed header in view.

## Design notes
- Only the 分组 mode shows tunnel groups (列表 = flat table, 卡片 = flat cards) — nothing to change there.
- `position: sticky` was blocked by `overflow-hidden` on the user card + tunnel group wrappers and, on phones, by
  `.flvx-mobile-shell { overflow-x: hidden }` (makes the shell a non-scrolling scroll container). Fix: `overflow: clip`
  (no scroll container, same clipping) where supported; old browsers keep `hidden` (no sticky, as today).
- Scroll after toggle: one `useLayoutEffect` after the toggle commit keeps the tapped header where it was (no jump),
  then (open, non-filter) scrolls it to the sticky top. Runs only on clicks; no listeners/timers/requests added.

- Desktop: sticky offsets count from inside the layout `<main>` scroller's 24px padding → open header uses
  `top: -24px` there (≥769px) so it pins right under the top bar and matches the scroll target.
- Plan 164 jump keeps its "not saved" rule as a single transient key (`jumpOpenKey`); the next header/footer
  click is saved and ends it (so closing a jump-opened group never pops the saved one open).

## Checklist
- [x] Plan doc
- [x] Accordion state: single open key (derived first-in-display-order for legacy maps), toggle/jump/reveal
- [x] Sticky open header (solid bg, safe-area top on phone, desktop main scroller) + overflow clip fixes
- [x] Open styling (teal bar, tinted header, bold title, border) + more gap; collapsed unchanged
- [x] Footer 「收起 · 隧道名」
- [x] Viewport handling after open/close (header to top on open; anchored on close)
- [x] tsc 0 errors / eslint forward.tsx errors 16 = base 16 (warnings 644 → 646: prettier re-chunking of
      untouched unformatted code, 0 messages in changed lines) / vite build OK
- [x] Demo panel (fork.98 paneld, synthetic DB, `unshare -n`) + headless checks 104/104 + screenshots 390/1440
      light/dark (a–f + jump/create/footer) + WebM recording of d) + gallery :8791 /rule-group-focus/
- [x] Commit + push, branch CI green (5d98b641, CI Build Check run 41138913593: 4/4 jobs success)
- [x] release-notes.md + summary.md / final.md (`/root/flvx-workers/runs/rule-group-focus/`)

Status: BUILT, not released — waits for the user's screenshot approval, then ships in the next batch release
(panel frontend only, no DB change, rollback = previous panel image without `--restore-db`).
