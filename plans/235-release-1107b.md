# 235 release-1107b — fork.124 = fork.123 + plan 234 (incl. 233)

User approval: 2026-11-07 ~17:15 TW 「可以」 after the 234 screens. Second (last) FLVX release of 11-07 TW.

- Base: `maintenance/3.0.27-fork.123-release-1107` @ dd9a4290 (tag 3.0.27-fork.123 → 612b7b46)
- Merge: `feature/3.0.27-switch-margin-settings` @ 31c1aa9b (contains 233 @ 74e78271)
- Branch: `maintenance/3.0.27-fork.124-release-1107b`, tag `3.0.27-fork.124`
- Panel: no schema change, 3 new `vite_config` keys + `auto_switch_counts` row on first count.
- Agent OTA (native landing): NexKR, Bestvm US only. Skip Breadcloud ×4 (vendor maintenance) and the rest.
- Settings stay at defaults 5 / 5 / 20 (do not change).
- Run dir: `/root/flvx-workers/runs/release-1107b`

## Checklist
- [x] Release branch created, 234 merged (no conflicts; diff vs fork.123 = 233/234 files + plan docs only)
- [x] Gates: go build + go test ./... (SQLite + PostgreSQL contract), go-gost + go-gost/x tests, tsc / build / lint vs base
- [x] Baseline before anything (panel, nodes, rules, landing links, sub, 救命版, API latency, NexKR / Bestvm US agent, 最优 / Reality picks)
- [x] Push branch + tag 3.0.27-fork.124; CI + images + release assets verified
- [x] Panel backup (incl. DB) + rollback dir; prune backups (keep 2)
- [x] Panel upgrade; re-check vs baseline; fork.123 binary still works with the new vite_config keys
- [x] Defaults 5/5/20 unchanged, 最优 picks unchanged right after upgrade, card renders (desktop + phone), counts start at 0
- [x] OTA NexKR (backup first), re-check landing links
- [x] OTA Bestvm US (backup first), re-check
- [x] Post-checks: /version.json, old-profile client ≤1 reload, clients rpc 403, card save round-trip same values, margin capability
- [x] Repeat baseline, compare
- [x] Ledger 233/234 → 已发布; temp leftovers 0
- [x] Summary written; plan marked complete

## Results (round 2 finished 11-07 ~18:55 TW)
- Gates on 4799be34: backend `go build ./...` ok; SQLite `go test ./...` all ok; PostgreSQL `go test ./...` all ok; go-gost ok;
  go-gost/x: tc netns tests ok once /usr/sbin in PATH; `TestSocketCapSharedByProbeAndPin` flaky under load
  (peak=1 < 2) — same on base dd9a4290 (12/30 vs head 6/30) → pre-existing; `go vet` unkeyed RouteRef in
  internal/leikwan/testapi (dir unchanged vs base) → pre-existing; fe tsc 0 errors, eslint 94 errors / 3793 warnings
  = base exactly (none in new files), vite build ok.
- Tag 3.0.27-fork.124 pushed 18:20 TW; docker-build 42363520437 success; images backend/frontend (4 manifests each);
  release assets = fork.123 set, compose pinned, gost-amd64 sha256 + `-V` ok.
- Panel backup /opt/flvx-svc/rollback/pre-fork124-20261107T103247Z (quick/integrity ok); pruned to 2 (fork.122 dir gone).
- Panel 18:33:10 TW healthy on fork.124; 31/27 nodes online as before; 15/15 real rules, landing links, sub, 救命版 same;
  API latency same; panel CPU 14.9 → 12.5–13.6 s/5 min, RSS 42 → 49–54 MiB (fresh process).
- 最优: 0 `[best-path]` switches after restart; 4 `[rule-best]` switches (tunnels 4/74, AWS HK → Datawave HK 2, 8–11 ms
  faster) = re-decision after restart (decisions live in memory, pre-existing). Snapshot churn = idle tunnels showing the
  latest ranking only.
- Card on prod desktop + phone: 5/5/20, 「11-07 起自动切换 N 次」, 0 page errors; same-value save round-trip ok (keys now
  stored as 5/5/20).
- OTA: NexKR 18:41:27 (6.1 s), Bestvm US 18:43:52 (5.0 s); backups /root/flvxx-backup-*-pre-fork124 on each; NexKR
  landing link 2/2 ok; both report the IP-margin capability (native 2 new / 2 old = Breadcloud).
- Public POST /api/clients/v2/rpc = 403; fresh-profile real login ok (SW on, build fork.124). The old-profile-across-upgrade
  browser run was not done (not started before the upgrade); SW/version code unchanged in this release.
- Temp user rb1107bfe deleted (leftover 0). Ledger 233/234 → 已发布.
