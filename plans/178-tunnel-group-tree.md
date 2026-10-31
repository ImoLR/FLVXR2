# 178 隧道页分组：默认折叠 + 一次只开一个 + 规则页同款树形线（BUILD ONLY）

Base: tag `3.0.27-fork.101` (prod). Branch `feature/3.0.27-tunnel-group-tree`. Frontend only.
Reference: rules page (`pages/forward.tsx`, plans 163 / 173 / 175) — copy the shipped look exactly (user 10-31:
「就用我们刚刚上线的规则那边显示的那种 UI 那种设计就可以了」), adapt only where the tunnel page's structure needs it.

## Scope
- Grouping modes with groups: `region` (入口 → 出口地区 → 隧道, 2 levels) and `custom` (隧道分组 → 隧道).
  `custom` was a flat list with a group column/filter until now; it gets group headers (tunnel_group order, 「未分组」 last).
- `none` mode, buttons, drag-sort, batch actions, edit dialog, diagnose, region pills, grouping switch: unchanged.

## Tasks
- [x] Baseline: demo panel (fork.101 backend, synthetic SQLite, loopback-only netns), base build + baseline screens.
- [x] Shared group pieces: `components/tree-group.tsx` (header / chevron / footer / class helpers) and
      `utils/group-accordion.ts` (scroll helpers) moved out of `forward.tsx` — rules page DOM byte-identical to fork.101.
- [x] Tunnel page: top-level groups in their own containers (header / body / footer) for card + list view.
- [x] All collapsed by default; accordion (one open, others collapse, not hidden); tap open header closes;
      after opening, the group scrolls to the top; open key in localStorage `tunnel-group-open:v1:u:<uid>` per mode.
- [x] Region mode: exit-region sub-headers default OPEN inside an open entry, still collapsible; 2-level tree line
      (desktop cards + table on all widths; phone cards keep the region level only so cards stay ≥ 286px wide).
- [x] Open styling: teal accent bar, tinted sticky header (desktop + H5 offsets, light + dark), 「收起 · 组名」 footer.
- [x] Filter mode (search / region pills / group filter): every matching group open, clicks only for that session.
- [x] No repeated info inside grouped cards (region chip under its region, group chip under its group; multi-region
      chip kept) — display only, tunnel data/names untouched. Table columns unchanged.
- [x] Drag-sort in custom mode keeps working (within a group; a drop on another group is ignored).
- [x] Shared scroll fix: switching from a very long open group measured the new (sticky) header at its pinned spot and
      left the group start above the screen; the helper now uses the group's top edge (rules page benefits too).
- [x] tsc / build / eslint: no new errors vs base (forward 16 / tunnel 4 errors = fork.101; warnings 639 / 1273 vs 639 / 1277).
- [x] Screens 390 + 1440, light + dark (a–h), gallery `/root/mmwx-preview/tunnel-group-tree/` with Chinese captions
      (59 screens, 175 checks pass).
- [x] Push branch, CI green (code `28a24c6f`: run 41206394219, 4/4 jobs success).
- [x] `runs/tunnel-group-tree/release-notes.md`, summary/final.md.

## Resource efficiency
- No new polling, timers, API calls or scroll listeners (measured: scroll 3 / resize 5 / interval 0 = fork.101; 8 API
  calls on load, same endpoints). Tree line is pure CSS (pseudo-elements, one container query for the card columns).
- Collapsed groups render nothing: page DOM 226 nodes all collapsed / 708 with one entry open vs 1782 before (desktop).

## Notes
- BUILT 10-31 21:40 TW; not released (ships in a batch after the user approves the gallery).
- Phone cards inside the tree use 16px side padding (was 24px) so 「1入口 › 0跳 › 1出口」 stays on one line at 375–390px.
- Verification: 175 headless checks (geometry of lines/elbows vs chevrons and checkboxes, accordion, scroll-to-top,
  sticky, reload memory, sub-groups, filters, drag-sort, no sideways overflow, DOM identity of rules page and 不分组
  view vs fork.101, listener counts) — all pass, 0 page errors.

## Round 2 (user feedback 10-31 TW): region level = real second level
User: entry level fine (teal frame, sticky entry header); in 「按入口/地区」 the exit-region level looks weak. Coordinator:
ONE warm amber colour for every region (no per-region rainbow). Same branch, BUILD ONLY, frontend only.

### Tasks (round 2)
- [x] Region boxes: each exit region of an open entry is its own box inside the teal entry box — thin amber border +
      amber left accent bar + tinted amber header (flag/name, N 个隧道, chevron); one amber for all regions, light + dark.
- [x] Region header sticky below the pinned entry header (navbar → entry → region), CSS only: the open entry header in
      region mode gets a fixed height (CSS var), region `top` = entry sticky top + that height (desktop + H5 offsets);
      next region replaces it, both release at the entry end. No scroll listeners / ResizeObserver.
- [x] Tree lines: teal entry line → short teal elbows into each region header (rounded └ on the last region);
      tunnels hang off the REGION header with the variant-A line in amber inside the amber box (cards + table).
- [x] Table view: one table per region box (the horizontal scroller sits below the sticky region header, so the
      header can pin); column header row + 本地区全选 per region box.
- [x] Regions default open with their entry, each collapsible (collapsed = amber header only, not sticky); no region
      footer (the box border already closes it; an extra 「收起 · 地区」 row looked crowded); entry footer stays.
- [x] Phone 390: 「1入口 › 0跳 › 1出口」 must not wrap — phone card view: the amber box is level 2 (teal line to the
      boxes, no amber card lines: a second indent leaves ~250px cards, the line wraps); cards in a region box use 12px
      side padding + 6px gap in that line (`data-tunnel-chain` hook) → 278px cards, one line. Phone table: both lines.
- [x] 自定义分组 / 不分组 / rules page unchanged vs round 1 / fork.101 (DOM check: byte-identical except the
      `data-tunnel-chain` attribute on cards).
- [x] tsc / build / eslint no new errors (tunnel.tsx 4 errors = fork.101, warnings 1272 vs 1273); no new timers /
      polling / API calls / scroll listeners / ResizeObservers (scroll 3, resize 5, interval 0, RO 5 = round 1).
- [x] Screens 390 + 1440, light + dark (a–f + unset-region entry), 37 screens, 202 checks pass, 0 page errors;
      gallery 「第 2 轮」 section on top of round 1; release-notes updated (trial merge into fork.102 clean).
- [x] Push, CI green (code `f2f954aa`); summary → runs/tunnel-group-tree-r2/{summary,final}.md.

### Notes (round 2)
- BUILT 10-31 ~23:55 TW, not released (ships in the next batch after the user approves the gallery).
- Table view trade-off: one table per region (the sideways scroller would stop the region header from sticking), so
  every region box has its own column header + 「本地区全选」 (no entry-level 「本组全选」 in region mode) and columns
  of different region tables are sized by their own content (not perfectly aligned between regions).
