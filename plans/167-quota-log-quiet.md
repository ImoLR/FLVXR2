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
- [ ] Before numbers: pushes/h + agent lines/h on 32 / mk-hr-1188 (23), panel CPU/RAM, fleet journal audit (read-only)
- [ ] Panel: hysteresis push policy (urgent tighten / near-limit / structural changes at once, loosening + small moves
      coalesced per node), unit tests (over-limit still blocked within the old bound, steady-state push reduction ≥ 90 %)
- [ ] Agent: SetQuotaGroups (and other timer-driven commands found in the audit) quiet on success; errors logged once per
      distinct error (rate-limited); other commands unchanged; unit tests
- [ ] Other FLVX noise found in the fleet audit fixed if per-connection / per-second
- [ ] Gates: go build, backend tests SQLite + PostgreSQL (compare with base), agent tests (`go-gost/x`)
- [ ] Tag `3.0.27-fork.97`, CI + Images green, release assets validated
- [ ] Panel backup + upgrade `/opt/flvx-svc`, prune backups, health + 30/30 online
- [ ] OTA one at a time: 32 (canary) → 21 → 55 → rest (never 36)
- [ ] Journald cap `flvx-cap.conf` (SystemMaxUse=1G) on FLVX-only hosts; shared hosts report only
- [ ] After numbers (≥ 30 min on 32 / 23), agent RSS/CPU 32/21/55, panel CPU/RAM
- [ ] Prod proof: temp user + rules on two nodes, total conn limit enforced across nodes, idle push count low, 0 residue
- [ ] Summary `runs/quota-log-quiet/summary.md`; plan complete
