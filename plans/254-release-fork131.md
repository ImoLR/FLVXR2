# 254 — release fork.131 (plans 252 + 253) + targeted agent OTA

Base: tag `3.0.27-fork.130` (7464438a). Branch `maintenance/3.0.27-fork.131`, worktree `/root/projects/flvxx/flvxx-rel131`.
Merged: `feature/3.0.27-chain-only-cleanup` @ 26d6ec98 (252), `feature/3.0.27-landing-vless-enc` @ 67fafa58 (253),
plus fork.130 release docs (`maintenance/3.0.27-fork.130` @ 4b905a78). Run dir `/root/flvx-workers/runs/release-fork131`.
User approved 11-13 ~20:50 TW (「253 可以…你这边先发布吧」).

Scope: panel upgrade (253 adds additive column `node.landing_vless_enc`; rollback = fork.130 image swap, delete vless_enc
nodes first). Agent OTA only: NexKR (canary) + Bestvm US (253 native landings), then 线路机 exits Datawave HK 2, AWS HK,
Bestvm HK 年付, Bestvm TW, Datawave TW 1, Bestvm JP B, Bestvm sg 年付 (252). One host at a time. Rest of fleet stays fork.130.

## Tasks
- [x] worktree + merge (no conflicts) + plan doc; go/npm caches cleaned (disk 5.4 → 8.2 G free)
- [ ] gates: go-backend build/vet/test (SQLite + PG), go-gost/x tests, go-gost build, `npm run build`
- [ ] push branch, CI green, tag `3.0.27-fork.131`, Images workflow, release Latest + assets validated
- [ ] pre-state snapshot (nodes, rules, chains, chain-only servers, bizcheck)
- [ ] panel backup + rollback dir, upgrade /opt/flvx-svc, health + nodes, migration column, prune backups
- [ ] OTA canary NexKR, then Bestvm US, then the 7 线路机 exits (one at a time, SSH backup each)
- [ ] Boil HKT 99u (server 11) VLESS Encryption switch ON (other settings unchanged)
- [ ] live test: vless_enc nodes on Boil HKT 99u + NexKR, 复制链接/Clash, Xray + mihomo clients, subs; delete, port freed
- [ ] verify: nodes / rules / chains unchanged, landing_chain 3 on 8 exits, shared sync ok, Core RSS, panel logs
- [ ] ledger 252 + 253 → 已发布, cleanup caches + worktrees, summary, plan complete

## Results
