# 258 — release fork.133 (plan 255 线路监测 + 检测端口) + targeted agent OTA ×3

Base: tag `3.0.27-fork.132` (980984c3, = prod since 11-14 23:34 TW). Branch `maintenance/3.0.27-fork.133`, worktree
`/root/projects/flvxx/flvxx-rel133`. Merged: `feature/3.0.27-line-monitor` @ 5644da36 (code 7fa8b36b; base fork.131) —
no conflicts, no file touched by both 256 and 255. Run dir `/root/flvx-workers/runs/release-fork133`.
User approved 11-14 ~23:10 TW (「没问题，这个你安排一下发布吧」), condition: nothing that can drop business nodes outside
the night window.

Scope: panel upgrade (auto-migrate adds 4 NEW tables line_monitor_chart / _line / _sample / _port; nothing existing
changes; rollback = fork.132 image swap, tables stay inert) + agent OTA ONLY Fachost seednet 黑五 (canary, target),
Tcscloud Seednet (target), AWS HK (source, last). Rest of fleet unchanged.
Time rules: disruptive steps (panel swap, OTA) only 02:00–06:00 TW, never 03:45–04:20 (boil 04:00 IP change), start no
later than 03:35; HiNet socket swap after 04:20. Non-disruptive work any time.

## Tasks
- [x] caches cleaned (5.0 → 5.7 G free), worktree + merge, plan doc
- [ ] gates: go-backend build/vet/test (SQLite + PG 18), go-gost/x tests, go-gost build, `npm run build`
- [ ] push branch, CI green, tag `3.0.27-fork.133`, Images workflow, release Latest + assets validated
- [ ] pre-state snapshot (nodes, rules, chains, landing sync, conns on the 3 OTA hosts, panel RSS)
- [ ] (≥02:00) panel backup + rollback dir, prune backups, upgrade /opt/flvx-svc, health + nodes, 4 new tables
- [ ] OTA Fachost seednet 黑五 (canary) → Tcscloud Seednet → AWS HK (one at a time, SSH backup each, traffic check)
- [ ] chart 「AWS HK → Seednet」 (2 lines, 自动开启检测端口, both ✓)
- [ ] (≥04:20) HiNet box: remove hand-made flvx-linemon.socket, run product 开启命令 port 25931 / 24 h / allow-only OFF
- [ ] chart 「Seednet → HiNet」 (2 lines → boilhinet2.imgamer.top:25931, 测试连通 ✓ ✓)
- [ ] watch 10 min: samples every 30 s on 4 lines, panel logs, RSS/CPU
- [ ] verify: nodes / rules / chains / landing sync unchanged, versions, interruption seconds
- [ ] ledger 255 → 已发布, memory notes, cleanup worktrees + caches, summary, plan complete

## Results
