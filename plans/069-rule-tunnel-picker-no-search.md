# 069 — Rule form tunnel picker without search box (fork.21)

## Goal
In the rule create/edit dialog, opening 「选择隧道」 no longer shows the
「搜索入口、地区或隧道」 search input (users rarely need to search and the input
pops up the keyboard on mobile). User request 2026-10-06.

## Change
- `vite-frontend/src/pages/forward.tsx`: drop `isSearchable` / `searchPlaceholder`
  from the form tunnel `Select`. The bridge then renders the same native
  `<select>` as the other form selects (转发模式 etc.) and the list filter;
  entry→exit region groups stay as `<optgroup>`s. Frontend-only, no API/DB change.

## Tasks
- [x] Remove the search props from the form tunnel picker
- [x] `tsc --noEmit` + `npm run build` pass
- [x] CI Build Check green ([37445966728](https://github.com/ImoLR/FLVXR2/actions/runs/37445966728), `1e66bbd2`)
- [ ] Tag `3.0.27-fork.21`, Build and Push Images green, release assets verified
- [ ] Production backup (`pre-fork21-<TS>`) + panel upgrade to fork.21
- [ ] Verify served bundle no longer contains 「搜索入口、地区或隧道」, panel healthy
