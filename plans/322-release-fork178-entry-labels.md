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
- [x] push branch, tag `3.0.27-fork.178`, CI + Images green, Latest, 10 assets, gost sha256, compose tags
- [x] baseline before swap: state, 5 busiest forwards diagnose + RTT, subs through CF
- [x] reconcile dry run on a fresh read-only prod copy (expect wings 沪台 电信 + 联通 names, wings 沪台 row, clot88 AWS HK row)
- [x] backup (pre-fork178 rollback dir) + swap, prune backups to 2 newest
- [x] cleanup happened within ~90 s: names retired/no_access, CF CNAMEs gone, rows gone
- [x] checks after: state identical, redeploys failed=0, diagnose + RTT, logs, subs byte-identical, entry tags, user 线路监测 read-only + admin 403
- [x] lab dir /root/flvx-tmp/r321 deleted, worktree flvxx-321 removed (branch merged + pushed)
- [x] ledger + memory updated, plan 321 + 322 done

## Results
- Tag `3.0.27-fork.178` → ea8f75c8 (= ac5b4bad + this doc). CI Build Check 46076822387 ok, Build and Push
  Images 46076824996 ok (no hang), release not prerelease, Latest, 10 assets same set as fork.177,
  compose images `flvxr2-svc-*:3.0.27-fork.178`, PINNED_VERSION / REPO ok, gost-amd64 sha256 ok.
  Tests not re-run: fast-forward of 321 (CI 46073084592 green, full local suites in plan 321).
- Dry run 06:39:08 TW on a fresh prod copy: names=2 serverRows=2 = the expected 4, nothing more.
- Backup + swap 06:39:16–06:39:25 TW (work/deploy-178.sh). Rollback dir
  `/opt/flvx-svc/rollback/pre-fork178-20261127T223916Z/` (rollback.sh → fork.177 image swap). Kept:
  pre-fork177 + pre-fork178 (prune removed pre-fork176 and the fork.175 images).
- Automatic cleanup 06:40:49–06:40:51 TW (first pass after start): wings' 2 names on Leikwanhost 沪台IPLC
  → `retired / no_access`, both Cloudflare records deleted (authoritative NS: NXDOMAIN), row clot88 · AWS HK
  (494 B) and row wings · Leikwanhost 沪台IPLC (44 856 B) deleted. Other 7 active names untouched (all resolve).
  wings keeps rows Dmit Nathan + Datawave HK 2 + AWS HK + landing 7 (still backed by his Nathan grants:
  not orphans, not in the dry run).
- After the swap: node / landing node / forward states identical (63 on / 2 off); 62 redeploys failed=0;
  landing servers only got the usual revision bump on startup (fork.177 had the same), every landing Core
  process started days before (no restart); 5 busiest forwards diagnose 3/3 each, latency same
  (Dmit Echo #660 Bestvm US hop has occasional 300–800 ms spikes before and after, median 57 ms);
  RTT same; subs 117/117 through CF byte-identical (last_pull fields restored); logs no errors.
- User view (wings token): 线路监测 list = 3 charts (AWS HK → Seednet, Seednet → HiNet, 利群专线端内),
  data ok, 0 of 33 node addresses in the responses; admin line-monitor list/data and user-access/orphans
  → 403. No 「用户可见」 switch touched (user_hidden = 0 on all 3).
- Forward list: 20 of 65 forwards carry entry tags (电信 + IPv6 / 联通 + IPv6 = IPv6-only DDNS names,
  AAAA only); IPv4 / vendor names untagged as designed.
- No agent OTA (go-gost, install scripts, compose, workflows unchanged). Boil Hinet 158 not touched.
- Note (record only, per user rule): 火山上海 went offline around the swap; its redeploys already timed out
  before the swap.
