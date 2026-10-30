# 171 — release batch 1031 (fork.98 = plans 168 + 169 + 170)

User 2026-10-30 ~23:55 TW: 「做完之后不用给我发截图了，就跟前面一起就发布」 — release 168 (entry speed → tc),
169 (gold hint rings until clicked) and 170 (only 用户默认速率 + per-server 速率) together without screenshot approval.
168 open questions take the worker defaults. Panel upgrade + OTA all agents (168); node 36 untouched.

Branch `maintenance/3.0.27-fork.98-batch-1031` from `origin/feature/3.0.27-speed-simplify` (168 → 170 on fork.97)
+ merge of `origin/feature/3.0.27-onboarding-hints-always` (169). Run dir `/root/flvx-workers/runs/release-batch-1031`.

## Checklist
- [ ] Gates: go build, backend tests SQLite + PostgreSQL, go-gost/x + go-gost tests, frontend tsc/lint/build
- [x] Screenshot gate: 169 harness on the merged tree + fresh prod DB copy 42/44 (the same 2 admin-desktop misses as 169's own run: admin has no landing-link rules → no 复制链接; the 12 "running animations" are react-hot-toast toasts, 0 on 落地 after done); ring visible on 用于新规则 / 目标类型 / 复制链接 (H5 user); lap 2.4 s; 170's 20 PNGs reviewed (no 限速 nav, no 规则限速, perm dialog = 状态 only, 编辑用户 默认速率 + rows) — no defects
- [x] Migration dry-run on a fresh prod DB copy (168: 2 node columns; 170: startup ref cleanup): up in 0.6 s, `node.entry_rate_tc` INTEGER default 0 + `entry_rate_error` TEXT added, 30 nodes = 0, forward/forward_port 38/38, no `[speed-ref-cleanup]` line (0 refs), RSS 37 MB
- [ ] Tag `3.0.27-fork.98`, CI + Images green, release assets validated
- [ ] Panel backup + rollback dir, upgrade, version check, backups pruned
- [ ] Post-upgrade: all nodes online as before, forward_port + per-node services identical, 0 speed limits anywhere
- [ ] OTA all agents one at a time (canary = busy entry node), entry_rate_tc recorded, no `flvxlr-*` devices; node 36 skipped
- [ ] Prod proof with temp objects: per-server 速率 20 Mbps → tc class, 2 parallel downloads ≈ 20 evenly; user default applies; removed → clean
- [ ] UI check on prod (read-only): no 限速 menu / 规则限速 / user-tunnel 限速; 编辑用户 默认速率 + rows; gold ring on 落地
- [ ] Resources before/after (backend, entry-rate job, DB, logs, node CPU/sockets, node 32 journald)
- [ ] Summary `runs/release-batch-1031/summary.md`; plan complete
