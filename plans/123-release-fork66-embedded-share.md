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
- [x] Baseline `go test ./...` on a clean worktree at `3.0.27-fork.65` (GOMAXPROCS=2 -p 1)
- [x] Branch `go test ./...` — only baseline failures (compared by name)
- [x] go-gost/x tests
- [x] Frontend tsc / eslint (no new) / build (largest file < 5 MiB)
- [x] `scripts/landing-acceptance/` local run incl. P1
- [x] Migration dry run on an online-backup copy of the prod DB (12 new columns + defaults, existing tables unchanged)
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
- Gates: fork.65 baseline (clean worktree e7ad9542) 15 failing tests = branch 15, same names (backup export/import,
  federation dual panel, legacy migrations, renewal TZ, service monitor, tunnel address-in-use); `go vet` output
  identical; 1156 passing tests incl. the 14 plan 121 protocol tests. go-gost/x: 13 packages ok (go-gost unchanged vs
  fork.65). Frontend: tsc ok, eslint on touched files 0 errors and no new warning kinds, build ok, largest file
  2,893,996 B (index js), precache 3073 KiB.
- Migration rehearsal: online backup of the prod DB (403 MB, quick_check ok), branch paneld started on the copy inside
  `unshare -n` (loopback only, cannot reach mmwx/nodes) for 60 s, clean SIGTERM exit. landing_server +6 and
  landing_node +6 columns with defaults ('' / 4 / 'none' / 'TCP'); 77/80 tables raw-hash identical, landing_server /
  landing_node only gained columns (all existing columns hash-identical), landing_integration.sync_error changed
  (no network). landing_server revision / pushed / applied revisions unchanged → no re-push to mmwx after upgrade.
  Copies deleted afterwards.
- Local landing acceptance (branch paneld + fork.66 lab agent + pinned Core v1.4.59 xray 26.3.27): S1–S8, E1–E3, T1,
  P1, R1 all PASS (104 checks, 223 s); R1: Core RSS 36.6 MiB / 13 fds, agent 50.7 MiB / 10 fds, Core never restarted.
- Shared sync cost with no caps advertised (fork.65 vs fork.66 paneld, same fake Custom without protocol_caps /
  server_mode / busy_ports, same seed: 2 shared servers × SS2022 + VLESS, 185 s window): both 9 requests
  (3 × GET /shares, 6 × GET /shares/<id>/status), 0 request bytes, 6,519 response bytes, no desired PUT; panel RSS
  34.7 vs 35.0 MiB.
