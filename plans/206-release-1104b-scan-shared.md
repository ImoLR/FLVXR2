# 206 — release-1104b (DONE 11-04 20:57 TW): fork.113 = plan 205 (shared 禁止端口扫描 + no recreate) + plan 201 (probe 表格 default)

Branch `maintenance/3.0.27-fork.113-release-1104b` = `feature/3.0.27-landing-scan-shared` (cb86c6cf) + merge 38227e33
(plan 201, frontend-only). Panel only, no OTA, no DB schema change. Run dir `/root/flvx-workers/runs/release-1104b`.

## Results so far
- Gates on 0eb3c0f5: go test ./... SQLite 25/25 ok, PostgreSQL 25/25 ok (0 FAIL); frontend tsc 0, build ok.
- Branch CI 41823077102 green; tag `3.0.27-fork.113` → 0eb3c0f5, Images 41825334232.
- Baseline 11-04 19:3x TW: 29/29 online, forward_port 42, runtime 296 objects / 0 orphans / 0 missing, 10 real rules + 6 temp
  rules = release-batch-1104 final, 5 landing links 2/2, 救命版 47/48 WAL 8 553 152 B, panel 13.96 s CPU/5 min RSS 44.6 MiB;
  /tz fresh browser opens 卡片 (no TCP option) on fork.112 = the reported bug.

- Release validated (Latest, 10 assets = fork.112, pins, gost sha256, ghcr amd64+arm64). Backup
  `/opt/flvx-svc/rollback/pre-fork113-20261104T121956Z` (quick/integrity ok); prune kept pre-fork112 + pre-fork113.
- Panel fork.113 live 20:20 TW (frontend build 3.0.27-fork.113-mw22fxzm), no OTA. 29/29 online; state/rules/landing
  links = baseline before, after upgrade and after all tests; 救命版 unchanged (47/48, WAL 8 553 152 B, 0 restarts).
- Shares: all four advertise scan_guard, pushed = applied, no sync error / 422 / 409. 内置: Boil Hinet 马年 + PQS TW
  生效中, Breadcloud US 年付 + Dmit T1 暂无节点 (生效中 while a node exists). Revision +2 on servers with nodes after
  restart = same pattern as the fork.111 restart (NexKR hash flips 6bd869e4 ↔ 547e910c), not plan 205; no Core restart.
- Scan tests (temp admin nodes 119/121/122, own public IP closed ports 39950-39999): Dmit T1, Boil Hinet 马年, PQS TW
  each hit vertical 30 → disabled 21–47 s after scan start → traffic cut → 解除禁用 → passes in 6 s → deleted.
  Breadcloud US 年付: node port unreachable from outside (dev box, AWS HK, LeiKwan VPS; Core listens *:300xx and the
  machine reaches its own IP) = upstream firewall, pre-existing, 0 real nodes there.
- Recreate test (temp user 59 on Dmit T1): blocked → create refused with reason + scanCreateBlocked; node 解除禁用 →
  create ok; blocked again + deleted → still refused; event 解除禁用 → create ok; user + nodes deleted (0 grants/settings).
- /tz fresh browser 1440 + 390 px (local + tzgo.imgamer.top): 表格 selected, TCP sort present and works, 0 page errors.
- Resources: panel 13.96 → 13.40 s CPU / 5 min, RSS 44.6 → 54.5 MiB (fresh process); JP/NexKR agent + Core unchanged.

## Checklist
- [x] Build final.md clean (205 + mmwx helper-scan-guard v0.7.19 live)
- [x] Release branch + merge plan 201
- [x] Gates on release head: go-backend `go test ./...` (sqlite + pg), frontend tsc + build
- [x] Prod baselines: panel res/health, nodes online, rule groups, landing links, 救命版
- [x] Push branch, CI green; tag `3.0.27-fork.113`, images + release assets verified
- [x] Backup (DB) + rollback dir, prune backups (keep 2)
- [x] Upgrade /opt/flvx-svc → fork.113, health + nodes
- [x] Post checks = baseline; shared revisions applied, no 422/409
- [x] 内置 禁止端口扫描: shared servers 「生效中」 / 「暂无节点」
- [x] Shared scan test on temp admin node: detect → disable → 解除禁用 → delete
- [x] Recreate block test on temp user: blocked until 解除禁用; cleanup
- [x] /tz opens in 表格 at 1440 px and 390 px (fresh browser), TCP sort present
- [x] Final checks, summary, plan marked complete
