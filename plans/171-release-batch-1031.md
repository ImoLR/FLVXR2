# 171 — release batch 1031 (fork.98 = plans 168 + 169 + 170)

User 2026-10-30 ~23:55 TW: 「做完之后不用给我发截图了，就跟前面一起就发布」 — release 168 (entry speed → tc),
169 (gold hint rings until clicked) and 170 (only 用户默认速率 + per-server 速率) together without screenshot approval.
168 open questions take the worker defaults. Panel upgrade + OTA all agents (168); node 36 untouched.

Branch `maintenance/3.0.27-fork.98-batch-1031` from `origin/feature/3.0.27-speed-simplify` (168 → 170 on fork.97)
+ merge of `origin/feature/3.0.27-onboarding-hints-always` (169). Run dir `/root/flvx-workers/runs/release-batch-1031`.

## Result
Released 2026-10-31 01:16 TW (panel) + OTA 01:18–01:54 TW: production = `3.0.27-fork.98`, 29 agents fork.98 (all `entry_rate_tc = 1`), node 36 fork.95, 30/30 online.

## Checklist
- [x] Gates: go build ok (vet: only the pre-existing leikwan/testapi unkeyed-fields notes), backend tests SQLite 19/19 + PostgreSQL 19/19 ok (0 FAIL), go-gost/x 20 pkgs ok + go-gost ok (short TMPDIR), frontend tsc 0 errors, build ok, eslint 94 errors vs 95 on fork.97 (no new); branch CI 41093684518 green
- [x] Screenshot gate: 169 harness on the merged tree + fresh prod DB copy 42/44 (the same 2 admin-desktop misses as 169's own run: admin has no landing-link rules → no 复制链接; the 12 "running animations" are react-hot-toast toasts, 0 on 落地 after done); ring visible on 用于新规则 / 目标类型 / 复制链接 (H5 user); lap 2.4 s; 170's 20 PNGs reviewed (no 限速 nav, no 规则限速, perm dialog = 状态 only, 编辑用户 默认速率 + rows) — no defects
- [x] Migration dry-run on a fresh prod DB copy (168: 2 node columns; 170: startup ref cleanup): up in 0.6 s, `node.entry_rate_tc` INTEGER default 0 + `entry_rate_error` TEXT added, 30 nodes = 0, forward/forward_port 38/38, no `[speed-ref-cleanup]` line (0 refs), RSS 37 MB
- [x] Tag `3.0.27-fork.98` (d22acf6b), CI 41095749939 + Images 41095752832 green, release validated (Latest, 10 assets = fork.97 set, pins, sha256, amd64+arm64)
- [x] Panel backup `pre-fork98-20261030T171613Z` (quick/integrity ok), pruned (pre-fork97 + pre-fork98 kept), upgrade 01:16:52 TW healthy, frontend build 3.0.27-fork.98-mvv86b9c
- [x] Post-upgrade: 30/30 online in 21 s, forward_port 38 + 236 runtime objects identical (0 orphans/missing), 30 reconnects diff-only (59 updated=0; node 18 tunnels updated=2 = plan-165 in-memory best order reset to DB order on any panel restart, code unchanged since fork.96); read-only SQL: 0 speed refs / rates anywhere, no `[speed-ref-cleanup]` line
- [x] OTA 29 agents one at a time 01:18–01:54 TW (canary 2 = entry with 8 rules, then 14 → … → 47, then busiest entries 32/48/23, 26 last after 0 session drops in the hour), 0 failed, 0 diagnose regressions, all reconnect 2–12 s; all 29 `entry_rate_tc = 1` (no kernel fallback), 0 `flvxlr-*` links / 0 tc filters on the 23 SSH nodes; node 36 skipped on purpose (stays fork.95); post-OTA 30/30, forward_port 38 + 236 objects identical
- [x] Prod proof (temp tunnel 107 entry 47 + temp user 56 + rules 197/198, all deleted, 0 rows left, node 47 0 links/filters): row 20 → HTB `7f1a:8000` 22229Kbit, no nft policer; near client (node 15, ~1 ms) 10.59 + 10.58 = 21.2 Mbps; default 30 (no override) → 33344Kbit class, 15.89 + 15.89 = 31.8; removal → objects gone ≤22 s, back to ~900 Mbps. From this host (~250 ms RTT) only 75 % of the rate (7.47 + 7.47 for 20). First attempt (tunnel 106 / user 55) used billing mode `flow: 99999` by mistake → bogus 3.5 TB on the temp user, rules auto-paused; all deleted, nothing persisted
- [x] UI check on prod (read-only, writes aborted): 13/13 — sidebar/H5 tabs/我的 without 限速, /limit → /dashboard, rule 高级功能 without 规则限速, 用户隧道权限 without 限速, 编辑用户 默认速率 + server 速率 rows, gold ring on 用于新规则 (desktop + H5); shots in `runs/release-batch-1031/shots/`
- [x] Resources: backend CPU 4.3 % (60 s, before) → 6.1 % (60 s + 180 × 1 s samples after; no per-minute spike from the entry-rate safety pass), RSS 37.9 → 47–48 MiB right after the OTA/proof hour, DB 57.3 MB unchanged, panel non-request lines 0 → 1 per 10 min (a plan-165 best-path switch); agents CPU/RSS: node 2 2.6 %/75.5 MB → 2.3 %/65.5, node 23 2.3 %/83.9 → 2.2 %/68.6, node 48 (1 vCPU) 0.6 %/100.9 → 0.8 %/79.9, node 32 0.3 %/65.6 → 0.35 %/62.5; agent journal 10 min after settling: 2/23/32 = 0 lines, node 48 had a 60-line burst of un-summarized `route` dial timeouts to user 8's target (17:59–18:06 UTC, pre-existing log class, 0 since); node 32 journald 952.1 MB before = after
- [x] Summary `runs/release-batch-1031/summary.md`; plan complete. Rollback: panel `pre-fork98-20261030T171613Z/rollback.sh` (→ fork.97, no `--restore-db`), agents `/root/flvxx-backup-20261030T171819Z-pre-fork98/restore-agent.sh` (SSH nodes) / `flvxx.old` (25/49/50/51/54/55), per-node kill switch `FLVX_ENTRY_RATE_TC=off`
