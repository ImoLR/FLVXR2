# 321 — grant-removal cleanup, entry carrier/IPv6 labels, 线路监测 for users

Base: fork.177 release branch `maintenance/3.0.27-fork.177-snell-vendor` @ 325004c2.
Branch: `feature/3.0.27-321-entry-labels-linemon-user` (worktree `/root/projects/flvxx/flvxx-321`).
BUILD + screenshots only — no release, prod read-only. Gallery: http://104.145.236.29:8791/plan-321/.
Work dir: `/root/flvx-workers/runs/entry-labels-linemon-user/work`.

## A. Grant removal leaves the entry + names behind

### Finding (prod, read-only, DB copy 2026-11-28 01:37 TW)
- User 90 wings: grants 522 (t79 沪台) and 523 (t78 沪台 v4) are both gone
  (`tunnel/user/remove` 01:20:00 and 01:30:03 TW); left: 524 (t74 Nathan) +
  525–532 (t106–112/114, via user group 10 「Dmit Nathan」). No tunnel of his
  enters node 48 any more; his forward on 48 (created 01:22:24) was deleted 01:26:18.
- Still there: `user_server_quota` row 7378212 (user 90, node 48, 44 856 B used)
  = the 「Leikwanhost 沪台IPLC」 row in 用户设置 → 服务器 (no role pill), and
  `user_subdomain` 8 (电信 name) + 9 (联通 name), both `active`, CF CNAMEs live (labels not written here: public repo).
- Cause: plan 303 round 2 made names live "while the user exists and is not
  expired/disabled" — the "no longer on the node" rule of the original design
  was never implemented (`usersub.Desired` retires only on user/node deleted).
  The per-server quota row is created by traffic accounting and nothing ever
  removes it while the user exists. So it is a real gap, not a display bug.
- Other orphans on prod: clot88 (user 7) node 47 quota row (494 B). No other names.

### Rule (new)
A user's names on node N and his per-server row for N exist only while he still
needs N: a grant (any status — 停用/到期 grants stay renewable) whose tunnel uses
N, a forward with a port on N (or whose tunnel passes N, quota rows), a native
landing node / landing access on N, a 利群 grant entry on N. When the last one
goes: names → `retired` (reason `no_access`, CF record deleted by the pass, only
records the DB knows / comment flvx-user), quota row deleted. State-based
(runs after every mutating POST via the kicker + periodic), so every path is
covered (grant delete, batch edits, group/user-group changes, tunnel delete or
entry change, user update) and idempotent. Grant expiry does not delete the
grant (it is disabled, renewable) → names kept (open question in the final report).

### Tasks
- [ ] repo: `UserSubdomainAccess(nodeIDs)` (entry access) + `UserNodeNeeds` / `PruneUserServerQuotaOrphans(apply)`
- [ ] usersub: `Facts.Access`, `Desired` → retired `no_access`; model reason const
- [ ] handler: facts load access; kicker retires synchronously (DB) + prunes quota rows; periodic prune
- [ ] admin endpoint `POST /api/v1/node/user-access/orphans {apply}` (dry-run list / apply)
- [ ] admin UI: which grant a name / server row comes from (专属域名 item `grants`, 服务器 row `grants`); reason text 「已不在该节点」
- [ ] contract tests: grant remove (last + not last), tunnel delete, user-group removal, batch, idempotent, quota row
- [ ] reconcile script (dry-run on a prod-DB copy, output attached) + online check

## B. Entry labels 电信 / 联通 / IPv6

- [ ] model `node_ddns_record.carrier` ("" auto = from source ct/cu/cm; ct/cu/cm; none) + DDNS section select
- [ ] repo `LoadEntryLabels()` host → {carrier, ipv6}: DDNS domains + current IPs, user names (via their record), IPv6 literals; IPv6 tag only when the domain has no A record
- [ ] API (additive): forward list `entryLabels`, tunnel lists `inIpLabels`, 专属域名 items `carrier`/`ipv6`, sub editor pool `entryLabel`, 利群 entry nodes `entryLabel`
- [ ] UI pills: forward rows + 完整入口 modal, 监听IP picker, 专属域名 lists, sub editor pool, 利群 入口机 / 换主入口 / App 我的入口
- [ ] subs unchanged: duplicate names already get ①②③ (FinalizeNames) → not broken → left alone; prod-DB copy lab: all subs × UAs byte-identical

## C. 线路监测 for normal users (read-only)

- [ ] model `line_monitor_chart.user_hidden` (default visible) + admin `chart/user-visible`
- [ ] user endpoints `/api/v1/line-monitor/list|data`: visible charts only; no IPs / host / port / main domains / reasons / engine
- [ ] nav entry 线路监测 (desktop sidebar + H5 更多) for everyone; read-only page for users (no edit/delete/pause/测试连通/复制命令)
- [ ] admin chart header 「用户可见」 switch (add-only)
- [ ] contract tests: user → admin endpoints 403, user list/data stripped, hidden chart refused

## Verification
- [ ] go vet + go test ./... (SQLite) + PG handler/repo/contract `-p 1`; compare failures with base
- [ ] npm run build (tsc)
- [ ] prod-DB copy (unshare -n): subs byte-identical base vs 321, reconcile dry-run list
- [ ] screenshots desktop + 390 px (admin + user, before/after) → /root/mmwx-preview/plan-321/
- [ ] push, CI green

## Release recipe (panel only)
(filled in at the end)
