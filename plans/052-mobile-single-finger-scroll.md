# 052 - Fix single-finger scrolling on newer Android browsers

## Problem
On newer Android phones, the H5 layout (dashboard, forward rules, node list) cannot be
scrolled with one finger; only two-finger gestures move it. Older phones work.

Reproduced with headless Chromium in Android mobile emulation against a local backend:
a single-finger swipe leaves `window.scrollY` and `body.scrollTop` at 0.

Root cause:
- The mobile shell rule `html, body, #root { overflow-x: hidden }` (and the global
  `html, body` rule) turns all three into scroll containers (`overflow-y` computes to
  `auto`). `body` is `height: 100%`, so it becomes the real scroller.
- `index.html` sets `overscroll-behavior: none` on `html`, `body` and `#root`.
- A touch scroll starting in the page content chains outward and reaches `#root`, a scroll
  container with nothing to scroll and `overscroll-behavior: none`, so the chain stops
  before `body`. Newer Chromium applies this strictly; older versions did not.

Verified by injection: `#root { overscroll-behavior: auto }` lets `body` scroll, and
`overflow-x: clip` on `html, body, #root` makes the document scroll normally with no
horizontal overflow.

## Approach
- In the mobile shell, override `html, body, #root` to `overflow-x: clip` inside
  `@supports (overflow-x: clip)`. `clip` hides horizontal overflow without creating a scroll
  container; browsers without `clip` support keep the previous `hidden` behavior. (A plain
  duplicate `overflow-x: hidden; overflow-x: clip;` is collapsed to `clip` by the build,
  dropping the fallback.) Desktop rules are unchanged.
- `GlobalPullToRefresh.getScrollTop` also falls back to `document.body.scrollTop`, so when
  `body` is the scroller (fallback browsers) a scrolled page is not treated as at the top.

## Tasks
- [x] Use `overflow-x: clip` for `html, body, #root` in the mobile shell rule
- [x] Include `document.body.scrollTop` in the pull-to-refresh scroll-top fallback
- [x] Re-run the emulated single-finger swipe test on dashboard, forwards, nodes (all scroll
  down and back up; no horizontal overflow; desktop wheel scrolling unchanged)
- [x] Build the frontend (output keeps the `hidden` fallback plus the `@supports` clip override)
- [ ] Publish the independent `3.0.27-fork.7` release from the fork.6 line without changing earlier tags
- [ ] Verify the CI build and all `3.0.27-fork.7` release assets
- [ ] Upgrade only the production FLVXX frontend/backend to `3.0.27-fork.7` with a validated
  rollback point, and confirm the served frontend contains the fix
