# 174 — release batch 1031b (fork.99 = plans 172 + 173) + node 52 bbr+fq

User 2026-10-31 ~14:55 TW: 「可以直接发布吧按你推荐的做就可以了」 — release 172 (API rejects tunnel `flow` ∉ {1,2} and
`trafficRatio` ∉ [0.01,100]; backend only) and 173 (规则页分组 accordion + sticky open header + teal accent + 「收起 · 隧道名」
footer; frontend only) together without further review; 173 open questions keep the worker defaults. Panel only, no agent
OTA. Node 52 火山广州 also switched to bbr+fq (option ②).

Branch `maintenance/3.0.27-fork.99-batch-1031b` from tag `3.0.27-fork.98` + merges of `origin/feature/3.0.27-traffic-ratio-cap`
(172, 789b89ca) and `origin/feature/3.0.27-rule-group-focus` (173, 6d922eff); no conflicts. No DB schema change.
Run dir `/root/flvx-workers/runs/release-batch-1031b`.

## Checklist
- [x] Gates: go build ok (vet: only the pre-existing leikwan/testapi unkeyed-fields notes, same as fork.98), backend tests SQLite 19/19 + PostgreSQL 19/19 ok (0 FAIL), frontend tsc 0 errors, eslint 94 errors (= fork.98, no new), build ok; branch CI 41178310305 green
- [x] Fresh prod DB copy: 42 tunnels, flow 1 ×2 / 2 ×40, traffic_ratio 1.0 ×42 → 0 rows outside 172's bounds (copy deleted)
- [ ] Tag `3.0.27-fork.99`, CI + Images green, release assets validated
- [ ] Panel backup + rollback dir, upgrade, version check, backups pruned
- [ ] Post-upgrade: all nodes online as before, forward_port + per-node services identical, agents not upgraded
- [ ] Prod proof (temp tunnel): `flow: 99999` / `trafficRatio: 1000` rejected in Chinese, DB unchanged; UI 单向/双向 save works
- [ ] Prod UI: rules 分组 accordion / sticky header / footer collapse (phone + desktop); no horizontal scroll on other pages
- [ ] Resources before/after (backend RSS/CPU, 0 new non-request log lines in 10 min)
- [x] Node 52 (done 14:54 TW, see summary): net.conf fq_codel → fq + fleet-bbr apply.sh (backup, pids, latency, rate trees, restore.sh covers net.conf); jump hosts 2 / nbnet still reachable
- [ ] Summary `runs/release-batch-1031b/summary.md`; plan complete
