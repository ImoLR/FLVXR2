# 287 — 落地页分组：连接线 + 点开后位置修正（同转发页 / 隧道页）

Task `landing-group-tree` (coordinator 723b32ba, 11-20 TW). Base: tag `3.0.27-fork.155` (619b8341), branch
`feature/3.0.27-landing-group-tree`, worktree `/root/projects/flvxx/flvxx-287`.
**Build + screenshot gallery only** — no merge / tag / release / prod change (user reviews the screens first).

## Goal
User liked the fork.155 落地 page groups (plan 286) and asked for two additions on every 落地 group
(admin 落地服务器 by region, admin 全部节点 by user, user 可用落地 by region, user 我的节点 by server;
desktop + phone/H5, light + dark):

1. Items of an open group hang off a connector line from the group header, like the 转发 (plan 175) /
   隧道 (plan 178) pages.
2. Tapping a group header does the same position correction as 转发 / 隧道 (`utils/group-accordion.ts`):
   opening → header glides to the top; closing → header stays where it was (scroll only if it left the screen).

## Addendum 1 (coordinator 11-20 ~05:00 TW, user 「都补上吧」) — supersedes "no accordion"
Standing rule (memory group-tree-default): every collapsible group list = tree line + position correction +
accordion, desktop + phone, matching the 转发 / 隧道 pages.
- A. 落地 page: accordion on all 4 lists (one open per list; opening B collapses A to its normal header). Remembered
  = one open key per list per account (`flvx:landing:{region,user,my-server}-open:v3:u:<uid>`), v2 open-set keys
  dropped on load → everything starts collapsed once. Jumps open their target group (others collapse) and keep
  their own scrollIntoView + flash.
- B. 订阅 page admin 全部用户 (desktop groups, already one-open + tree line): add the position correction. The H5 view
  is the shared ＋/− expand-row list (already one open, no group header / tree line) → unchanged.
- C. 节点 page 「分组」 view: tree-group look (header / sticky open header / footer 「收起 · 组名」), accordion
  (one open key per account `flvx:node:group-open:v1:u:<uid>`, old all-groups map `node-group-collapsed-state`
  dropped → all collapsed), position correction, tree line. The groups hold the node TABLE (not cards), so the line
  is the rules-page table line through the checkbox column; that column is sticky-left (the table is wider than
  the screen and scrolls sideways) so the line stays under the chevron. 列表 / 卡片 views unchanged.
- D. 利群汇聚 专线 tree (desktop + H5, already one open): only the position correction.
- Shared: `hooks/use-group-scroll-settle.ts` wraps `utils/group-accordion.ts` (unchanged) for these lists.

## Decisions
- (superseded by addendum A: accordion) Scope = only these two things; nothing else on the page changes
  (headers, pills, cards, tables, order, colours, jumps / flash).
- State found at fork.155: the 全部节点 desktop table already uses the rules-page tree cells (line + elbows); the phone
  ＋/− rows already use the rules-page phone tree classes, but the class sat on the bordered row itself, so the line
  had a ~2 px break between rows → wrap each row like the rules page (tree node = plain wrapper, bordered row inside).
  The user 我的节点 desktop card grid had only the header stub (line ended under the header) → cards hang off the line.
  落地服务器 / 可用落地 region groups had no line at all.
- Card grids (region groups, user 我的节点 desktop): the 隧道 page card-tree pattern — a vertical trunk left of the
  grid, an elbow into the first card of every grid row, the first card of the last row ends the line with the
  rounded corner. Column count from the same rules as the grid itself (region grid: container query on the wrapper,
  thresholds of the unchanged `flvx-card-grid` auto-fill 342–370 px tracks; 我的节点: the existing md:grid-cols-2
  viewport breakpoint). Cards keep their size / grid; they only move right by the indent.
- Line colour: teal `--flvx-tree-line` in the teal-framed groups (全部节点, 我的节点 — same as the rules page).
  The region groups (落地服务器 / 可用落地) have a region-tinted header (隧道 page region tones), so their line uses the
  region colour exactly like the 隧道 page region level (same 2 px line, elbow, 7 px rounded end). A teal variant
  is shown in the gallery for comparison (one CSS line to switch).
- Position correction: reuse `group-accordion.ts` unchanged; wrappers get `data-group-key`; a user tap on a header /
  footer queues `buildPendingGroupScroll(key, open, toTop = true, anchor = true)` and a layout effect runs
  `settleGroupScroll` once. Programmatic opens (⚠ 查看, 查看节点, 创建节点, 设置 saved, restored state on load) keep
  their own behaviour (they call `open()` directly, nothing queued) → no double scroll, no jump on load.
- No backend change.

## Checklist
- [x] Worktree from fork.155, disk check (go-build had nothing > 24 h; node_modules = symlinks to the main checkout
      + qrcode from runs/landing-admin-groups/npm-extra, no install)
- [x] Frontend: position correction (data-group-key + queued settle) for region / user / my-server groups
- [x] Frontend: region groups tree line (trunk + per-row elbows, region colour), admin + user, desktop + phone
- [x] Frontend: user 我的节点 desktop cards hang off the teal line
- [x] Frontend: phone ＋/− rows wrapped like rules-page tree nodes (no breaks in the line)
- [x] A: 落地 accordion (v3 single open key per list, v2 dropped, jumps open target + collapse others)
- [x] B: 订阅 admin 全部用户 desktop position correction
- [x] C: 节点 page 分组 view tree groups + accordion + position correction + sticky table tree column
- [x] D: 利群汇聚 专线 position correction (desktop + H5)
- [x] `npm run build` + lint (no new eslint problems vs fork.155: node.tsx 8→7 errors / node-list-view 6→6 are
      pre-existing a11y ones; the other touched files 0/0)
- [ ] Lab (synthetic DB, isolated netns, playwright): every group kind open/close, desktop + 390 + 360, light + dark;
      line geometry checks (elbows on row-first cards, line ends at last row, no overflow at 360 px)
- [ ] Lab: scroll settle frames (open low group → header at top; close long group → header stays); forward + tunnel
      pages scroll exactly as before (same checks on fork.155 dist vs branch dist)
- [ ] Gallery /root/mmwx-preview/landing-group-tree/ (fork.155 before vs after for the first two)
- [ ] Commit + push branch
- [ ] Ledger row 287, memory note, build-cache cleanup, summary
