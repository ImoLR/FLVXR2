# 208 — release-1104c: fork.114 = fork.113 + plan 207 (rule-line 「日美最优」 region wording)

Branch `maintenance/3.0.27-fork.114-release-1104c` = `maintenance/3.0.27-fork.113-release-1104b` (4eb2f999) + merge of
`feature/3.0.27-best-region-label` @ b4abc9ff (clean merge, 8 files). Panel only, no OTA, no DB schema change.
User approved the screens 11-04 ~20:45 TW (keep 「最优线路」 for mixed-region multi-target rows; 4/61/74 show 「香港最优」;
no further desktop compression). Run dir `/root/flvx-workers/runs/release-1104c`.

## Checklist
- [x] Release branch + merge plan 207 (clean)
- [x] Gates on release head: go-backend `go test ./...` (sqlite + pg), frontend tsc + lint (changed files) + build
  (SQLite 25/25 ok, PG 25/25 ok, 0 FAIL; tsc 0, eslint best-pick.tsx 0, build ok)
- [x] Prod baselines: panel res/health, nodes online, rule groups real traffic, landing links, 救命版, rule list API latency + size
  (29/29 online, 296 runtime objects / 0 orphans, 10 real rules + 6 temp tunnels ok (rule 37 2/4 = old), 5 landing 2/2,
  救命版 47/48, panel 14.00 s CPU/5 min RSS 53.5 MiB, forward/list admin median 15.3 ms 33 795 B)
- [ ] Push branch, CI green; tag `3.0.27-fork.114`, images + release assets verified
- [ ] Backup (DB) + rollback dir, prune backups (keep 2)
- [ ] Upgrade /opt/flvx-svc → fork.114, health + nodes
- [ ] Post checks = baseline
- [ ] Rule page on prod (admin + normal user, 1440 + 390): region wording per release-notes table; 隧道 page unchanged
- [ ] Ledger: 207 → 已发布; final summary; plan marked complete
