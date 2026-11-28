# 323 — 主入口 picker shows every entry + 利群专线端内 legend = range 平均

Base: fork.178 release branch head `a4547740` (= prod code).
Branch: `feature/3.0.27-323-entry-select-endin-avg` (worktree `/root/projects/flvxx/flvxx-323`).
**Build only** (user 2026-11-28: 「先别发布，到时等几个一起发布」) — no tag, no release, no prod change.
Panel only (frontend only), no DB schema change, no agent OTA.
Work dir: `/root/flvx-workers/runs/entry-select-endin-avg/work`.

## A. 利群汇聚 → 授权 → 换主入口 / 修改: 「火山广州」 never visible

### Finding (prod, read-only Playwright, coordinator 11-28)
- `leiKwanEntryNodes` returns all 4 entries (火山上海, 阿里广州, 百度云广州, 火山广州 last).
- Bridge Select, grouped listbox (`select.tsx` `resizeListbox`): `maxHeight = min(60vh, 384, available)`
  with `available` measured to the **dialog's** bottom. The listbox is absolutely positioned inside
  ModalBody (`scrollBehavior="inside"` → `overflow-y-auto`), which ends above the footer and clips it:
  listbox 248 px = its whole content (never scrolls itself), option 4 (火山广州) fully under the
  ModalBody clip, option 3 half cut.

### Fix (generic, grouped listbox of the bridge Select)
- **Inside a dialog** the listbox is pinned with `position: fixed` exactly where its CSS layout puts
  it (same left / width — keeps e.g. the forward picker's `md:w-[calc(200%+0.5rem)]`). The dialog
  content has a `translate` → it is the fixed containing block, so the scrolling ModalBody no longer
  clips the list; it may cover the footer like any dropdown. Placement by "measure where top/left 0
  lands, then offset" (works for any containing block). Re-placed on window resize / any scroll
  (not on the list's own scroll); closes when its trigger is scrolled out of sight.
- Room is measured to the clip that really applies (`visibleBand`: viewport ∩ every
  `overflow-y ≠ visible` box from the fixed containing block / the select's parent up, stopping at a
  fixed box), not to the dialog box.
- Flip to the other side only when the preferred side is shorter than the list **and** than 200 px
  (~3 rows) and the other side is bigger; otherwise unchanged. The side's room caps `maxHeight`, so
  the list scrolls inside itself when even that is short.
- In-flow lists (`classNames.listbox` "static": landing rule-target / external-target pickers) and
  page-level lists (not in a dialog, stay absolute) keep their layout.
- Same bug found and fixed by this: 编辑专线 →「备线隧道」 last option 「不建备线」 was under the clip.

### Tasks
- [x] Reproduce on a local build (prod API responses captured read-only, replayed into the
      unmodified fork.178 dist in headless Chromium — same numbers as prod) — before shots
- [x] Fix in `vite-frontend/src/shadcn-bridge/heroui/select.tsx`
- [x] Verify 换主入口 / 修改 dialogs: every entry visible or reachable by scrolling the list at
      1440×900, 1366×768, 390×844, 390×844 @125 % font (+ simulated 5th entry = App 手机本地);
      offline entries listed + disabled
- [x] Other grouped Selects unchanged where nothing was clipped (添加转发 选择隧道 desktop
      pixel-identical, phone ≤1 px; 修改 dialog only a 1 px sub-pixel border column); 备线隧道 fixed
- [x] Interaction checks (wheel, Escape, keyboard, click-select, dialog stays open, list follows
      the trigger when the body / dialog scrolls, keeps its own scroll, closes when the trigger
      leaves): after 15/15, base fails the 3 visibility cases

## B. 线路监测 「利群专线端内」 legend pill = range 平均

User 11-28: the pill next to each 端内 line name (legend above the chart) equals the table's 「当前延迟」;
he wants it to show the table's 「平均」 (mean of the selected range, `LineStats.Avg`).

### Fix (frontend only, `vite-frontend/src/pages/node-monitor/chart-card.tsx` legend)
- 端内 lines (`kind = lk_endin`): pill = `stats.avg` of the loaded range (follows range / zoom /
  custom like the table), tooltip 「所选时间段平均，当前 X ms」. No avg (no data) → today's status pill.
- TcpPing lines, the table, sample storage, 利群汇聚 「端内」 tag and 诊断 unchanged.
- `ChartCard` is the only legend renderer (admin page + plan 321 user view) → both follow.

### Tasks
- [x] Legend change in `chart-card.tsx` — pill 「平均 X ms」 (tone = live status, slate when stale),
      tooltip 「所选时间段平均，当前 X ms」 / 「…，当前无数据·reason」
- [x] Screenshots before/after (1440 + H5 390) of chart 「利群专线端内」, admin + user view; pills =
      table 平均 for 1h and 1d; no-avg fallback = 无数据·reason; TcpPing charts unchanged

## Common
- [x] `npm run build` (tsc + vite) ok; eslint 0 errors, no new warnings (select.tsx 35 → 31)
- [x] Screenshots gallery `/root/mmwx-preview/plan-323/` (http://104.145.236.29:8791/plan-323/)
- [ ] Commit, push branch (own upstream), CI green
- [ ] Ledger row 323 in `/root/flvx-workers/RELEASE-LEDGER.md` 待发布 (panel only, no DB, no OTA)
- [ ] Clean build output / caches
