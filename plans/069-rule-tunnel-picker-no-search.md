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
- [x] Verify served bundle no longer contains 「搜索入口、地区或隧道」, panel healthy

## Release / rollout evidence
- Annotated tag points to `1e66bbd2`; [Build and Push Images 37446226404](https://github.com/ImoLR/FLVXR2/actions/runs/37446226404) passed.
- [fork.21 release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.21) is Latest, non-prerelease, with the same 10 asset names as fork.20. Both compose image tags, both scripts (`PINNED_VERSION` / `REPO`), and both GOST SHA256 files verified.
- Production rollback point: `/opt/flvx-svc/rollback/pre-fork21-20261006T101356Z/` (compose/.env, one-shot SQLite online backup with `quick_check=ok`, 62 table counts, local fork.20 image tags, and `ROLLBACK-METADATA.md`).
- Installed the verified fork.21 v6 compose, bumped `FLUX_VERSION`, and pulled/recreated only backend + frontend; backend healthy and frontend HTTP 200 (2026-10-06 10:14 UTC).
- Post-upgrade verification: `POST /api/v1/forward/list` HTTP 200 / code 0 (26 items); all 22 previously online nodes remained online with advancing `node_metric` timestamps in two samples; nodes 1/24/28 remained offline as before. Agent versions and node/tunnel/forward/user row counts unchanged. Backend logs clean; reconnect redeploys succeeded.
- Served main JS changed from `index-CULceEEN.js` to `index-QPV1Xk1M.js`; scanned both served JS files: 「搜索入口、地区或隧道」 absent, node-page 「搜索地区名称或代码」 retained. CSS unchanged as expected.
- Chinese rollout summary and verification evidence: `/root/flvx-workers/runs/fork21-release/summary.md` and adjacent `evidence/`. No code changes, agent upgrades, manual DB data changes, or changes to other stacks.
