# 321 — grant-removal cleanup, entry carrier/IPv6 labels, 线路监测 for users

Base: fork.177 release branch `maintenance/3.0.27-fork.177-snell-vendor` @ 325004c2.
Branch: `feature/3.0.27-321-entry-labels-linemon-user` (worktree `/root/projects/flvxx/flvxx-321`).
Built + screenshots (gallery http://104.145.236.29:8791/plan-321/); **released as fork.178 on 2026-11-28 06:39 TW (plan 322)**.
Work dir: `/root/flvx-workers/runs/plan-321/work` (lab scripts, prod-copy logs, reconcile script).

## A. Grant removal leaves the entry + names behind

### Finding (prod, read-only; DB copies 2026-11-28 01:37 and 02:27 TW)
- User 90 wings had user_tunnel 522 (t79 沪台), 523 (t78 沪台 v4), 524 (t74 Nathan).
  Panel log: `tunnel/user/remove` at 01:20:00 and again at 01:30:03 TW → **both**
  沪台 grants are gone; left: 524 (t74 Nathan) + 525–532 (t106–112/114, via user
  group 10 「Dmit Nathan」), none of which enters node 48. His forward on 48
  (created 01:22:24) was deleted 01:26:18. So after the first removal keeping the
  entry was right (t78 still entered 48); after the second one nothing of his needs
  node 48 any more.
- Still there: `user_server_quota` row 7378212 (user 90, node 48, 44 856 B used)
  = the 「Leikwanhost 沪台IPLC」 row in 用户设置 → 服务器, and `user_subdomain`
  8 (电信 name) + 9 (联通 name), both `active`, CF CNAMEs live (labels not written
  here: public repo).
- Cause: plan 303 round 2 made names live "while the user exists and is not
  expired/disabled" — the "no longer on the node" rule of the original design
  was never implemented (`usersub.Desired` retires only on user/node deleted).
  The per-server quota row is created by traffic accounting and nothing ever
  removes it while the user exists. A real gap, not a display bug.
- Other orphans on prod: clot88 (user 7) node 47 AWS HK quota row (494 B). No other names.

### Rule (new)
A user's names on node N and his per-server row for N exist only while he still
needs N: a grant (any status — 停用/到期 grants stay renewable) whose tunnel uses
N, a forward with a port on N (or whose tunnel passes N, quota rows), a landing
node / landing access on N, a 利群 grant entry on N. When the last one goes:
names → `retired` (reason `no_access`, 「已不在该节点」; CF record deleted by the
pass — only records the DB knows, comment flvx-user), per-server row deleted.
State-based (after every mutating POST via the kicker, and on the 10-minute
pass), so every path is covered (grant delete, batch edits, group / user-group
changes, tunnel delete or entry change, forward delete, landing revoke) and
idempotent. The "still needs" set is a superset of what creates names (same
forward / landing rows), so a name can never be created and retired in a loop.
利群 users and WG-path users are never pruned (their counted node moves around).
`FLVX_ACCESS_CLEANUP=dry` turns the automatic part off (dry run lists, apply does it).

### Tasks
- [x] repo: `UserSubdomainAccess(nodeIDs)` + `userServerNeeds` / `UserServerQuotaOrphans` / `PruneUserServerQuotaOrphans`, `UserNodeGrants`
- [x] usersub: `Facts.Access`, `Desired` → retired `no_access`; never reserve a name that would retire at once; model reason const
- [x] handler: full facts load access; kicker retires synchronously (DB) + prunes rows; 10-min pass prunes; `FLVX_ACCESS_CLEANUP=dry`
- [x] admin endpoint `POST /api/v1/node/user-access/orphans {apply}` (dry-run list / apply; `auto` flag)
- [x] admin UI: 专属域名 item 「N 个授权」 + 「来自」 grant pills, 服务器 row 「授权：…」, reason 「已不在该节点」
- [x] contract tests (SQLite + PG): forward delete keeps (grants left), one of two grants removed keeps, 停用 keeps, last grant removed retires before the response + CF record deleted + row gone + other nodes untouched, tunnel delete, user-group removal, idempotent second pass, old-panel orphan via dry run / apply, users 403, dry mode
- [x] reconcile script `work/reconcile-access.py` (lab | dry | apply) + lab dry run on a fresh prod copy (below)

## B. Entry labels 电信 / 联通 / IPv6

- [x] model `node_ddns_record.carrier` ("" auto = from source ct/cu/cm; ct/cu/cm; none) + DDNS 「运营商标签」 select (display only, never changes the sync)
- [x] repo `LoadEntryLabels()` host → {carrier, ipv6}: DDNS domains + current IPs, user names (via their record), IPv6 literals; IPv6 tag only when the domain has no A record; IPv4 / vendor names untagged
- [x] API (additive): forward list `entryLabels`, 节点链接 (`landing/node/links`) `entryLabel`, 专属域名 items `entryLabel` (+ `recordLabel` follows the override), 订阅 pool `entryLabel`, 利群 entry nodes + rule `entry.entryLabel`
- [x] UI pills: forward list / card rows + 完整入口, 节点链接 cards, 专属域名 lists (node drawer + user detail), 订阅 pool + selected rows, 利群 授权 入口 picker / picked entry / rule 入口, App 授权 「我的入口」
- [x] dropped: tags in the rule dialog's 监听IP picker — that list reads `/tunnel/user/tunnel`, which carries no addresses, so it never has options
- [x] subs unchanged: duplicate names already get ①②③ (`sublink.FinalizeNames`) → not broken → left alone; prod-DB copy: 9 subs × 13 UAs byte-identical

## C. 线路监测 for normal users (read-only)

- [x] model `line_monitor_chart.user_hidden` (default visible) + admin `chart/user-visible`
- [x] user endpoints `/api/v1/line-monitor/list|data`: visible charts only; no IPs / host / port / main domains / probe reasons / engine numbers
- [x] nav entry 线路监测 (desktop sidebar + H5 更多) for everyone; read-only page for users (no edit / delete / pause / 检测端口 / 测试连通 / 复制开启命令)
- [x] admin chart header 「用户可见」 switch (add-only)
- [x] contract tests: user → admin endpoints 403, user list / data stripped, hidden chart refused

## Verification
- [x] go vet + `go test ./...` SQLite 30/30 packages ok; PG (temp postgres:18) repo / handler / contract `-p 1` ok — no failures, nothing to compare with base
- [x] `npm run build` (tsc) ok; eslint: 0 new errors (forward.tsx a11y errors are pre-existing)
- [x] prod-DB copy (unshare -n, real binaries: fork.177 from the prod image, 321 local build): subs 117/117 identical 177 vs 321, after apply 117/117, fork.177 on the 321-migrated copy 117/117; dry run = 2 names + 2 rows; automatic first pass (~90 s) does exactly the same; user line-monitor API: 0 of 99 sensitive strings, admin endpoints 403
- [x] screenshots desktop + 390 px (admin + user, before/after, synthetic lab) → /root/mmwx-preview/plan-321/ (25/25 lab checks)
- [x] push, CI green

### Reconcile dry run (prod copy 2026-11-28 02:47 TW, `reconcile-access.py lab`)
```
names=2 serverRows=2
  name #8 wings (user 90) · node 48 Leikwanhost 沪台IPLC · <电信 name> → leikwan-hutai-ct [active]
  name #9 wings (user 90) · node 48 Leikwanhost 沪台IPLC · <联通 name> → leikwan-hutai-cu [active]
  row  #1074584 clot88 (user 7) · node 47 AWS HK · used 494 B · no overrides
  row  #7378212 wings (user 90) · node 48 Leikwanhost 沪台IPLC · used 44856 B · no overrides
```
(full output with the labels: `work/reconcile-lab-dryrun.txt`, not in the repo)

## Release recipe (panel only — no agent OTA)
go-gost, install scripts and compose files are untouched → **no OTA**. DB: two
additive columns (`node_ddns_record.carrier` varchar default '', `line_monitor_chart.user_hidden`
bool default false); fork.177 runs unchanged on the migrated DB (rehearsed).

1. Merge this branch onto the current prod branch (fork.177 `maintenance/3.0.27-fork.177-snell-vendor`
   or whatever is newer), tag `3.0.27-fork.178` (or next free), CI + Images green, 10 assets.
2. Right before the swap: `python3 work/reconcile-access.py lab` (fresh read-only prod copy →
   dry-run list). Expect the 4 rows above (more if access was removed since).
3. Backup + swap as usual (deploy script of fork.177, pre-fork178 rollback dir, prune-backups after).
   Default env = automatic cleanup: within ~90 s (or on the first admin POST) the listed names retire,
   their Cloudflare CNAMEs are deleted (CF flvx-user records −2), the 2 rows go. Cautious variant:
   add `FLVX_ACCESS_CLEANUP=dry` to the backend environment, then `reconcile-access.py dry` →
   review → `reconcile-access.py apply`, then remove the variable at the next restart.
4. Checks: prod subs through CF before / after identical (prodsubs.py of fork.175); node 48 /
   wings: names `retired/no_access`, CF records gone, wings' 服务器 rows = Nathan only; forward rows
   show 电信 / 联通 / IPv6; normal user sees 线路监测 (all 3 prod charts visible by default — decide
   first whether 「利群专线端内」 etc. should be hidden: switch 「用户可见」 per chart).
5. Rollback: image swap to fork.177 (columns stay, ignored). Retired names stay retired (a name
   is never reused; a user who regains access gets a new name). Deleted server rows are not
   restored by the rollback (only by `--restore-db`).

## Open questions (for the user) — answered 2026-11-28 ~06:30 TW: all three approved as built; all 3 prod charts stay visible to users
- Grant **expiry / 停用** keeps the names and the row (the grant is renewable); only deleting the
  grant (or the tunnel / group membership) removes them. OK?
- A deleted per-server row loses its period usage and any per-server override; re-granting the
  same node later starts that row from 0 (only when access was fully gone in between).
- 线路监测 default = every chart visible to all normal users (chart names, line labels and node
  names are shown; no IPs / domains / ports).

- [x] released: fork.178 (plan 322), cleanup on prod 06:40:49–06:40:51 TW = exactly the 4 rows of the dry run
