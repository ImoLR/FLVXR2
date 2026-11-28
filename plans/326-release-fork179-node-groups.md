# 326 — release fork.179 (plans 323 + 324 + 325: 换主入口 dropdown + 端内 平均, 节点页分组, node-group admin guard)

Task `release-fork179-node-groups` (Claude high). User OK 2026-11-28 ~18:40 TW after the plan 324
screenshots (「截图没什么问题，就安排发布吧」) and for fixing the node-group admin gap in the same batch
(「按你推荐的做」).

Base: fork.178 branch `maintenance/3.0.27-fork.178-entry-labels` @ a4547740.
Branch: `maintenance/3.0.27-fork.179-node-groups` (worktree `/root/projects/flvxx/flvxx-326`) =
323 (fast-forward, c1b83c84) + merge of 325 (b56ebb87, on top of 324 4c179b25) + this doc.
Panel only: go-gost, install scripts, compose files, workflows untouched → **no agent OTA**.
DB: no new column; 324 writes one `vite_config` row `node_page_leikwan_pos` on the first 管理分组 save
(fork.178 ignores it). Rollback = image swap.

## Checklist
- [x] 325 / 326 / fork.179 free (plans on all branches, RELEASE-LEDGER, tags)
- [x] plan 325 built + tested (contract test SQLite + PG, fails without the fix)
- [x] merge 323 + 324 + 325 (clean), no go-gost / install / compose diff
- [x] go vet, SQLite `go test ./...`, PG handler/repo/contract `-p 1`, npm build (tsc + vite)
- [ ] push branch, tag `3.0.27-fork.179`, CI + Images green, Latest, 10 assets, gost sha256, compose tags
- [ ] baseline before swap: state, 5 busiest forwards diagnose + RTT, subs through CF, public probe
- [ ] backup (pre-fork179 rollback dir) + swap, prune backups to 2 newest
- [ ] checks after: state identical, redeploys failed=0, diagnose + RTT, logs 15 min, subs byte-identical, public probe unchanged
- [ ] feature checks on prod (read-only): tab bar groups + counts, markers, 利群入口 delete greyed, 换主入口 shows 火山广州, 端内 平均, normal user refused on node-group endpoints
- [ ] prod screenshots (desktop + 390 px) into gallery 「正式环境」
- [ ] worktrees 323 / 324 / 325 removed (branches merged + pushed), lab leftovers cleaned
- [ ] ledger + memory updated, plans 323 / 324 / 325 / 326 done

## Results
