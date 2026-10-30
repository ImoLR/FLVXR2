# 167 — quota-log-quiet: stop the SetQuotaGroups push/log flood + fleet log audit (fork.97 + OTA)

User 2026-10-30 (session 1749279e): 「日志要赶紧修掉……日志问题要重视」 — urgent, release as soon as built and verified
(no batch wait, no screenshot gate). No disconnects: OTA one node at a time; node 36 stays fork.95.

Branch `maintenance/3.0.27-fork.97-quota-log` from `origin/maintenance/3.0.27-fork.96-batch-1029` (= prod fork.96).
Worktree `/root/projects/flvxx/flvxx-quota-log`. Run dir `/root/flvx-workers/runs/quota-log-quiet`.

## Problem
- Panel `quotaCoordinator` (`go-backend/internal/http/handler/quota_groups.go`) reconciles at most 1×/s and re-pushes a
  node's budgets whenever their JSON differs from the last push. A user with a total connection / IP limit whose live
  counts move on other nodes changes the budgets almost every reconcile → one `SetQuotaGroups` WS command per second per
  affected node.
- Agent logs `🔔 收到命令: SetQuotaGroups (data redacted)` at info for every command → ~1350 lines/h on node 32.

## Checklist
- [x] Before numbers: 24 h SetQuotaGroups 32 = 32331, 48 = 27546, 23 = 10449 (others ≤ 21); lines/h 32 1290, 48 1284, 23 168; agent CPU/RSS 32 0.35 %/63 MB, 21 0.33 %/61 MB, 48 0.25 %/97 MB, 23 1.78 %/79 MB; panel ~3 %/36 MiB; fleet audit of 24 hosts in `runs/quota-log-quiet/audit/before/` (cause: user 8, 400 conns / 5 IPs, ~80–115 live on 23 → budgets of 32/48 change every 1–2 s)
- [x] Panel: hysteresis push policy (urgent tighten / near-limit / structural changes at once, loosening + small moves
      coalesced per node), unit tests (over-limit still blocked within the old bound, steady-state push reduction ≥ 90 %) — c4b3d8d3; sim of the prod pattern: 32 3183 → 110/h, 48 3047 → 12/h
- [x] Agent: SetQuotaGroups (and other timer-driven commands found in the audit) quiet on success; errors logged once per
      distinct error (rate-limited); other commands unchanged; unit tests — c08817c3 (24 h audit: SetQuotaGroups is the only timer-driven command still logged)
- [x] Other FLVX noise found in the fleet audit fixed if per-connection / per-second: TcpPing partial-loss pairs (≈95 % of 首次失败/已恢复, up to 748/day on 42) → one hourly line; per-connection handler errors (47: 1596/day, 48: 684/day) → per service × class summary; blocked connections no longer logged twice
- [x] Gates: go build, backend tests SQLite + PostgreSQL (compare with base), agent tests (`go-gost/x`) — SQLite 19/19 + PG 19/19 ok, 0 failures (fork.96 base: 0/0); handler + contract re-run on the final code (aa44f509) SQLite + PG ok; go-gost/x (short TMPDIR) + go-gost ok; quota tests 30× `-race` ok. Review round (subagent): 1 bug (failed push kept the old baseline) + 7 risks/nits fixed in aa44f509; `-count=3` service failures are pre-existing (same on fork.96: global registries)
- [ ] Tag `3.0.27-fork.97`, CI + Images green, release assets validated
- [ ] Panel backup + upgrade `/opt/flvx-svc`, prune backups, health + 30/30 online
- [ ] OTA one at a time: 32 (canary) → 21 → 55 → rest (never 36)
- [x] Journald cap `flvx-cap.conf` (SystemMaxUse=1G) on FLVX-only hosts with a default cap > 1G: 2, 14, 22, 23, 32 (journal ~4G → ~1G each, ~3 G disk back each, agents same PID); hosts with existing ≤ 200M caps kept (15/17/18/20/42/47/48/52), small disks (12/26/39, default < 1G) untouched; mmwx-shared hosts report only (21/30/31/34/35/36/45/53)
- [ ] After numbers (≥ 30 min on 32 / 23), agent RSS/CPU 32/21/55, panel CPU/RAM
- [ ] Prod proof: temp user + rules on two nodes, total conn limit enforced across nodes, idle push count low, 0 residue
- [ ] Summary `runs/quota-log-quiet/summary.md`; plan complete
