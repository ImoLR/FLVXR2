# 166 — release batch 1029 (fork.96 = plans 163 + 164 + 165)

User 2026-10-29 TW night: release 163 + 164 + 165 together without screenshot approval; prod test mandatory; never knock a
node offline; convert existing ≥2-node layers to 最优 after the proof (「现有的也一起改了」).

Branch `maintenance/3.0.27-fork.96-batch-1029` from `origin/feature/3.0.27-best-path-select` (163 → 164 → 165 on fork.95).
Run dir `/root/flvx-workers/runs/release-batch-1029`.

## Checklist
- [x] Gates: go build, backend tests SQLite + PostgreSQL, agent tests, frontend tsc/eslint/build (19/19 + 19/19; agent all ok — landing 3 tests need a short TMPDIR (unix socket path >108), pass with it; eslint 95 errors vs 96 on fork.95)
- [x] Migration dry-run on a fresh prod DB copy (164 column `forward.line_compliance_ack_sig`): up in ~1 s, column added, #109 count 1 → ack → 0, user 403
- [x] Screenshot gate: three galleries reviewed, no defects found, nothing re-shot
- [x] Tag `3.0.27-fork.96` (bb0d1ee3), CI 40963757197 + Images 40963760947 green, release assets validated (Latest, 10 assets, pins, sha256, amd64+arm64)
- [x] Panel backup `pre-fork96-20261029T222029Z`, upgrade 06:20:41 TW healthy, backups pruned (pre-fork95 + pre-fork96 kept)
- [x] Post-upgrade: 30/30 online, forward_port 38 + 290 runtime objects identical, 30 reconnects updated=0; 0 tunnels on `best`
- [x] OTA 29 agents one at a time (21 → 55 → 23 → … → 26), 0 failed; node 36 skipped on purpose (brief: don't touch), stays fork.95
- [x] 164 data step: #109 ticked via API, count 1 → 0, sig v1:…, non-admin 403
- [x] 165 prod proof: temp tunnel 104 (47 → [17,15]) switched 17 151 ms → 15 2 ms, one UpdateChains on 47 only, new conns via 15, long conn via 17 survived (120/120 echoes), 30/30 online, all deleted + nodes clean
- [x] New tunnel defaults to 最优 (API create without strategy → best); existing 167 strategies identical before 5b
- [x] 5b: 20 layers / 40 rows → `best` (undo `step5b/undo-20261029T230344Z.json`), one backend restart → chain-only UpdateChains, 38/290 unchanged, 30/30, 6 rounds 0 switches; next hour 2 switches (tunnel 33 back and forth), 0 push fails
- [x] Resources: backend 36.5 → 34.0 MiB after 1 h, CPU ~5 %, round 8.3 → 8.37 s, DB 57.3 MB same, 0 non-request log lines/10 min, agents 21/23/2/47 RSS/fds/TIME_WAIT steady (55 no SSH)
- [x] Read-only prod screenshots: 10 shots in `runs/release-batch-1029/shots/` (rules grouped, #109 已确认合规, 最优 chips + detail, new tunnel form default 最优)
- [x] Summary `runs/release-batch-1029/summary.md`; plan complete. Node 36 not OTA'd on purpose (stays fork.95).
