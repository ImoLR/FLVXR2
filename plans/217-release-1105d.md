# 217 — release-1105d: fork.117 = fork.116 + plans 214 + 216 (panel only)

User approval 2026-11-05 ~19:30 TW (「可以就按你推荐的改然后发布…还有哪些没发布的一起发布了」). Third FLVX release
of 11-05 TW — explicitly requested by the user (overrides the ≤2/day cadence).
Run dir: `/root/flvx-workers/runs/release-1105d`. Expected prod: fork.116 → fork.117. Panel only, no DB change, no OTA.

## Inputs
- 214 `feature/3.0.27-node-rules-align` @ 00649446 — 路由规则 dialog r3 + final short chip in 我的规则
  (step 0: 24abdf5c, 「→ 强制 IPv4 / → 直连 / → 封锁」, full name stays in 展开详情). Frontend only.
- 216 `feature/3.0.27-landing-enable-range` @ 2ab31f3f — 启用为落地 auto-picks a free 1000-port block, owner names in
  errors, red toasts 6–15 s + click/✕ to close. Panel only (Go + frontend), no DB change.
- Release branch `maintenance/3.0.27-fork.117-release-1105d` = tag 3.0.27-fork.116 (9ca7f5fb) + merge 214 + merge 216 + this doc.

## Checklist
- [x] Step 0: 214 short chip (24abdf5c), tsc/lint/build ok, demo 328/328 checks, other layers 127/127 0 px diff vs r3, gallery 「r3 → 最终」
- [x] Plan number 217 free on every branch / worktree
- [x] Release branch from fork.116 + both merges (clean; both heads are ancestors)
- [x] Step-0 CI green on the feature branch
- [x] Gates on release head: go build, go test SQLite + PG contract, tsc, vite build, eslint no new errors vs fork.116
- [x] Baselines (pre): state, counters, rule client checks (temp rules `rb117-client-*`), landing links, sub pulls, 救命版, res, API latency
- [x] Push branch, CI green; tag `3.0.27-fork.117`; Images + Release green; assets/pins/sha256/arches verified
- [x] Open a fork.116 browser profile on prod (kept across the upgrade)
- [x] Backup (validated DB) + rollback dir + local image tags; prune backups (keep 2)
- [x] Upgrade /opt/flvx-svc panel only
- [x] Post checks = baseline (state/counters/rules/landing/subs/救命版/res)
- [x] UI checks on prod: 路由规则 dialog 390 + 1440 (内置 unchanged, 我的规则 short chip + ✓, 展开详情 switch + buttons; read-only),
      216 port-block info visible, Breadcloud US / JP ranges untouched, version.json fork.117, old profile ≤ 1 reload, /api/clients/ 403
- [x] Ledger: 214 + 216 → 已发布 fork.117
- [x] Cleanup (temp users, temp rules, subs, browsers), final.md / summary.md; plan marked complete

## Result (2026-11-05 TW)
- Tag `3.0.27-fork.117` → 33ce8075; CI 42021040780 (release branch) + 42020739538 (step 0) green; Images 42025105066 green;
  release Latest, assets = fork.116 set, pins/REPO/sha256/amd64+arm64 verified.
- Gates: go build/vet ok; `go test ./...` with PostgreSQL DSN: 25 packages ok, 0 failures; tsc 0; build ok;
  eslint 94 errors / 3789 warnings = fork.116 (no new).
- Prod upgraded 20:34 TW (panel only, no DB change, no OTA), healthy in 9 s. Rollback dir
  `/opt/flvx-svc/rollback/pre-fork117-20261105T123354Z` (image swap; `--restore-db` last resort). Backups kept: pre-fork116 + pre-fork117.
- Business = baseline: 30/30 online; tunnels/chains/forwards/ports identical; 10 real rules same (rule 37 diag 2/4 pre-existing);
  6/6 temp rules + 8/8 landing links OK; Clash/sing-box 200; 救命版 47/48 (阿里深圳 long offline) unchanged; API median 15.4 → 15.4 ms.
- UI on prod (read-only; demo 我的规则 injected in the browser only, writes blocked): short chips + ✓ (admin + owner, 390 + 1440),
  展开详情 switch + buttons, 命中后 full name; 216 toast shows the picked 端口段, red toast still up at 6.5 s, ✕ closes.
  Breadcloud JP / US stay 30000-30999. version.json fork.117; old fork.116 profile → fork.117 with 1 reload; /api/clients/ 403.
- Temp user rb117fe, temp rules rb117-client-*, temp sub deleted; 0 leftovers.
