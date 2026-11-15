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
- [ ] gates: go-backend build/vet/test (SQLite + PG 18), go-gost/x tests, go-gost build, `npm run build`
- [ ] push branch, CI green, tag `3.0.27-fork.134`, Images workflow, release Latest + assets validated
- [ ] pre-state snapshot (nodes, rules, chains, landing sync, conns on the 2 OTA hosts, line-monitor samples, HiNet
      NAccepted + getpeername count)
- [ ] panel backup + rollback dir, prune backups, upgrade /opt/flvx-svc, health + nodes
- [ ] OTA Fachost seednet 黑五 (canary) → Tcscloud Seednet (one at a time, SSH backup each, traffic check)
- [ ] verify ≥10 min: HiNet NAccepted +≈4/min, 0 new getpeername; 4 lines sample every 30 s; nodes / rules / landing
      sync unchanged; versions; interruption seconds
- [ ] ledger 259 → 已发布, memory notes, cleanup worktrees + caches, summary, plan complete

## Results
