# 322 — release fork.178 (plan 321: grant-removal cleanup, 电信/联通/IPv6 tags, 线路监测 for users)

Task `release-fork178-entry-labels` (Claude high). User OK 2026-11-28 ~06:30 TW
(「可以，就这么发布吧，然后这么做就行了」): grant expiry / 停用 keeps names + rows (as built);
a deleted server row drops its cycle usage + override (as built); 线路监测: all 3 prod charts stay
visible to users — no 「用户可见」 switch is flipped.

Base: fork.177 branch `maintenance/3.0.27-fork.177-snell-vendor` @ 325004c2.
Branch: `maintenance/3.0.27-fork.178-entry-labels` = fast-forward to plan 321 head ac5b4bad
(+ this doc). Worktree `/root/projects/flvxx/flvxx-322`.
Panel only: go-gost, install scripts, compose files, workflows untouched (`git diff 325004c2 ac5b4bad`
on those paths is empty) → **no agent OTA**. Default env = automatic cleanup.

## Checklist
- [x] 322 / fork.178 free (plans on all branches, RELEASE-LEDGER, tags)
- [x] branch = fast-forward of 321 (CI 46073084592 green on ac5b4bad) → tests not re-run
- [ ] push branch, tag `3.0.27-fork.178`, CI + Images green, Latest, 10 assets, gost sha256, compose tags
- [ ] baseline before swap: state, 5 busiest forwards diagnose + RTT, subs through CF
- [ ] reconcile dry run on a fresh read-only prod copy (expect wings 沪台 电信 + 联通 names, wings 沪台 row, clot88 AWS HK row)
- [ ] backup (pre-fork178 rollback dir) + swap, prune backups to 2 newest
- [ ] cleanup happened within ~90 s: names retired/no_access, CF CNAMEs gone, rows gone, wings 服务器 = Nathan only
- [ ] checks after: state identical, redeploys failed=0, diagnose + RTT, logs, subs byte-identical, entry tags, user 线路监测 read-only + admin 403
- [ ] lab dir /root/flvx-tmp/r321 deleted, worktree flvxx-321 removed (branch merged + pushed)
- [ ] ledger + memory updated, plan 321 + 322 done

## Results
(filled in during the release)
