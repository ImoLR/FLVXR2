# 305 — RELEASED 11-25 18:03 TW as fork.170 (沪台 on, 保持原地址) — 用户专属域名: one name per DDNS record + 已有转发保持原地址 (release 303 as fork.169, 305 as fork.170, turn on Leikwanhost 沪台IPLC)

Task `user-subdomain-release` (coordinator 1218a2c5, 11-25 TW, Claude xhigh).
User 11-25: 「直接發佈」 for 303, and the 沪台 rules below (voice).

## Part A — release plan 303 as fork.169

- Worktree `flvxx-305`, branch `maintenance/3.0.27-fork.169-user-subdomain` =
  fork.168 branch head bad54b5d + merge `feature/3.0.27-user-subdomain` @ 78664f46
  (clean merge; only landing.go touched by both, different hunks).
- Added guard: a node with more than one DDNS domain cannot turn the toggle on
  in fork.169 (names cover the main domain only; 沪台 `-cu` would leak).
- DB: + table `user_subdomain`, + `node.user_subdomain_enabled` (default off).
- vmsilo 沪日首发 / 沪美 toggles stay OFF (the coordinator turns them on after
  the NekoBox users are told).

## Part B — plan 305 build (user decisions 11-25)

1. Nodes with several DDNS records (沪台: `leikwan-hutai-ct` AAAA main = 电信,
   `leikwan-hutai-cu` AAAA extra = 联通): one name per user per DDNS domain,
   each a CNAME to its own record domain. The entry that showed `-ct` shows the
   user's 电信 name, `-cu` → his 联通 name. Admin UI labels names by record
   (电信 / 联通 / 移动 from the record source, else 主地址 / the domain).
2. Non-DDNS entry addresses (`paid-v4.bgp1.leikwanhost.com`) untouched.
3. 已有转发保持原地址: per-node choice when the toggle goes on (checkbox, default
   ON). The set is stored explicitly: table `user_subdomain_keep` (node, kind
   forward|landing, ref id + ref created_time, user). Kept forwards / native
   landings keep their address forever (subs byte-identical, plan-302
   behaviour included); only forwards created after toggle-on get names, and
   only their owners get names. Delete + recreate = new forward. Toggle-on with
   the box OFF = full migration (the node's keep set is cleared). Admin sees a
   grey 「保持原地址」 tag on kept forwards (admin only).
4. Guard: the fork.169 multi-domain refusal is replaced by per-record support;
   every hand-out path resolves per record (no single-main shortcut left).
5. UI add-only; screenshots → gallery `:8791/user-subdomain/` section 「305」.

## Part C — release 305 as fork.170, turn on 沪台

- Branch `maintenance/3.0.27-fork.170-multi-record` from fork.169.
- After the swap: toggle on for Leikwanhost 沪台IPLC only, 「已有转发保持原地址」 ON.
  Expected: 0 names, forwards 110/113/156/173/174/347 byte-identical.
- Never: Mkcloud 沪日ixp 440 / 深港ixp 1 / 深港ixp 2.

## Checklist

### A
- [x] Worktree + merge 303 onto fork.168 (clean)
- [x] Guard: multi-domain node toggle refused (+ contract test)
- [x] go test ./... SQLite + PG
- [x] tsc + npm run build
- [x] Prod-DB copy: fork.168 vs fork.169 subs (100/104 expected, same 4 ipv6 lines)
- [x] Push branch, CI green, tag 3.0.27-fork.169, Images, 10 assets, sha256
- [x] Migration rehearsal on prod-DB copy (fork.169 migrates, fork.168 starts clean)
- [x] Baseline (5 busiest forwards diagnose + RTT), backup, swap, light check
- [x] Ledger row 303 → 已发布 fork.169

Part A result (11-25 17:37 TW): prod = fork.169 (tag → 2f2abfe2). go test ./... SQLite + PG
30/30 packages ok (contract needs `-timeout` > 10 min with PG); tsc + build ok; CI 45582352531,
Images 45584254857, 10 assets, gost sha256 ok. Prod-DB copy: subs 100/104 byte-identical (the 4 =
liuchen / test-ns-fx Clash/Stash `ipv6: false → true`, as in 303 r3), 56/56 user API views identical.
Rehearsal: fork.169 migrates the copy, fork.168 starts clean on it. Swap 17:37:33–17:37:41 TW;
31/32 online, 64 forwards / 49 tunnels / 8 subs unchanged, 31 redeploys failed=0, 5 busiest forwards
no regression. All toggles off, 0 names. Rollback `/opt/flvx-svc/rollback/pre-fork169-20261125T093732Z/`.

### B
- [x] Model: user_subdomain.record_domain, user_subdomain_keep table
- [x] Planner: per (user, node, record) names; usage excludes kept refs
- [x] Repo: targets per DDNS domain; Resolve per record + per forward / landing keep
- [x] Handler: CNAME target per record; toggle-on keep set; admin views (record label, kept tag)
- [x] All hand-out paths pass the forward / landing ref (forward list, package, copy links, subs, landing links, server list)
- [x] Frontend: checkbox 「已有转发保持原地址」, record label chips, grey 「保持原地址」 tag
- [x] Tests: per-record names, prefix change, kept forward byte-identical, new forward gets names, vendor v4 untouched; SQLite + PG
- [x] Real CF once (flvxtest- records, cleaned up)
- [x] Prod-DB copy: fork.169 vs fork.170 off / 沪台 on+keep (identical)
- [x] Screenshots → gallery section 「305」

Part B result (11-25 TW):
- `user_subdomain.record_domain` ("" = main) + table `user_subdomain_keep` (node, kind forward|landing,
  ref id + ref created_time, user). `repo.UserSubdomainTargetsOf` = main first, then every other DDNS
  domain of the node; `LoadUserEntryNames` maps each domain + its IPs to the user's name for that
  record; `ResolveForward` / `ResolveLanding` skip kept refs; `Resolve` (no ref: landing server list)
  keeps the host for a user whose refs on the node are all kept. Planner: one slot per (user, node,
  record), names only for synced records, usage without kept refs; a name whose record is gone /
  not synced pauses (node). Toggle off → on (`ddns.userSubdomainKeep`, nil = true) writes the keep
  set and the toggle in one transaction (`EnableNodeUserSubdomain`); box off clears the set. Full
  pass prunes keep rows of deleted / recreated refs. fork.169 multi-domain refusal removed.
- Admin views: node list `kept` + `records` (labels 电信 / 联通 / 移动 / 主地址 / 附加地址), name rows
  `recordLabel` / `targetDomain`, forwards `kept`, node drawer 「保持原地址」 list, forward list
  `entryKeptNodes` (admin only) → grey 「保持原地址」 tag. Users get no new fields.
- Tests: `TestUserSubdomainMultiRecordContract` (SQLite + PG): keep toggle → 0 names, users' forward
  lists / package / sub pool / landing lists / copy links + 13 sub formats byte-identical; new forward
  by usb2 → 2 names (CNAME 电信 → -ct, 联通 → -cu), new forward `name1:p,name2:p,vendor:p`, old forward
  and old sub unchanged, no -ct/-cu/IPs anywhere for the new forward; a new user swept over 13 user
  endpoints + 13 formats; prefix change on -ct → same names, no CNAME writes; delete + recreate =
  names; toggle off then on with the box off = full migration. Planner `TestMultiRecordNames`.
  303 contract tests send `userSubdomainKeep: false` (their full-migration semantics) and expect the
  new `kept` field. go test ./... SQLite + PG: 30/30 packages ok. Real Cloudflare (opt-in
  `TestUserSubdomainMultiRecordCloudflareE2E`): 2 flvxtest AAAA + 2 user CNAMEs, ready via the zone's
  authoritative servers in ~7 s, deleted, zone back to 39 records.
- Prod-DB copy (isolated): fork.169 vs fork.170 off: subs 104/104, views 56/56 identical; fork.170 +
  沪台 on with 保持原地址 (through node/update like the dialog): 0 names, forwards 110/113/156/173/174/347
  kept, subs 104/104 identical, user views identical, admin list + grey tag only.
- Gallery `:8791/user-subdomain/` section 「305」 (24/24 lab checks).

### C
- [x] Push, CI, tag 3.0.27-fork.170, Images, assets
- [x] Rehearsal on prod-DB copy, backup, swap, light check
- [x] Toggle on 沪台 with keep ON; verify 0 names + byte-identical subs/addresses
- [x] Ledger, memory (fork-release-and-deploy, entry-domains), summary

Part C result (11-25 TW): tag 3.0.27-fork.170 → 5ad3bbe6 (CI 45588971118, Images 45589267093, Latest,
10 assets, gost sha256 ok). Rehearsal: fork.170 migrates a prod-DB copy (+ record_domain, + keep
table), fork.169 starts clean on it. Swap 18:03:37–18:03:45 TW; 31 redeploys failed=0; users'
entry views identical across the swap; 5 busiest forwards no regression. 18:05:37 TW 沪台 toggle on
through node/update (the dialog's payload, records unchanged, `userSubdomainKeep: true`; expiry
reminder re-dismissed): 0 names, 6 forwards kept (110/113/173/174 imolr, 156 @jiuwei, 347 usb2);
@jiuwei / usb2 API views identical, admin list only gains the grey tag; prod snapshots before/after
rendered in the isolated lab: 104/104 subscriptions byte-identical; forwards 156 / 347 diagnose ok.
No live test forward (admins never get names; the real-Cloudflare test covered the CNAME path).
Rollback: `/opt/flvx-svc/rollback/pre-fork170-20261125T100337Z/rollback.sh` (switches 用户专属域名 off
on nodes with a keep set or several DDNS domains first, then fork.169); toggle only: node edit →
DDNS → 用户专属域名 off. vmsilo 沪日首发 / 沪美 still off — when the coordinator turns them on,
untick 「已有转发保持原地址」 (API `userSubdomainKeep: false`) for the full migration planned in 303.
