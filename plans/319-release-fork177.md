# 319 — Release fork.177 = fork.176 + plan 317 (native Snell + Mieru) + plan 318 (VPS 厂商挂在节点上); vendor backfill; Dmit T1 native cutover

Task `release-fork177-vendor-snell` (Claude xhigh, coordinator 7ac749c9). User 11-27 ~20:55 TW approved both
screenshot galleries: 「可以，就按你推荐的做吧，然后把每个节点补上这个逻辑，然后帮他选上这些厂商域名，然后把落地规则生效，
然后把这一套都做完吧，然后看还有什么问题通知我。」

Branch `maintenance/3.0.27-fork.177-snell-vendor` = fork.176 branch head 4f2b5bee
+ merge feature/3.0.27-native-snell-mieru @ 90f271cc + merge feature/3.0.27-vendor-node-pick @ a5496b5a.
Conflicts (2, as predicted): `repository_landing_egress.go` node Select (vendors + landingAgentProtocolColumns),
`landing_user_view_contract_test.go` native admin key list (Snell / Mieru + vendor keys) — both sides kept.

## Checklist
- [x] 1. Worktree flvxx-319, merge 317 + 318, resolve the 2 conflicts.
- [ ] 2. Tests on the merged head: go vet, SQLite `go test ./...`, PG handler / repo / contract `-p 1`,
      go-gost/x landing + socket, `npm run build`.
- [ ] 3. Prod-DB copy rehearsal in `unshare -n`: fork.177 migrates (node.landing_snell / landing_mieru /
      vendors / vendor_other), render diff = only the 4 expected landings; fork.176 on the migrated copy renders
      byte-identical to before (rollback proof).
- [ ] 4. Push, tag 3.0.27-fork.177, CI + Images green, Latest, 10 assets, gost sha256.
- [ ] 5. Prod backup + rollback dir pre-fork177-* (→ fork.176), prune, swap, light check (state, redeploys,
      5 busiest forwards diagnose + RTT, log, Core PIDs).
- [ ] 6. Vendor backfill on every node (node/vendor/set; dry run, then real); 0 ⚠.
- [ ] 7. Per-landing check: 内置「屏蔽本机厂商官网（…）」 rendered on the host + live test through the landing.
- [ ] 8. OTA agent fork.177 on Dmit T1 only (backup + restore script + before/after check).
- [ ] 9. Dmit T1 cutover to native (official :10015 + node 168 byte-identical), then mmwx server 10 delete.
- [ ] 10. Post-cutover tests (救命版 fwd 81 via Dmit Nathan + direct, node 168 Mieru TCP + UDP, latency / speed).
- [ ] 11. Ledger 317 + 318 → 已发布, memory files, summary.

## Evidence
