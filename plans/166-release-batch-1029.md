# 166 — release batch 1029 (fork.96 = plans 163 + 164 + 165)

User 2026-10-29 TW night: release 163 + 164 + 165 together without screenshot approval; prod test mandatory; never knock a
node offline; convert existing ≥2-node layers to 最优 after the proof (「现有的也一起改了」).

Branch `maintenance/3.0.27-fork.96-batch-1029` from `origin/feature/3.0.27-best-path-select` (163 → 164 → 165 on fork.95).
Run dir `/root/flvx-workers/runs/release-batch-1029`.

## Checklist
- [x] Gates: go build, backend tests SQLite + PostgreSQL, agent tests, frontend tsc/eslint/build (19/19 + 19/19; agent all ok — landing 3 tests need a short TMPDIR (unix socket path >108), pass with it; eslint 95 errors vs 96 on fork.95)
- [x] Migration dry-run on a fresh prod DB copy (164 column `forward.line_compliance_ack_sig`): up in ~1 s, column added, #109 count 1 → ack → 0, user 403
- [x] Screenshot gate: three galleries reviewed, no defects found, nothing re-shot
- [ ] Tag `3.0.27-fork.96`, CI + Images green, release assets validated
- [ ] Panel backup + rollback dir, upgrade `/opt/flvx-svc`, version + health, prune backups
- [ ] Post-upgrade: nodes online, forward_port + per-node runtime objects unchanged, tunnels already on `best` listed
- [ ] OTA all agents one at a time (21 → 55 → big entry → rest → 26), each online + objects equal before the next
- [ ] 164 data step: tick 已确认合规 on rule #109, count 1 → 0
- [ ] 165 prod proof with temp tunnel/rule (decision, pushed order, traffic, long-lived conn survives, nodes online), cleanup
- [ ] New tunnel defaults to 最优; existing strategies unchanged before 5b
- [ ] 5b: convert existing ≥2-node layers to `best` (undo file, no service redeploy), watch ≥5 rounds
- [ ] Resources before/after (backend RSS/CPU, round duration, switches, DB size, logs, node sockets)
- [ ] Read-only prod screenshots (rules grouped, #109 ticked, tunnel edit 最优 hint)
- [ ] Summary + plan marked complete
