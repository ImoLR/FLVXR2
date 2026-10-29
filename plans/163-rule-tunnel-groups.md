# 163 — rule-tunnel-groups: 规则页默认按隧道名分组 + 分组默认折叠 (BUILD ONLY)

User 2026-10-29 (TW evening): 「規則顯示的方式也要改一下，把它顯示成那種以隧道名來分組的，然後默認是不要打開的，這種看起來比較直觀」.

Branch `feature/3.0.27-rule-tunnel-groups` from `origin/maintenance/3.0.27-fork.95-limits` (= prod fork.95).
Build only: no tag, no release, no prod writes, no OTA. Frontend only (`vite-frontend/src/pages/forward.tsx`), no backend / DB change.

## Facts (fork.95, verified in code before the change)

- Three layouts, cycled by the mode button: 分组 (`!compactMode`: user card → one section per tunnel name, collapsible,
  drag-sortable; desktop table / phone cards inside) → 列表 (`compactMode && viewMode=grouped`: one flat table / card list, no
  tunnel grouping) → 卡片 (`compactMode && viewMode=direct`: card grid).
- `compactMode` came from the GLOBAL config `forward_compact_mode` (admin-written, read by everyone; its settings switch is
  hidden in config.tsx) and was forced on for phones without a saved `forward-view-mode`; `viewMode` defaulted to `direct`.
  So phones landed on 卡片 and desktops on whatever the admin last picked.
- Group open/closed state: `forward-group-collapsed:u:<uid>` (localStorage) + admin bucket in config
  `forward_group_collapsed_map`; `=== true` = collapsed, so missing = expanded.
- Group header rule count was commented out upstream (「保持界面简洁」).
- Pagination of the 分组 layout is by USER sections (5/10/20 per page), never splits a tunnel section → no change needed.
- Backend: admin may read/write any config key; `update-single` accepts unknown keys (no whitelist) → a new key needs no
  backend change.

## Design

- Layout per user and browser: `forward-display-mode:v2:u:<uid>` = `grouped | list | card`; missing → `grouped` (the
  tunnel-grouped 分组 layout) on desktop and phone, admin and normal users. Old `forward-view-mode` is ignored and removed;
  the global `forward_compact_mode` config is no longer read or written by the rules page (each user keeps their own choice).
- Open groups: `forward-group-expanded:v2:u:<uid>` (+ admin bucket in new config `forward_group_expanded_map`), stores
  EXPANDED keys only → missing = collapsed, new tunnels start collapsed. Old collapsed keys removed / ignored.
- Collapsed groups do not mount (or even build) their rows.
- Header: tunnel name + ratio chip (as before) + grey 「N 条」 + 「N 条已暂停」/「N 条异常」 when > 0 + drag handle.
- Filter active (= the 重置 button shows: keyword, name, port, target, specific user, tunnel, speed limit) → every shown group
  is open; clicks while filtering only close/open for this filter session (not saved); clearing returns to the saved state.
- New rule (normal and 「我自己的节点」 chain create) → its group opens and is saved as open.
- Admin on desktop in 分组: the 「所属用户」 picker (already shown on phones) is shown above the groups too, because the table
  headers that carry it are hidden while groups are collapsed.
- Phones: ratio chip + hints go under the tunnel name (name keeps the full width, wraps at words); desktop header unchanged
  apart from the new count/hints on the right. After 新增 the opened group is scrolled into view if off screen.

## Results

- Commits: 0c50ce06 (feature), c111a78a (phone header + scroll into view), fa6e3e34 (phone title word wrap).
- Checks on the demo (synthetic data, fork.95 backend binary, built frontend): 68/68 — default 分组 + all collapsed
  (user/admin, 390/1440, light/dark), old saved `forward-view-mode=direct` → still 分组 and legacy keys removed, open one
  group survives reload and closing empties the map, keyword filter opens matching groups / click during filter not saved /
  重置 returns to the saved state, admin 全部用户 + desktop picker + open state written to `forward_group_expanded_map`,
  新增 opens the new rule's group, mode button 分组→列表 (kept after reload)→卡片→分组. 0 page errors.
- Render, 300 rules / 40 tunnels (headless Chromium, desktop 1440): before (fork.95 default 分组, all open) 300 rows mounted,
  16 507 DOM elements, 26 MiB JS heap, one 72 ms long task per list refresh (15 s poll); after: 40 headers, 0 rows,
  ~800 DOM elements, 12 MiB heap, 0 long tasks per refresh, opening one group ~36 ms. Phone 390: ~820 elements (before: 卡片
  page of 10 = 765). No new timers/polling; page load does one request fewer (`forward_compact_mode`).

## Tasks

- [x] Plan doc
- [x] Display mode: versioned per-user key, default grouped, drop global compact config read/write, remove old key
- [x] Expanded map: new local key + admin config key, missing = collapsed, toggle / persist, old keys removed
- [x] Collapsed groups don't build/mount rows
- [x] Header count 「N 条」 + paused/abnormal hint
- [x] Filter auto-expand (session-only overrides, reset when filter changes/clears)
- [x] Reveal group of a newly created rule
- [x] Admin desktop user picker in grouped mode
- [x] Pagination check: 分组 pages by USER sections (5/10/20 per page) and never splits a tunnel section → no change
- [x] tsc / lint / build: no new errors vs base (tsc 0 → 0; eslint forward.tsx errors 17 → 16, warnings 676 → 645; vite build ok)
- [x] Local seeded demo panel (fork.95 paneld + synthetic SQLite, loopback-only netns) + screenshots 390/1440 light/dark → gallery `/root/mmwx-preview/rule-tunnel-groups/` (32 shots, 68/68 checks)
- [x] Render cost measured (300 rules / 40 tunnels)
- [x] Push, branch CI green (CI Build Check: 40941220340 @0c50ce06, 40942569980 @c111a78a, 40943116130 @fa6e3e34 — all 4 jobs success)
- [x] `runs/rule-tunnel-groups/release-notes.md` + summary/final.md

Status: BUILT, not released (batch release later; panel only, no DB change).
