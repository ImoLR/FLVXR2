# 123 — Release fork.66: plan 121 extra protocols on shared landings + H5 chip column

Release `3.0.27-fork.66` = fork.65 (branch head 5b1885c8) + plan 121 (AnyTLS / Snell / Mieru on mmwx-shared
landings, admin enable per server, hidden until mmwx advertises `protocol_caps`) + one UI tweak approved by the user
on 2026-10-21. Panel only — plan 121 changes no agent code, so no agent OTA.

Branch `maintenance/3.0.27-fork.66-embedded-share` from `maintenance/3.0.27-fork.65-agent-panel-failover` head
5b1885c8 (tag `3.0.27-fork.65` + plan 122 docs). Run dir `/root/flvx-workers/runs/release-fork66-embedded-share`.

## UI tweak
Shared landing server list (落地 → 服务器): on H5 the protocol chips of a server card (SS2022 / VLESS / AnyTLS /
Snell / Mieru …) go into a right-hand column, top-aligned with the server name, one chip per line, right-aligned,
same chip style. Nothing else on the page changes.

## Checklist
- [x] Release branch from 5b1885c8, cherry-pick the 8 plan 121 commits (diff identical to fork.64..a11d3b98)
- [x] UI tweak: H5 protocol chip column on shared server cards (desktop decision noted below)
- [x] Before/after screenshots (H5 light + dark, desktop if changed) + index page in run dir `screens/`
- [ ] Baseline `go test ./...` on a clean worktree at `3.0.27-fork.65` (GOMAXPROCS=2 -p 1)
- [ ] Branch `go test ./...` — only baseline failures (compared by name)
- [ ] go-gost/x tests
- [ ] Frontend tsc / eslint (no new) / build (largest file < 5 MiB)
- [ ] `scripts/landing-acceptance/` local run incl. P1
- [ ] Migration dry run on an online-backup copy of the prod DB (12 new columns + defaults, existing tables unchanged)
- [ ] Push branch, CI green
- [ ] Tag `3.0.27-fork.66`, Images green, release Latest with the fork.65 asset set
- [ ] Prod rollback dir `pre-fork66-<TS>` (validated DB backup), prune-backups.sh
- [ ] Prod panel upgrade; health, nodes online before/after, no error spike
- [ ] Prod checks: new columns, table counts/hashes, /landing/monitor, shared landings on mmwx 5 and 7 unchanged
- [ ] Resource numbers: panel RSS before/after, shared-landing sync request count/size per cycle
- [ ] Summary + plan marked complete

## Notes
- UI tweak (H5 only, `useH5Mode()`): server card header becomes a row; left = name / source / mode chip / chain
  chips / status chip (unchanged order), right = protocol chips, `flex-col items-end gap-1`, top-aligned with the
  name. The body chip row drops the protocol chips on H5 only. In the column VLESS shows as `VLESS` (title = full
  `VLESS Reality Vision`): the full label made the card min-content 411 px on a 390 px phone (page overflow). Server
  name wraps instead of truncating on H5. Card widths measured identical before/after (366 px @390, 336 px @360).
- Desktop left unchanged: a right column there squeezed names like 「Breadcloud US 年付」 onto two lines, not tidier.
