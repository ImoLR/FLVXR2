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
- [x] push branch, tag `3.0.27-fork.179`, CI + Images green, Latest, 10 assets, gost sha256, compose tags
- [x] baseline before swap: state, 5 busiest forwards diagnose + RTT, subs through CF, public probe
- [x] backup (pre-fork179 rollback dir) + swap, prune backups to 2 newest
- [x] checks after: state identical, redeploys failed=0, diagnose + RTT, logs 15 min, subs byte-identical, public probe unchanged
- [x] feature checks on prod (read-only): tab bar groups + counts, markers, 利群入口 delete greyed, 换主入口 shows 火山广州, 端内 平均, normal user refused on node-group endpoints
- [x] prod screenshots (desktop + 390 px) into gallery 「正式环境」
- [x] worktrees 323 / 324 / 325 removed (branches merged + pushed), lab leftovers cleaned
- [x] ledger + memory updated, plans 323 / 324 / 325 / 326 done

## Results
- Tag `3.0.27-fork.179` → c347193b (= fork.178 a4547740 + 323 fast-forward + merge of 325 on 324 + this doc).
  Branch CI Build Check 46140480315 ok, Build and Push Images 46140816821 ok (frontend job ~10 min, no hang),
  release not prerelease, Latest, 10 assets (same set as fork.178), compose images `flvxr2-svc-*:3.0.27-fork.179`,
  PINNED_VERSION ok, gost-amd64 sha256 ok. No go-gost / install / compose / workflow diff → no OTA.
- Local tests on the merged head: go vet ok; SQLite `go test -p 1 ./...` → one failure in `tests/contract`
  (message lost: my output filter kept only the last 40 lines); the whole contract package re-run twice → ok both
  times; PG handler/repo/contract `-p 1` ok (incl. the SQLite subtests); npm build (tsc + vite) ok.
- Plan 325 on prod: before the swap (fork.178) normal user test-ns-fx got `node-group/list` = all 4 groups and
  reached the write handlers (invalid bodies used, nothing changed); after → 403 「权限不足，仅管理员可操作」 on all 5;
  admin list unchanged (ixp 3 / 中转-Akari 7 / 落地 7 / 利群入口 4).
- Backup + swap 19:50:23–19:50:32 TW (work/deploy-179.sh). Rollback dir
  `/opt/flvx-svc/rollback/pre-fork179-20261128T115022Z/` (rollback.sh → fork.178 image swap). Kept:
  pre-fork178 + pre-fork179 (prune removed pre-fork177 and the fork.176 images).
- After the swap: node / landing / forward states identical (31/33 online, 63 on / 2 off; only the usual landing
  revision bump); 31 redeploys failed=0; public probe identical (old-panel nodes back ~30 s after start, same groups,
  no 利群); subs 117/117 through CF byte-identical (last_pull restored); 5 busiest forwards diagnose 3/3, latency
  and RTT same except the known Dmit Echo → Bestvm US hop spikes on #660 (also before the swap and in fork.178's
  samples, Bestvm US redeploy unchanged); logs 15 min: only the pre-existing 妙妙屋 shared-sync error (12 h streak
  before the swap).
- Prod UI (admin, read-only, write requests blocked in the browser): tabs 综合 33 ⚠8 · 线路机 2 · IPLC 3 · IEPL 4 ·
  IXP 3 · 家宽 2 · 前置 1 ⚠1 · Akari 8 △5 · 落地 3 · 利群入口 🔒 4 ⚠2 · 其他 4 △1 = DB; ⚠/△ = 火山上海 / 火山广州
  offline, Tcscloud Seednet IPv6, 5 renewals ≤7 days; 管理分组 利群入口 delete greyed; 换主入口: desktop shows all
  4 incl. 火山广州, phone 390 list scrolls to it; 端内 legend 「平均 X ms」 for all 5 lines. Groups not saved on prod.
  Shots: gallery http://104.145.236.29:8791/node-group-tabs/ section 「正式環境」.
- Headless prod shots need the `flvx_session` cookie too, otherwise the WS is refused and the 节点 page shows
  「监控连接已断开」 (test artifact only).
- Open (not in scope): `/api/v1/node-tag/*` and `/api/v1/tunnel-group-new/*` have the same missing admin check.
