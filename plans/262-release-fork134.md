# 262 — release fork.134 (plan 259 线路监测 RST 温和关闭) + targeted agent OTA ×2

Base: tag `3.0.27-fork.133` (65885f9f, = prod since 11-15 02:00 TW). Branch `maintenance/3.0.27-fork.134`, worktree
`/root/projects/flvxx/flvxx-rel134`. Merged (fast-forward): `fix/3.0.27-linemon-rst-close` @ 02ffe9e4 (code e4bee9c0).
Run dir `/root/flvx-workers/runs/release-fork134`. NOT included: 260 sub-admin-view, 261 sub-rules-link.
User approved 11-15 16:37 TW 「可以现在就升级」 → daytime release OK (no 02:00–06:00 window for this one).

Scope: panel upgrade (no DB change; rollback = fork.133 image swap) + agent OTA ONLY Fachost seednet 黑五 (canary) →
Tcscloud Seednet. AWS HK and the rest of the fleet unchanged. HiNet box (mmwx-hinet2): read only, one-liner NOT re-run
(3-day stopgap window stays, ≈11-18 15:45 TW).

## Tasks
- [x] worktree + ff merge, plan doc (262 = next free on all refs)
- [x] gates: go-backend build/vet/test (SQLite + PG 18), go-gost/x tests, go-gost build, `npm run build`
- [x] push branch, CI green, tag `3.0.27-fork.134`, Images workflow, release Latest + assets validated
- [x] pre-state snapshot (nodes, rules, chains, landing sync, conns on the 2 OTA hosts, line-monitor samples, HiNet
      NAccepted + getpeername count)
- [x] panel backup + rollback dir, prune backups, upgrade /opt/flvx-svc, health + nodes
- [x] OTA Fachost seednet 黑五 (canary) → Tcscloud Seednet (one at a time, SSH backup each, traffic check)
- [x] verify ≥10 min: HiNet NAccepted +≈4/min, 0 new getpeername; 4 lines sample every 30 s; nodes / rules / landing
      sync unchanged; versions; interruption seconds
- [x] ledger 259 → 已发布, memory notes, cleanup worktrees + caches, summary, plan complete

## Results
Gates (local, 11-15 16:39–16:55 TW): go-backend build OK, vet 0 non-unkeyed, SQLite `go test ./...` all ok except
`TestChainUserPolicyPausesWithoutDeleting/expired` once (timing under load from a parallel worker; rerun 10/10 ok; same
test ok under PG; handler change is line_monitor only). PG 18: repo / handler / contract ok. go-gost/x: `chain
TestRouteUsesPooledConn` flaky = pre-existing (fork.133 base 4/30 fail vs fork.134 4/30 fail, chain package unchanged);
rest ok. go-gost build OK, `npm run build` rc=0. CI Build Check 43695523638 green (708f7c5d).

Release: tag `3.0.27-fork.134` → 30527a06; CI Build Check 43696790025 green, Images 43696791661 green; release Latest,
10 assets = fork.133 set, compose pinned to fork.134 images, install.sh / panel_install.sh PINNED_VERSION, gost-amd64
sha256 ok, `-V` = 3.0.27-fork.134.

Deploy (11-15, TW): panel backup `/opt/flvx-svc/rollback/pre-fork134-20261115T090843Z` (SQLite quick/integrity ok;
rollback = `rollback.sh` image swap), prune → kept pre-fork133 + pre-fork134. Panel swap 17:08:59–17:09:11 (healthy,
0 restarts, 0 error lines; 28/28 online, 51 rules / chains / tunnels / landing / shared sync unchanged).
OTA: Fachost seednet 黑五 17:10:39 (systemd stop→start 0.39 s) → Tcscloud Seednet 17:12:24 (0.58 s); both
`succeeded`, backups `/root/flvxx-backup-20261115T091034Z-pre-fork134` / `-20261115T091221Z-pre-fork134`;
flux_agent / mmw-agent / landing Core PIDs unchanged; tunnel 30 / 97 diagnose 6/6 before and after.
Fleet: 2×fork.134 + AWS HK fork.133 + 8×fork.131 + 15×fork.130 + Nobrand ×2 fork.126.

Verify: mmwx-hinet2 `flvx-linemon-25931.socket` NAccepted 1 (stuck, before) → 4 after 黑五 → 9 (17:13:43) → 52
(17:24:17) = +4.1/min; getpeername lines 40 / 10 min before → 0 since 17:12:40. 4 lines: every 30 s, no gap > 45 s
over 40 min across panel + OTA, all ok; chart 1 ports 23526 / 27002 open/open; 测试连通 ✓ ✓ (黑五 1041 ms once =
SYN retransmit, same 1/715 in the 6 h before; samples after p99 28.5 ms). One-liner on mmwx-hinet2 not re-run
(window ≈11-18 15:45 TW; reset to 24 h optional).
