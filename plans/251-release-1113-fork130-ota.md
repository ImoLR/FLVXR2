# 251 — release-1113: fork.130 (plans 247 + 248 + 249 + 250) + 全网 agent OTA

Base: tag `3.0.27-fork.129` (af408eee). Branch `maintenance/3.0.27-fork.130`, worktree `/root/projects/flvxx/flvxx-rel130`.
Merged: `fix/3.0.27-old-issues-1112` @ d0b47833 (248 + 247), `feature/3.0.27-warm-idle-rule-tag` @ d73b6ffc (249),
`feature/3.0.27-line-protocol-anytls` @ 264dbbbe (250), plus fork.129 release docs (945cadb3: plans 246, 248-landing-core-restart).
Run dir `/root/flvx-workers/runs/release-fork130`. User approved 11-12 23:58 TW (247–249) and 11-13 01:40 TW (250).

Brief: release as `3.0.27-fork.130`; panel upgrade with backup (250 adds 7 additive `forward_chain` columns; rollback =
fork.129 image swap; AnyTLS chain rules made after the release would be treated as Reality by fork.129). Fleet OTA of
all 26 fork.129 agents, canary Mkcloud 沪日ixp 440, one host at a time, 02:00–04:30 TW only. Skip Nobrand ×2 + offline;
Dmit Echo via panel only. 248 `node/upgrade` may return `pending: true` → poll `node/upgrade/status`.

## Tasks
- [ ] merge 3 branches (no conflicts) + plan doc; disk ≥ 5 GB
- [ ] gates: go-backend build/vet/test (SQLite + PG), go-gost/x tests, go-gost build, `npm run build`
- [ ] push branch, CI green, tag `3.0.27-fork.130`, Images workflow, release Latest + assets validated
- [ ] pre-state snapshot (nodes, rule states, chain/线路机 rules, warm idle conns per host)
- [ ] panel backup + rollback dir, upgrade /opt/flvx-svc, health + 28 nodes, migration columns
- [ ] OTA canary Mkcloud 沪日ixp 440 (check 247 dead-target refill not at error level)
- [ ] OTA the rest one host at a time (SSH backup each), report 火山广州 upgrade result (pending → status)
- [ ] verify: nodes / rules unchanged, self-heal log count, 线路机 exits AnyTLS capability
- [ ] warm idle sleep after ~35 min (per-host idle conns before → after vs 86 → 24 estimate), admin/user tags
- [ ] plan 250 live test: one AnyTLS 「我自己的节点」 rule on Dmit Nathan tunnel, client connect, delete, Snell/SS2022 rejected
- [ ] ledger 247–250 → 已发布, prune backups, cache + worktree cleanup, summary, plan complete

## Results
