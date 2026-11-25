# 305 — 用户专属域名: one name per DDNS record + 已有转发保持原地址 (release 303 as fork.169, 305 as fork.170, turn on Leikwanhost 沪台IPLC)

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
- [ ] go test ./... SQLite + PG
- [ ] tsc + npm run build
- [ ] Prod-DB copy: fork.168 vs fork.169 subs (100/104 expected, same 4 ipv6 lines)
- [ ] Push branch, CI green, tag 3.0.27-fork.169, Images, 10 assets, sha256
- [ ] Migration rehearsal on prod-DB copy (fork.169 migrates, fork.168 starts clean)
- [ ] Baseline (5 busiest forwards diagnose + RTT), backup, swap, light check
- [ ] Ledger row 303 → 已发布 fork.169

### B
- [ ] Model: user_subdomain.record_domain, user_subdomain_keep table
- [ ] Planner: per (user, node, record) names; usage excludes kept refs
- [ ] Repo: targets per DDNS domain; Resolve per record + per forward / landing keep
- [ ] Handler: CNAME target per record; toggle-on keep set; admin views (record label, kept tag)
- [ ] All hand-out paths pass the forward / landing ref (forward list, package, copy links, subs, landing links, server list)
- [ ] Frontend: checkbox 「已有转发保持原地址」, record label chips, grey 「保持原地址」 tag
- [ ] Tests: per-record names, prefix change, kept forward byte-identical, new forward gets names, vendor v4 untouched; SQLite + PG
- [ ] Real CF once (flvxtest- records, cleaned up)
- [ ] Prod-DB copy: fork.169 vs fork.170 off / 沪台 on+keep (identical)
- [ ] Screenshots → gallery section 「305」

### C
- [ ] Push, CI, tag 3.0.27-fork.170, Images, assets
- [ ] Rehearsal on prod-DB copy, backup, swap, light check
- [ ] Toggle on 沪台 with keep ON; verify 0 names + byte-identical subs/addresses
- [ ] Ledger, memory (fork-release-and-deploy, entry-domains), summary
