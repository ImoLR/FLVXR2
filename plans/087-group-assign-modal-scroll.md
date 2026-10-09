# 087 — Group assign modals scroll (fork.38)

## Problem
隧道分组 → 分配隧道: after selecting multi-entry tunnels, the per-entry checkbox
blocks (fork.34) make the modal content taller than the modal; the modal clips
the overflow and cannot be scrolled. Same layout in 分配用户.

## Cause
`vite-frontend/src/pages/group.tsx` assign modals used
`<ModalContent className="min-h-[420px] max-h-[80vh]">` with the bridge default
`DialogContent` (`grid`) and `classNames.base` `overflow-hidden`. Grid rows grow
with content, so `ModalBody`'s `overflow-y-auto` never got a bounded height and
the overflow was clipped by the dialog.

## Fix
Both assign modals use `scrollBehavior="inside"` (bridge: dialog becomes a flex
column, body `min-h-0 flex-1 overflow-y-auto`), redundant body `overflow-y-auto`
removed. Frontend only; no backend/agent/DB change.

## Tasks
- [x] Fix committed (c60072e7) on `maintenance/3.0.27-fork.38-assign-modal-scroll`
- [x] Browser verification (Playwright, local paneld on a prod-DB copy, 1280x600 / 1280x900 / 390x700): body scrolls, header + footer visible, 选择隧道 dropdown opens/scrolls/selects, checkboxes toggle, save payload `tunnelEntries`; 分配用户 scroll with many users; screenshots in /root/flvx-workers/runs/assign-modal-scroll/
- [x] `npm run build` + `npm run lint` (no new errors vs fork.37)
- [x] Push branch, CI green
- [x] Tag `3.0.27-fork.38`, Build and Push Images green, release assets verified
- [x] Prod backup (rollback dir + image tags), prune to 2 newest
- [x] Upgrade /opt/flvx-svc to fork.38, verify health, served bundle contains the change, nodes reporting
- [x] Plan marked complete + pushed

## Verification notes
- Playwright (chromium 1243) against `vite preview` of the built dist, API proxied to a local
  paneld on 127.0.0.1:16365 with an online-backup copy of the prod DB (+40 dummy users in
  the copy); assign POSTs intercepted (not written). Script + results:
  /root/flvx-workers/runs/assign-modal-scroll/t/verify.js, screens/results.json.
- Fixed build: 60/60 checks pass at 1280x600, 1280x900, 390x700 (touch): body is the
  scroll container (1306px content in 334px body at 1280x600), wheel/touch scroll works,
  header + 取消/保存 stay visible, 选择隧道 dropdown opens inside the dialog, its list scrolls
  and selects, entry checkboxes toggle, save payload `tunnelEntries` matches the toggle;
  分配用户 dropdown (50 users) opens, scrolls, selects; footer visible.
- Same script on the fork.37 build: 15 failures (body grows to content height, no scroll,
  footer + lower entries clipped) — reproduces the user report.
- eslint `src/pages/group.tsx`: 0 errors / 25 warnings before and after.

## Rollout (2026-10-09)
- Branch CI "CI Build Check" 37934759570 green; tag `3.0.27-fork.38` on 1edddc13,
  "Build and Push Images" 37934991579 green; release Latest, not prerelease, same 10 assets
  as fork.37, compose images `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.38`,
  `PINNED_VERSION="3.0.27-fork.38"` / `REPO="ImoLR/FLVXR2"` in both scripts, gost sha256 ok,
  v6 compose identical to fork.37 apart from the tag.
- Rollback point `/opt/flvx-svc/rollback/pre-fork38-20261009T132612Z` (compose, .env,
  gost.db.validated quick_check ok, local tags `local/flvxx-{backend,frontend}:pre-fork38-20261009T132612Z`
  = fork.37 images); prune kept pre-fork38 + pre-fork37, removed pre-fork36 + fork.35 images.
- Panel upgraded 13:26Z: backend healthy, frontend 200, served `index-C1m5Yc7l.js` has
  `scrollBehavior:\`inside\`` on both assign modals, 26/26 nodes with node_metric <60 s,
  no backend errors. Nodes not upgraded (frontend-only release).
