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
- [x] go vet + go test ./... (SQLite) on merged head; PG run of touched packages
- [x] go-gost/x ./socket/ tests; npm run build
- [x] Rollback rehearsal on prod-DB copy (174 migrates, 173 starts clean on migrated copy)
- [x] Push branch, annotated tag, CI + Images green, 10 assets, Latest, gost sha256
- [x] Prod backup pre-fork174 + prune (keep 2)
- [x] Panel swap + verify (health, nodes, forwards/tunnels/利群/subs, log 15 min, 节点 cards desktop + 390)
- [x] OTA 百度云广州 + 阿里广州 (idle check, before/after node check, LeiKwan client PIDs)
- [x] 利群汇聚 到落地 / 端内 check vs 诊断; agent ICMP log check on landings
- [x] Data op: 线路监测 chart 「利群专线端内」 with all 专线, points after ≥ 10 min
- [x] Cleanup worktrees 310/311 + lab dirs
- [x] Ledger, memory, plan docs 310/311 marked released

## Notes
- Tests on merged head 2919ffe7: `go vet ./...` ok; `go test ./...` SQLite all packages ok (incl. contract
  TestLeiKwanRuleLatencyContract / TestLeiKwanEndInContract / TestLineMonitorContract); go-gost/x `./socket/` ok.
- Rollback rehearsal (prod-DB copy 11-26 17:29 TW, no network via `unshare -n`, copy deleted after):
  fork.174 starts healthy in 2 s and AutoMigrate adds `line_monitor_line.kind` + `.leikwan_line_id` + table
  `line_monitor_part`; counts unchanged (node 32, forward 65, tunnel 49, user 26, 利群 rule 6 / line 4, charts 2,
  subs 8), quick_check ok. fork.173 then starts healthy on the migrated copy, no errors (only no-network DNS noise).
  With a 4-line 「利群专线端内」 chart inserted (kind lk_endin, source 0), fork.173 ran 150 s without errors: its
  probe loop skips lines with no source node / port 0, so after a rollback that chart just shows no data.
- PG (`FLVX_POSTGRES_TEST_DSN`, `-p 1 -timeout 40m`): handler 450 s, linemon, repo 559 s, contract 672 s — all ok.
  `npm run build` ok. Branch CI 45793118086 green. No failures, so no base comparison needed.
- Tag 3.0.27-fork.174 → 24db9ccd; Images 45795247599 + CI 45795243239 green; release Latest, 10 assets, compose pins
  `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.174`, PINNED_VERSION + REPO ok, gost-amd64 sha256 OK.
- Prod swap 18:05:49–18:05:57 TW (11-26). Rollback `/opt/flvx-svc/rollback/pre-fork174-20261126T100549Z/rollback.sh`
  (→ fork.173 image swap; new columns/table stay). Prune kept pre-fork173 + pre-fork174 (pre-fork172 + fork.171 images removed).
  Migration on prod: `line_monitor_line.kind` / `.leikwan_line_id` + table `line_monitor_part` present.
- Verify: health ok, 30/32 online as before (火山上海 / 火山广州 offline before), state snapshot (nodes, forwards,
  ports, tunnels, landings, subs, DDNS/addresses) identical before / after / final; 30 redeploys failed=0; 5 busiest
  forwards unchanged; panel log since swap: only 火山上海 timeouts (offline host). 节点 page desktop + 390: all 32 names
  full width (fork.173: several squeezed to 9 px), 32 tag lines, no overflow, ⓘ popover shows 备注 inside the viewport.
  Note: the headless check's 「监控连接已断开」 banner = forged token without the login session cookie (ws auth), not a regression.
- OTA (agent fork.143 → fork.174; agent holds no user connections, only the panel link): 百度云广州 18:10:57–18:16:06 TW,
  阿里广州 18:16:39–18:21:49 TW (GitHub download ~5 min each). All LeiKwan units (ultra-forward-263/264/280/281/282/615,
  client-agent, logswitch, front-relay, fast-failover) same PIDs before/after; agent RSS 62 / 59 MB, 10 fds, no errors.
- Before/after (诊断 ×2 each, speed 10 MB via 阿里深圳 → 入口 :10018 → 利群广港 → Boil HKT 99u): 百度云广州 129 → 98 → re-test
  132 Mbps; 阿里广州 130 → 143 Mbps; all 6 forwards 诊断 ok both times. 广台 Hinet 到落地 28.5 → 66.5 ms: LeiKwan's 广台 exit
  is now 103.167.51.166 with 38–45 ms kernel rtt / 40 ms ICMP to 马年 (福台 exit 154.206.93.227 still 10 ms); 全程 and
  端内 unchanged (24→26, 20→18 ms); vendor exit change, not the upgrade (LeiKwan clients untouched).
- After OTA: 利群汇聚 rows 到落地 on 5 forwards (广港 16, 沪日 61, 广台 Hinet 33 → 66.5 after the exit re-read, 福台 Hinet 36,
  福台 Seednet 45 ms), 广台 Seednet no tag (no 专线出口 connection on Fachost = same as 诊断 fallback, by design);
  专线 headers 端内 广港 7.6 / 沪日 30 / 广台 19 / 福台 6.5 ms, desktop + 390. Fachost outgoing ICMP = exactly 1/min
  (FLVX-PING, id = agent pid) to the 福台 Seednet exit; landing agent logs no errors (JP B: old scan noise only).
- Data op 18:27 TW: chart 3 「利群专线端内」 (lines 5–8 = 广港 / 沪日 / 广台 / 福台, 入口机 auto). 18:28–18:38: 11 samples
  each, no gaps; 无数据 7.1 % = the creation minute only.
- Cleanup: worktrees flvxx-310 / flvxx-311 removed (node_modules moved into flvxx-174), lab bulk (node-card-tags
  tmp/panel, leikwan-rule-latency shots-main) deleted; go-build entries > 6 h + npm cache trimmed earlier.
