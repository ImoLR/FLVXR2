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
- [ ] Gates on release head: go build, go test SQLite + PG contract, gost landing tests, tsc, vite build, eslint no new errors vs fork.115
- [ ] Baselines (pre): state, counters, rule client checks, landing links (incl. Boil Hinet 158), sub pulls (Clash + sing-box), 救命版, res, API latency, landing limits as stored
- [ ] Push branch, CI green; tag `3.0.27-fork.116`; Images + Release green; assets/pins/sha256/arches verified
- [ ] Open a fork.115 browser profile on prod (kept across the upgrade)
- [ ] Backup (validated DB) + rollback dir + local image tags; prune backups (keep 2)
- [ ] Upgrade /opt/flvx-svc panel; migration check (node totals 400, server totals 0, nothing else changed); no landing link dropped
- [ ] Post checks = baseline (state/counters/rules/landing/救命版/res)
- [ ] OTA Breadcloud JP (node 21) → check; then NexKR (node 55) → check (links, conns, monitor 入站/总/上限 400 + TW/CW)
- [ ] mmwx-side checks: no 「总上限待升级」 on shared nodes, TW/CW breakdown, shared revision advanced once
- [ ] UI checks on prod: 流量排行 + 服务器流量 card, 节点详情 停用/启用 visible (read-only), 路由规则 dialog unchanged vs fork.115, monitor rows, Clash url-test, version.json + old profile ≤1 reload, /api/clients/ 403, traffic_daily rows
- [ ] Ledger: 211–213 → 已发布 fork.116
- [ ] Cleanup (temp users, temp rules, browsers), final.md / summary.md; plan marked complete
