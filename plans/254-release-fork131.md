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
- [x] gates: go-backend build/vet/test (SQLite + PG), go-gost/x tests, go-gost build, `npm run build`
- [x] push branch, CI green, tag `3.0.27-fork.131`, Images workflow, release Latest + assets validated
- [x] pre-state snapshot (nodes, rules, chains, chain-only servers, bizcheck)
- [x] panel backup + rollback dir, upgrade /opt/flvx-svc, health + nodes, migration column, prune backups
- [x] OTA canary NexKR, then Bestvm US, then the 7 线路机 exits (one at a time, SSH backup each)
- [x] Boil HKT 99u (server 11) VLESS Encryption switch ON (other settings unchanged)
- [x] live test: vless_enc nodes on Boil HKT 99u + NexKR, 复制链接/Clash, Xray + mihomo clients, subs; delete, port freed
- [x] verify: nodes / rules / chains unchanged, landing_chain 3 on 8 exits, shared sync ok, Core RSS, panel logs
- [x] ledger 252 + 253 → 已发布, cleanup caches + worktrees, summary, plan complete

## Results
- Merge (11-13 20:40 TW): 252 + 253 + fork.130 docs, no conflicts; agent reports landing_chain 3 + landing_vless_enc 1.
- Gates (20:43–21:00 TW): go-backend build ok, vet 0 non-unkeyed lines, `go test ./...` SQLite all ok; PG 18 temp
  container: repo ok (219 s), handler ok (134 s), contract ok (267 s); go-gost build ok; `npm run build` ok.
  go-gost/x: `TestClassifyInboundFailure` failed once ("udp busy: port_in_use TCP") — a random UDP port that a TCP
  listener on this busy dev box already held; test + code unchanged since fork.130, rerun 5/5 + full package ok → flake.
  Branch CI 43427542555 green.
- Release: tag `3.0.27-fork.131` (annotated) → 99393cda; Images 43429197438 success; release Latest, asset set = fork.130,
  compose/install pinned, gost-amd64 sha256 + `-V` ok.
- Pre-state (20:44 TW): 29/29 online, 50 rules (49 active), 1 forward_chain (6, tunnel 116 → chain-only server 9 on
  Bestvm US); bizcheck pre: 49 active, 3 skipped, not-all-ok 6/37/53/69/77/78/84/95 (= fork.130 baseline). Users then
  added rules 606 (ns-saorana, 20:46) + 607 (sunflorian, AnyTLS chain 8 on tunnel 116, 21:07) → post-panel baseline.
- Panel 21:11:53–21:12:03 TW fork.130 → fork.131 (healthy, 救命版 mount ro ok). Rollback point
  `/opt/flvx-svc/rollback/pre-fork131-20261113T131142Z/rollback.sh`; pruned (kept pre-fork130 + pre-fork131).
  Column `node.landing_vless_enc` added; 29/29 online at 21:12:14.
- OTA (one host at a time, SSH backup `/root/flvxx-backup-<ts>-pre-fork131/restore-agent.sh` each, list work/backups.txt):
  NexKR canary 21:12:48 (5 s answer, 7 s back), Bestvm US, Datawave HK 2, AWS HK, Bestvm HK 年付, Bestvm TW,
  Datawave TW 1, Bestvm JP B, Bestvm sg 年付 (21:21:16) all `succeeded`, no `pending`; landing Core / flux_agent PIDs unchanged.
- Boil HKT 99u (server 11): 协议模板 VLESS Encryption ON 21:13 TW via frontend-shaped save; diff = protocols only
  (+ revision 24→25). All other servers keep it OFF (NexKR on only 21:16–21:19 for the test).
- Live test (admin temp nodes): Boil HKT 99u node 191 port 30000 (active in ~54 s via Helper), NexKR node 192 port
  30002 (instant). Link `vless://…?encryption=mlkem768x25519plus.random.0rtt.…&flow=xtls-rprx-vision&security=none&type=tcp`
  + Clash entry with `encryption`. Xray 26.3.27 + mihomo 1.19.32 from the dev box: Boil HKT egress 58.152.255.186,
  8 MiB in 2.9 s / 2.8 s; NexKR egress 218.50.130.172, 4.2 s / 3.7 s; first attempt ok; traffic billed (down 16.8 MB
  per node). Temp sub: Base64 2/2 vless_enc lines, Clash 2 `encryption` entries, sing-box 0 (skipped). NexKR Core PID
  unchanged (hot add/remove). Deleted: Boil HKT pending → released after Helper final traffic (66 s, port refused
  after), NexKR immediate (30002 no listener, existing node 97 on 30001 still listening). Sub deleted, 0 rows left.
- Verify (21:22–21:30 TW): 29/29 online; the 9 OTA'd hosts on fork.131 report landing_chain 3 + landing_vless_enc 1,
  rest 18 × fork.130 + Nobrand ×2 fork.126. Forwards + chains identical post-panel → post-OTA (chains 6 Reality / 8
  AnyTLS on tunnel 116, both active); vs pre only user changes (605 moved 101→92, new 606/607). Chain-only servers 1 → 1
  (9 Bestvm US, in use by chains 6 + 8 → kept, reconciler did nothing). New native landing 12 Tcscloud Seednet was
  enabled by someone in the panel at 20:44 TW (not by this task, 0 nodes). bizcheck post == pre (51 active, same 8
  partial). 5 shared landings: caps active, sync error '' (Boil HKT 99u included). Panel log: 0 error/panic lines.
  Core RSS: NexKR 70 MB, Bestvm US 84 MB (Cores not restarted); agent error lines 0.
- Ledger 252 + 253 → 已发布 fork.131. Cleanup: go build cache, npm cache, node_modules/dist, temp PG container,
  test configs; worktrees flvxx-rel131 / flvxx-vless-enc / flvxx-chain-cleanup removed (branches stay).
