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
- [x] 2. Tests on the merged head: go vet, SQLite `go test ./...`, PG handler / repo / contract `-p 1`,
      go-gost/x landing + socket (frontend build = CI Build Check).
- [x] 3. Prod-DB copy rehearsal in `unshare -n`: fork.177 migrates (node.landing_snell / landing_mieru /
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
- Tests (11-27 ~21:00–21:35 TW, TMPDIR off /tmp, run dir runs/release-fork177-vendor-snell/work): `go vet ./...` ok;
  SQLite `go test ./...` 30/30 packages ok (handler 280 s, contract 232 s); PG 18 `-p 1`: repo ok (586 s), handler ok
  (499 s), contract ok (726 s); go-gost/x socket ok; landing: 2 failures = the same 2 plan 317 recorded as pre-existing
  (TestLandingRealCoreUpgradeRestart fails identically on unmodified fork.176 — missing download stub;
  TestEgressRealCoreAcceptsRenderedConfig needs FLVX_LANDING_GEODIR); CI Build Check 46004322474 green (incl. npm build).
- Prod-DB copy (11-27 20:58 TW, backup API, `unshare -n`, real binaries: fork.177 = local CGO_ENABLED=0 build of the
  merged head, fork.176 = `docker cp` from the 176 image):
  - real fork.177 starts on the copy: AutoMigrate adds node.landing_snell / landing_mieru / vendors / vendor_other,
    quick_check ok, startup migration gives the 8 native landing nodes their vendor; backfill through the lab API
    (backfill-prod.py via labbackfill.py): 25 set, 8 skipped (already equal), second dry run 0 → 33/33 nodes have a vendor.
  - real fork.176 starts clean on the migrated + backfilled copy (health ok, no SQL / migration errors).
  - render dumps (agent payloads, share desired, node links of all 13 servers): fork.176 vs fork.177+backfill = 48
    identical, 4 different = only the vendor rule's domain list: Dmit T1 (share) dmit.io, dmithost.net → + dmit.com;
    Bestvm US / Bestvm JP B bestvm.cloud → + vmsilo.com, xwan.world; PQS TW pqs.pw → + pqs.cloud, pni.com.tw (rule
    names unchanged; Boil Hinet 158 byte-identical). fork.176 on the migrated copy vs before: 52/52 identical (rollback proof).
  - cutover simulation (node 168 moved to a native Dmit T1 + keep row + caps): node 168 links identical.
