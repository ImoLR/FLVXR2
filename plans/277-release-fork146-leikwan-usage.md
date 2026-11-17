# 277 — release plan 273 (利群用量 / 利群计费 / 利群在线设备) as 3.0.27-fork.146, panel only

Task `release-leikwan-usage` (Claude high). User approved the 273 screens 11-17 TW (「可以 发布了」, no changes).
Base = tag 3.0.27-fork.145 (plan 274), merge `origin/feature/3.0.27-leikwan-usage` @ ec83824b.
Panel only: go-gost untouched → no agent OTA. DB: + table `leikwan_usage_snapshot`.

## Checklist
- [x] No other release worker running (leikwan-qa-fix waits for this release)
- [x] Disk check (5.5 GB free), worktree flvxx-rel273, branch maintenance/3.0.27-fork.146-leikwan-usage, merge clean
- [x] go vet + go test (go-backend, SQLite + PG contract) + npm run build — all ok (handler 373 s, repo 707 s, contract 650 s; TestLeiKwanUsage* + VisibilityContract sqlite+postgres pass)
- [x] Push, tag 3.0.27-fork.146 (→ cc58ad6a), CI Build Check + Images 44153234441 green, 10 release assets, Latest
- [x] Rollback check on a prod-DB copy (fork.146 migrates, fork.145 starts clean, 0 restarts, rules 54/7)
- [x] Prod backup /opt/flvx-svc/rollback/pre-fork146-20261117T191027Z + rollback.sh, pruned (kept pre-fork145 + pre-fork146)
- [x] Panel swap fork.145 → fork.146 (11-18 03:10:28–03:10:36 TW)
- [x] Verify: 30/30 online, 54 rules, 利群 tables identical, 4 利群入口 运行中, 35 redeploys failed=0, logs clean
- [x] Verify: snapshots usage 03:11:16 / devices 03:11:31 / service 03:13:46 / previous 03:14:02, no errors; API = stored vendor data (separate python GET got 403 UA block → not retried); steady 2 reads / 5 min
- [x] UI smoke as admin (desktop + H5 390, 0 page errors), RSS 39 → 93 MiB after restart
- [x] Cleanup worktrees + lab, ledger, memory, summary

## Finding during release (not caused by it)
Since 11-18 01:26 TW the vendor refuses 利群 client builds < 2026.11.13.2 (HTTP 426 `client_version_too_old` on
/api/client/configs/pull). 火山上海 + 火山广州 run 2026.10.12.1 → lease renewals fail, vendor shows them offline
(the new 利群在线设备 card shows it). 阿里广州 + 百度云广州 run 2026.11.15.1 and are fine. Not changed here.
