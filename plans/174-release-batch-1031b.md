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
- [x] Tag `3.0.27-fork.99` → 64a62554; Images 41180026861 green, CI 41180027874 green; validate-release RELEASE_OK (Latest, 10 assets = fork.98, pins, sha256, amd64+arm64)
- [x] Rollback dir `pre-fork99-20261031T073414Z` (quick/integrity ok); upgraded 15:34 TW, healthy in 10 s, version.json 3.0.27-fork.99-mvw2mu1d; prune: pre-fork97 + fork.96 images removed
- [x] Post-upgrade: 30/30 online, forward_port 38 = 38, 235 runtime objects identical per node, 0 orphans/missing; agents untouched (29 × fork.98, node 36 fork.95)
- [x] Prod proof (temp tunnel 106, deleted, 0 rows left): create flow 99999 + update flow 99999/0/3, ratio 1000/0/0.001 → Chinese rejection, row unchanged; 2 / 100 / 0.01 / back to 1 accepted; real UI save 双向 (desktop) + 单向 (phone) → 更新成功, DB follows
- [x] Prod UI (read-only, writes intercepted): 36/37 + recheck 3/3 — accordion one-open (desktop + phone), teal 3px bar + sticky + bold title, footer 「收起 · 名」 collapses in view, phone header pinned mid-group; desktop admin page too short to lift header to the very top (scrolls to page end — expected); no horizontal scroll on 8 phone + 7 desktop pages (`.flvx-mobile-shell` overflow-x = clip)
- [x] Resources: backend CPU 5.4 % → 5.2 %, RSS 38.8 → 46–47 MiB (after browser checks; fork.98 was ~48 MiB the same hour), DB 57.3 MB unchanged; non-request logs in the 10 min after startup = 7 lines, one pre-existing `[nftables-dns]` target-IP resync event (forward 98, node 23), 0 lines from 172/173
- [x] Node 52 (done 14:54 TW, see summary): net.conf fq_codel → fq + fleet-bbr apply.sh (backup, pids, latency, rate trees, restore.sh covers net.conf); jump hosts 2 / nbnet still reachable
- [x] Summary `runs/release-batch-1031b/summary.md`; plan complete
