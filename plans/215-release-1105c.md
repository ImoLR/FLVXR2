# 215 — release-1105c: fork.116 = fork.115 + plans 211 + 212 + 213 (+ OTA Breadcloud JP / NexKR)

User approval 2026-11-05 ~17:15 TW (「都没问题…就直接发布吧…基本上都发布吧」). Second FLVX release of 11-05 TW.
Run dir: `/root/flvx-workers/runs/release-1105c`. Expected prod: fork.115 → fork.116.

**Plan 214 (node-rules-align, 路由规则 dialog) is EXCLUDED** — the user wants it reworked first. The first attempt
(release-1105b, which included 214) was stopped before any prod change; its branch was deleted.

## Inputs
- 211 `feature/3.0.27-landing-node-total` @ 40c2bbf0 — 节点总连接数 400, DB migration (totals 400, server totals → 0, shared copies → 0), agent TCP breakdown
- 212 `feature/3.0.27-traffic-stats` @ d9d099cf — 主页 服务器流量 + admin 流量排行, new table `traffic_daily` (additive)
- 213 `feature/3.0.27-sub-test-url` @ c170c8cd — sub url-test → `http://cp.cloudflare.com/`
- release branch `maintenance/3.0.27-fork.116-release-1105c` = 5950d0e8 (fork.115 docs head a5167651 + 3 merges) + this doc

## Checklist
- [x] Release branch from 5950d0e8 (all 3 heads + fork.115 are ancestors); stale 1105b branch deleted
- [x] Gates on release head: go build, go test SQLite + PG contract, gost landing tests, tsc, vite build, eslint no new errors vs fork.115
- [x] Baselines (pre): state, counters, rule client checks, landing links (incl. Boil Hinet 158), sub pulls (Clash + sing-box), 救命版, res, API latency, landing limits as stored
- [x] Push branch, CI green; tag `3.0.27-fork.116`; Images + Release green; assets/pins/sha256/arches verified
- [x] Open a fork.115 browser profile on prod (kept across the upgrade)
- [x] Backup (validated DB) + rollback dir + local image tags; prune backups (keep 2)
- [x] Upgrade /opt/flvx-svc panel; migration check (node totals 400, server totals 0, nothing else changed); no landing link dropped
- [x] Post checks = baseline (state/counters/rules/landing/救命版/res)
- [x] OTA Breadcloud JP (node 21) → check; then NexKR (node 55) → check (links, conns, monitor 入站/总/上限 400 + TW/CW)
- [x] mmwx-side checks: no 「总上限待升级」 on shared nodes, TW/CW breakdown, shared revision advanced once
- [x] UI checks on prod: 流量排行 + 服务器流量 card, 节点详情 停用/启用 visible (read-only), 路由规则 dialog unchanged vs fork.115, monitor rows, Clash url-test, version.json + old profile ≤1 reload, /api/clients/ 403, traffic_daily rows
- [x] Ledger: 211–213 → 已发布 fork.116
- [x] Cleanup (temp users, temp rules, browsers), final.md / summary.md; plan marked complete

## Result (2026-11-05, 台湾时间)
- Tag `3.0.27-fork.116` → 9ca7f5fb; CI Build Check 42002326171 green (incl. PostgreSQL contract); Build and Push Images 42002622432 green;
  release Latest, 10 assets = fork.115 set, pins/sha256/amd64+arm64 verified.
- Gates: go build ok; go test PostgreSQL 25 ok / 0 fail (fork.115: 0 fail); SQLite 25 ok / 0 fail (from release-1105b, Go code identical:
  `git diff 5950d0e8 cc30bfd4 -- ':!vite-frontend' ':!plans'` empty); gost landing ok (short TMPDIR); tsc ok; build ok; eslint 94 = fork.115 94, 0 new.
- Panel 17:52:41 → healthy 17:52:51. Migration: 7 landing nodes total 400; servers 2/4/6/7 max/default 1000/500/800/800 → 0; nodes 105/132/133
  入站 800 → 0; nothing else changed (shared_caps only gained `node_total`).
- OTA Breadcloud JP (21) 17:59:54 → online 6 s; NexKR (55) 18:03:04 → online 6 s; both payload `conn_mode inbound` + `max_total_connections 400`,
  landing Core untouched (same sha, not restarted); monitor shows TIME_WAIT/CLOSE_WAIT.
- Business: 10 real rules + 6 temp tunnels + 7 landing links = baseline (two transient blips re-tested OK, same signature seen on fork.115);
  救命版 47/48 unchanged; sub Clash/sing-box url-test `http://cp.cloudflare.com/`; old fork.115 tab → fork.116 with 1 reload, no loop.
- Rollback: `/opt/flvx-svc/rollback/pre-fork116-20261105T095229Z/rollback.sh [--revert-limits | --restore-db]`; agents
  `ssh mmwx-s11|fleet-nexkr sh /root/flvxx-backup-20261105T095946Z-pre-fork116/restore-agent.sh`.
