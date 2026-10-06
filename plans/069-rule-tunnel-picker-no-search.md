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
- [x] Tag `3.0.27-fork.21`, Build and Push Images green, release assets verified
- [x] Production backup (`pre-fork21-<TS>`) + panel upgrade to fork.21
- [ ] Verify served bundle no longer contains 「搜索入口、地区或隧道」, panel healthy

## Release / rollout evidence
- Annotated tag points to `1e66bbd2`; [Build and Push Images 37446226404](https://github.com/ImoLR/FLVXR2/actions/runs/37446226404) passed.
- [fork.21 release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.21) is Latest, non-prerelease, with the same 10 asset names as fork.20. Both compose image tags, both scripts (`PINNED_VERSION` / `REPO`), and both GOST SHA256 files verified.
- Production rollback point: `/opt/flvx-svc/rollback/pre-fork21-20261006T101356Z/` (compose/.env, one-shot SQLite online backup with `quick_check=ok`, 62 table counts, local fork.20 image tags, and `ROLLBACK-METADATA.md`).
- Installed the verified fork.21 v6 compose, bumped `FLUX_VERSION`, and pulled/recreated only backend + frontend; backend reached healthy. Post-upgrade verification follows.
