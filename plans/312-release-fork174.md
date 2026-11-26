# 312 — Release fork.174 (plan 310 利群 到落地 / 端内 / 线路监测 + plan 311 节点卡片标签)

Branch `maintenance/3.0.27-fork.174-latency-nodetags` = fork.173 branch head 671aa669
+ merge `feature/3.0.27-leikwan-latency-tag` @ 0ac0f15b (plan 310 r3)
+ merge `feature/3.0.27-node-card-tags` @ ef5713c6 (plan 311). Tag `3.0.27-fork.174`.
User approved the release 11-26 17:08 TW.

Scope: panel backend + frontend, go-gost agent (利群 host loop: client rtt + sock_diag 入口段).
DB: AutoMigrate adds `line_monitor_line.kind`, `line_monitor_line.leikwan_line_id`, table `line_monitor_part` (no data move).
OTA: only the 利群入口 hosts 百度云广州 + 阿里广州 (火山广州 / 火山上海 offline, skipped).

## Checklist
- [x] Disk ≥ 5 GB (go-build entries > 6 h + npm cache: 3.0 → 7.3 GB free)
- [x] Worktree + branch, merge 310 + 311 (no conflicts)
- [ ] go vet + go test ./... (SQLite) on merged head; PG run of touched packages
- [ ] go-gost/x ./socket/ tests; npm run build
- [ ] Rollback rehearsal on prod-DB copy (174 migrates, 173 starts clean on migrated copy)
- [ ] Push branch, annotated tag, CI + Images green, 10 assets, Latest, gost sha256
- [ ] Prod backup pre-fork174 + prune (keep 2)
- [ ] Panel swap + verify (health, nodes, forwards/tunnels/利群/subs, log 15 min, 节点 cards desktop + 390)
- [ ] OTA 百度云广州 + 阿里广州 (idle check, before/after node check, LeiKwan client PIDs)
- [ ] 利群汇聚 到落地 / 端内 check vs 诊断; agent ICMP log check on landings
- [ ] Data op: 线路监测 chart 「利群专线端内」 with all 专线, points after ≥ 10 min
- [ ] Cleanup worktrees 310/311 + lab dirs
- [ ] Ledger, memory, plan docs 310/311 marked released

## Notes
- Tests on merged head 2919ffe7: `go vet ./...` ok; `go test ./...` SQLite all packages ok (incl. contract
  TestLeiKwanRuleLatencyContract / TestLeiKwanEndInContract / TestLineMonitorContract); go-gost/x `./socket/` ok.
- Rollback rehearsal (prod-DB copy 11-26 17:29 TW, no network via `unshare -n`, copy deleted after):
  fork.174 starts healthy in 2 s and AutoMigrate adds `line_monitor_line.kind` + `.leikwan_line_id` + table
  `line_monitor_part`; counts unchanged (node 32, forward 65, tunnel 49, user 26, 利群 rule 6 / line 4, charts 2,
  subs 8), quick_check ok. fork.173 then starts healthy on the migrated copy, no errors (only no-network DNS noise).
  With a 4-line 「利群专线端内」 chart inserted (kind lk_endin, source 0), fork.173 ran 150 s without errors: its
  probe loop skips lines with no source node / port 0, so after a rollback that chart just shows no data.
