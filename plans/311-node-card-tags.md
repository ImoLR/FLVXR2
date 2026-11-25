# 311 — 节点卡片：小标签移到地区行 + ⓘ 弹窗显示备注

User 2026-11-25 (节点 page). Frontend only, no backend / agent / DB change. Build + screenshots only;
release waits for the user's OK on the screenshots, then joins the next batch with plan 310.

## A. 小标签不再挤掉节点名称（「移到地区那行」）
- Card (`vite-frontend/src/pages/node.tsx` `renderNodeCard`): name row = status dot + name + admin ⚠
  (`NodeAdminNoticeBadge`, `compact` logic unchanged). IPv6 RA / DNS 缓存 / 利群 / DDNS / 专属域名 / 线路机
  move to the region line (region · city first, tags after, `flex-wrap`). Order, colours, sizes and
  click / dropdown / dialog behaviour unchanged.
- List view (`vite-frontend/src/pages/node/node-list-view.tsx`): same — name cell keeps dot + name + ⚠,
  the tags go on the region line under the name.

## B. ⓘ 弹窗也显示备注（「点开后也显示备注」）
- Card ⓘ popover shows the 备注 (above the expiry part; wraps; click to copy, toast 「备注」).
  Only 备注 → popover shows only the 备注. Expiry-only popover unchanged.
- Red dot meaning unchanged (= has 备注); ⓘ button gets `title` 「备注 · 到期时间」 / 「备注」 / 「到期时间」.
- Card-bottom 备注 line unchanged.
- List view: has its own 备注 column and the expiry chip popover has no red dot → no gap, unchanged.

## Checklist
- [x] Read code (card + list view + badges), plan doc
- [x] A: card name row / region line
- [x] A: list view name cell
- [x] B: card ⓘ popover 备注 + title
- [x] `npm run build` + tsc + lint (as CI)
- [x] Lab (fork.173 paneld + synthetic DB, node/list fields patched in the browser) before / after screenshots:
      desktop + 390 px, light + dark; long name + many tags, no tags, popover 备注+到期 / 只备注 / 只到期; list view
- [x] Overflow check (no horizontal overflow in narrow cards / 390 px)
- [x] Gallery /root/mmwx-preview/node-card-tags/ (http://104.145.236.29:8791/node-card-tags/)
- [ ] Commit + push branch `feature/3.0.27-node-card-tags`, CI green
- [ ] Ledger row 311 (待发布, waits for screenshot OK), summary, cleanup (lab servers stopped, build leftovers)

## Notes (build)
- H5 pre-existing bug found and fixed: `.flvx-mobile-main .rounded-xl.border { width: 100% !important }` (mobile glass
  cards, globals.css) also matched the ⓘ popover → on phones it was 28 px wide (expiry text spilled out). Added an
  exception for `[data-node-info-popover]` (width: max-content); glass look unchanged.
- ⓘ popover with 备注 opens top-aligned to the button (left placement) so it doesn't climb above the card; expiry-only
  popover keeps the old classes (centered, nowrap).
- Lint: eslint on the two files = 13 errors before and after (all pre-existing), warnings 86 → 81. CI runs `npm run build` only.
- Lab checks (runs/node-card-tags/lab/checks.json): after = all OK (names visible, no visible overflow at 1440 / 760 / 390,
  popover texts + titles, popover inside viewport, copy → toast 「备注已复制到剪贴板」 + clipboard, DDNS dropdown + 专属域名
  dialog still open from the new spot). Before fails only on the bug itself (long-name card overflow, name 8 px wide).

## Release needs
Panel only (frontend image), no DB change, no OTA. Waits for the user's screenshot approval.
