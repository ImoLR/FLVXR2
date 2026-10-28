# 160 — release-batch-1028: fork.94 = plans 151 + 154 + 155 + 156 (panel only) + node 39 notice + tunnel split data step

User 2026-10-28: batch releases (1–2 per day). This batch ships, panel only (no agent OTA, `git diff fork.93 -- go-gost` empty):
- 151 backend-test-cleanup — backup import now refuses legacy 1.x per-type backups (was a silent "导入成功：{}"); test fixes.
- 154 node-admin-notice — admin-only 「节点提示」 (`node.admin_notice`, add-only) with a ⚠ badge on node cards.
- 155 tunnel-split-plain-names — plan-152 「入口显示名」/`{入口}` hints hidden (renderer + column kept), admin
  `tunnel/split-entry` / `merge-entry` endpoints; original multi-entry tunnel logic untouched.
- 156 tap-only-tooltips — chart tooltips + dropdown chips open on click/tap only (no hover / finger-swipe).

Base: `origin/feature/3.0.27-tap-only-tooltips` (65abbdd5) = fork.93 branch head 734abae3 + 151/154/155/156; no rebase
needed (no newer maintenance branch). Branch `maintenance/3.0.27-fork.94-batch-1028`, worktree
`/root/projects/flvxx/flvxx-release-batch-1028`. Run dir `/root/flvx-workers/runs/release-batch-1028`.

## Checklist
- [x] Read finals of 151/154/155/156 (all finished, tests green, CI green) — go/no-go: GO, all four ship
- [x] Gates on the release branch: `go build ./...` + vet OK, full `go test ./...` SQLite 19/19 + PostgreSQL 19/19 packages,
      0 failures; frontend tsc 0 errors, build OK (main JS 3.0 MB), eslint 96 errors = fork.93 base 96 (no new); branch CI green
- [x] Screenshot gate: galleries of 154 / 155 / 156 reviewed — no defect to fix. Note for the user: the 12 vmsilo twins inherit
      the tunnel-page folder 「Mkcloud 沪日ixp 440」 (admin-only label; user decides whether to move them to a new folder)
- [x] Fresh prod DB copy: new binary starts (migration add-only), 155 split dry-run + apply/undo rehearsal, 154 notice rehearsal
      (10-28 ~17:55 TW copy, release binary, netns panel + 30 fake agents: split 37/37 checks, node 23 0 pushes, users
      byte-identical; undo 26/26 identical; notice → 0 pushes, tunnel/forward tables identical)
- [ ] Push branch, annotated tag `3.0.27-fork.94`, CI + Images green, release assets validated (Latest, same asset set, pins, sha256, arches)
- [ ] Prod: rollback dir + validated DB backup + image tags + rollback.sh; prune backups
- [ ] Prod: upgrade `/opt/flvx-svc` to fork.94, health + version.json; 30/30 nodes online; forward_port + per-node service counts unchanged
- [ ] Node 39 notice via admin API; prove no config push to 39
- [ ] Plan 155 data step `--apply` (undo file first); verify 24 one-entry tunnels, no `{入口}`, 13 rules on 23 unchanged,
      client tests through tunnels 25/28/39 before + after, temp admin rule on a 51 twin (listen on 51 only, IPv6 client), deleted
- [ ] Users: every non-admin view byte-identical (real JWT, read only); names render plain
- [ ] Resources: agents on 23 / 51 / hop nodes before/after; backend RSS/CPU; non-request log lines / 10 min
- [ ] Read-only prod screenshots (admin): node 39 ⚠, tunnel list, rule picker, 监控 390 swipe/tap on 连接数 chart
- [ ] final.md + summary.md; `docs(plan): mark fork.94 rollout complete`
