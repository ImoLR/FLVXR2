# 277 — release plan 273 (利群用量 / 利群计费 / 利群在线设备) as 3.0.27-fork.146, panel only

Task `release-leikwan-usage` (Claude high). User approved the 273 screens 11-17 TW (「可以 发布了」, no changes).
Base = tag 3.0.27-fork.145 (plan 274), merge `origin/feature/3.0.27-leikwan-usage` @ ec83824b.
Panel only: go-gost untouched → no agent OTA. DB: + table `leikwan_usage_snapshot`.

## Checklist
- [x] No other release worker running (leikwan-qa-fix waits for this release)
- [x] Disk check (5.5 GB free), worktree flvxx-rel273, branch maintenance/3.0.27-fork.146-leikwan-usage, merge clean
- [x] go vet + go test (go-backend, SQLite + PG contract) + npm run build — all ok (handler 373 s, repo 707 s, contract 650 s; TestLeiKwanUsage* + VisibilityContract sqlite+postgres pass)
- [ ] Push, tag 3.0.27-fork.146, CI + Images + 10 release assets, Latest
- [ ] Rollback check on a prod-DB copy (fork.146 migrates, fork.145 starts clean)
- [ ] Prod backup + rollback script, prune backups (keep 2)
- [ ] Panel swap fork.145 → fork.146
- [ ] Verify: nodes online, rules, 利群 tables identical, 4 利群入口 运行中, logs clean 15 min
- [ ] Verify: snapshot rows (usage/devices ~1 min, service/previous ~3.5 min), API vs vendor GET, read rate 15 min
- [ ] UI smoke as admin (desktop + H5 390), panel RSS before/after
- [ ] Cleanup worktrees + lab, ledger, memory, summary
