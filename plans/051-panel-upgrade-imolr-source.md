# 051 - Remove remaining iKeilo dependencies from panel upgrade paths

## Problem
The fork releases from `ImoLR/FLVXR2`, and the UI update entries (sidebar button and
version footer) already use `/api/v1/system/upgrade`, which downloads version-pinned
release assets from `ImoLR/FLVXR2`. Two upgrade paths still depend on iKeilo:

- Legacy `/api/v1/panel/upgrade` (`panelUpgrade`) resolves a target version, then ignores it
  and runs `panel_install.sh` from the `main` branch. On `main` that script still has
  `REPO="iKeilo/FLVXR2"`, so any caller of this endpoint would install iKeilo's latest release.
- Post-upgrade image cleanup (`systemUpgradeExecutor.helperScript` and `panel_install.sh`
  `update_panel`) only matches `ghcr.io/ikeilo`, so old `ghcr.io/imolr/flvxr2-svc-*` images
  are never removed.

## Approach
- Make `panelUpgrade` delegate to `systemUpgrade` (identical `{version, channel}` request),
  so both endpoints use the same version-pinned `ImoLR/FLVXR2` release flow and the
  `main`-branch script is no longer used.
- Match `ghcr.io/imolr/flvxr2-svc-*` in both image cleanups, keeping the existing legacy
  `ghcr.io/ikeilo/*` cleanup so panels migrated from iKeilo images still get cleaned.
  The imolr match is limited to the panel images so unrelated images are not removed.
- Out of scope (non-runtime): docs site URLs, `skills/flvx-api/package.json` metadata,
  `scripts/sync-gost-to-domestic.sh`, and the `main` branch itself.

## Tasks
- [x] Delegate `panelUpgrade` to `systemUpgrade`
- [x] Extend image cleanup patterns in `helperScript` and `panel_install.sh`
- [x] Add tests for the delegation and the cleanup pattern
- [x] Run backend tests and compare with the fork.5 baseline (failure set identical)
- [x] Publish the independent `3.0.27-fork.6` release without changing earlier tags
- [x] Verify the CI build and all `3.0.27-fork.6` release assets (images pinned to
  `ghcr.io/imolr/*:3.0.27-fork.6`, scripts pinned to `3.0.27-fork.6` / `ImoLR/FLVXR2`,
  gost checksums OK, release marked Latest)
- [x] Upgrade only the production FLVXX frontend/backend to `3.0.27-fork.6` with a
  validated rollback point (`/opt/flvx-svc/rollback/pre-fork6-20261001T105951Z`);
  node online state unchanged (24/25, node 24 was already offline)
