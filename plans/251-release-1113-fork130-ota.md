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
- [x] merge 3 branches (no conflicts) + plan doc; disk ≥ 5 GB
- [x] gates: go-backend build/vet/test (SQLite + PG), go-gost/x tests, go-gost build, `npm run build`
- [x] push branch, CI green, tag `3.0.27-fork.130`, Images workflow, release Latest + assets validated
- [x] pre-state snapshot (nodes, rule states, chain/线路机 rules, warm idle conns per host)
- [x] panel backup + rollback dir, upgrade /opt/flvx-svc, health + 28 nodes, migration columns
- [x] OTA canary Mkcloud 沪日ixp 440 (check 247 dead-target refill not at error level)
- [x] OTA the rest one host at a time (SSH backup each), report 火山广州 upgrade result (pending → status)
- [x] verify: nodes / rules unchanged, self-heal log count, 线路机 exits AnyTLS capability
- [ ] warm idle sleep after ~35 min (per-host idle conns before → after vs 86 → 24 estimate), admin/user tags
- [x] plan 250 live test: one AnyTLS 「我自己的节点」 rule on Dmit Nathan tunnel, client connect, delete, Snell/SS2022 rejected
- [ ] ledger 247–250 → 已发布, prune backups, cache + worktree cleanup, summary, plan complete

## Results
- Merge (11-13 02:02 TW): 3 branches + fork.129 docs merged with no conflicts. Disk 6.3 G free before build.
- Gates (02:03–02:09 TW): go-backend build ok, vet only the old RouteRef unkeyed warnings, `go test ./...` all ok
  (SQLite); go-gost/x warmpool, chain, ctx, handler/forward, landing, handler/relay, socket, listener/tls all ok;
  go-gost build ok; `npm install --legacy-peer-deps && npm run build` ok. Branch CI 43275808141 green (4 jobs incl. PG
  contract). Local PG (temp postgres:18, repo/handler/contract, -p 1 -timeout 90m) running in background.
- Pre-state (02:05 TW): 29/29 nodes online (27 × fork.129 incl. new vmsilo 沪美, Nobrand ×2 fork.126), 50 rules (49
  active), 1 forward_chain row; bizcheck pre: 49 active, 3 skipped (SSH targets), not-all-ok rules 6/37/53/69/77/78/84/95.
- Release: tag `3.0.27-fork.130` (annotated) → 7464438a; Images 43277156320 success; release Latest, asset set = fork.129,
  compose/install pinned, gost-amd64 sha256 + `-V` ok.
- Panel 02:27:45–02:27:56 TW fork.129 → fork.130 (healthy, 救命版 mount ro ok). Rollback point
  `/opt/flvx-svc/rollback/pre-fork130-20261112T181118Z/rollback.sh` (validated SQLite, image tags). Pruned pre-fork128.
  Migration: forward_chain + inbound_protocol / tls_server_name / password / tls_cert_pem / tls_key_pem / cert_sha256 /
  prev_inbound; existing chain row 6 (tunnel 116) inbound '' = Reality. 29/29 online after restart.
- Canary Mkcloud 沪日ixp 440 02:28:25: `node/upgrade` answered in 5 s, state succeeded, back in 7 s. Error-level lines
  0 since restart (238 in the 10 min before = dead-target refill errors, plan 247 fixed). 12 idle pooled conns.
- Plan 250 live test (02:32–02:34 TW, tunnel 109 Dmit Nathan → JP, exit Bestvm JP B already fork.130): temporary
  SOCKS5 upstream with auth on Breadcloud JP2 (panel host + FLVX node IPs are refused as upstream, as designed);
  admin rule 604 / chain 7 AnyTLS → active in ~2 s, exit Core loopback 127.0.0.1:1024 AnyTLS inbound applied; 复制链接
  has url + Clash + Surge line; cert sha256 seen through the entry == pinned fingerprint; sing-box 1.14.4 client from the
  dev box: generate_204 204 (1.1 s), egress IP = upstream; temp sub `?t=surge` has the AnyTLS line, sing-box output has
  the anytls outbound. SS2022 landing node / landingServerId+snell / +ss2022 on tunnel 109 all refused 「线路机隧道只能
  使用 VLESS Reality Vision 或 AnyTLS 落地」, nothing created. Cleanup: sub 19, rule 604, chain 7 deleted; exit inbound
  and desired/applied entries gone; upstream stopped and removed from Breadcloud JP2. Before exits were upgraded the
  tunnel list showed AnyTLS greyed with 「出口节点「…」的节点程序版本过低」 (gate works).
- OTA 02:28–02:58 TW, one host at a time, SSH backup each (`/root/flvxx-backup-<ts>-pre-fork130/restore-agent.sh`,
  list in run dir work/backups.txt; vmsilo 沪美 uses the install.sh layout /etc/flvx_agent + flvx_agent.service →
  layout-aware backup/snap variants). 27 agents on fork.130 (26 from the brief + vmsilo 沪美 added 11-12); every
  `node/upgrade` answered in 3–12 s with state succeeded (no `pending` needed), back online in 5–14 s. 火山广州 this time
  7 s answer / 9 s back, status succeeded (no false failure; the slow-CN-download path did not occur). Skipped Nobrand
  5cm IPLC 沪日 + Nobrand IPLC 沪港 (fork.126). flux_agent + landing Core PIDs unchanged on every host.
- Verify (03:00 TW): 29/29 online; forwards and the existing chain row identical pre/post; bizcheck post == pre
  (49 active, same 8 partial: 6/37/53/69/77/78/84/95); panel `status self-heal` lines 0; no panic/error lines. All 10
  线路机 tunnels with exits show `chainAnyTLSAvailable` = true (exits Datawave HK 2, AWS HK, Bestvm HK 年付, Bestvm TW,
  Datawave TW 1, Bestvm US, Bestvm JP B, Bestvm sg 年付 all report landing_chain 2); 114/115 (no exit) unchanged.
- Test residue found + removed: the AnyTLS test auto-created a chain-only native landing server (id 10, node Bestvm
  JP B, 02:32:22) + an isolated landing Core there; chain-only hosts are kept by design after the last chain is deleted
  (no API to remove them; idle Core stop = old P2-6). Removed: DB copy taken (run dir gost-before-ls10-delete.db), row
  deleted (no other references), on Bestvm JP B agent stopped → flvx-landing-core stopped/disabled, unit +
  /etc/flvxx/landing (58 MB) removed → agent started (back in ~6 s, online fork.130, no landing dir recreated).
- Warm tags (03:12 TW): admin rules page 12 faster + 5 「预热中 · 数据不足」 + 1 unsupported; normal users (read-only
  minted tokens, 10 users) see only `{status: faster, savedMs, kind, hops: []}` on their own rules (users 7/8/11/16/74),
  nothing else. Admin tunnel page: 101 vmsilo沪日首发 shows warming (= 数据不足).
- Mkcloud 沪日ixp 440: 0 error-level lines after the OTA (238 per 10 min before = dead-target refills of rules 77/95).
