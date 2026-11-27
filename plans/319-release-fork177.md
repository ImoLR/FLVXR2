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
- [x] 4. Push, tag 3.0.27-fork.177, CI + Images green, Latest, 10 assets, gost sha256.
- [x] 5. Prod backup + rollback dir pre-fork177-* (→ fork.176), prune, swap, light check (state, redeploys,
      5 busiest forwards diagnose + RTT, log, Core PIDs).
- [x] 6. Vendor backfill on every node (node/vendor/set; dry run, then real); 0 ⚠.
- [x] 7. Per-landing check: 内置「屏蔽本机厂商官网（…）」 rendered on the host + live test through the landing.
- [x] 8. OTA agent fork.177 on Dmit T1 only (backup + restore script + before/after check).
- [x] 9. Dmit T1 cutover to native (official :10015 + node 168 byte-identical), then mmwx server 10 delete.
- [x] 10. Post-cutover tests (救命版 fwd 81 via Dmit Nathan + direct, node 168 Mieru TCP + UDP, latency / speed).
- [x] 11. Ledger 317 + 318 → 已发布, memory files, summary.

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
  - netns lab (TestNativeSnellMieruNetnsE2E, real Core v1.4.83 + mihomo 1.19.32) on the merged head: pass (397 s);
    connect TCP + UDP 8/8; Mieru over a UDP underlay again counts ~2× (168.5 MB for 84 MB), 4.2 Mbps down at a 10 Mbps
    cap, not cut within 10 s on disable (pre-existing, plan 317 open item).
- Release: tag 3.0.27-fork.177 → 674d1140, CI Build Check 46008473925 + Build and Push Images 46008481809 green (first
  try, no hang), Latest, not prerelease, 10 assets, compose images :3.0.27-fork.177, PINNED_VERSION / REPO ok, gost sha256 ok.
- Panel swap 21:48:16–21:48:24 TW (deploy-177.sh; rollback /opt/flvx-svc/rollback/pre-fork177-20261127T134815Z/rollback.sh
  → fork.176). prune-backups.sh after: kept pre-fork176 + pre-fork177. AutoMigrate added the 4 columns, startup
  migration gave the 8 native landing nodes their vendor. Light check: state identical (only 火山上海 came back online),
  64 reconnect redeploys failed=0, 5 busiest forwards (90 / 625 / 631 / 660 / 659) diagnose 3/3 before and after, entry
  RTT unchanged; 660's 2nd hop (Bestvm US → external target) noisy before and after (57–4133 ms, min 57 both). Log: same
  patterns as before. Rendered rules: only Dmit T1 (share) / Bestvm US / Bestvm JP B / PQS TW changed (hot), every landing
  Core PID unchanged, Boil Hinet 158 unchanged.
- Backfill 21:53:47–21:54:37 TW (backfill-prod.py --apply): 25 set (all code 0, landingServerId 0 = no landing re-sync),
  8 already equal, 0 unlisted → 33/33 nodes have a vendor, 0 「其他」. Rendered rules + Core PIDs identical to after the swap.
- Live check through each landing (work/lcheck177.py, admin nodes, one request per vendor host): NexKR, Bestvm JP B, Boil
  Hinet 马年, Boil HKT 99u, Fachost (agent fork.176, landing_guard 3): every vendor host refused by name AND by SNI; Bestvm US
  (agent fork.145) and PQS TW (fork.174): refused by name (bestvm.cloud, vmsilo.com, xwan.world / pqs.pw, pqs.cloud,
  pni.com.tw); google 204 + cloudflare 200 OK everywhere. Tcscloud Seednet has no node → no rule rendered (gets it with the
  first node). Boil Hinet 158: rendered only (helper state, domain:boil.network, unchanged), not touched.
- OTA Dmit T1 fork.174 → fork.177 21:59:46–21:59:53 TW (7 s; agent backup /root/flvxx-backup-20261127T135940Z-pre-fork177/);
  only flvxx's PID changed (mmw-agent / shared Core / Helper / 救命版 flux_agent unchanged); caps guard 3, reality 3,
  anytls 1, snell 1, mieru 1.
- Cutover (work/cutover319.py; quiet: 0 TCP / UDP flows on :10015 / :30000) 22:02:52–22:03:12 TW: native landing server 18
  「Dmit T1」 (DB insert like EnableLandingServerNote with the share's 30000-30999, then landing/server/save 10015–39999 →
  node rule range 10000-10014,40000-65535; protocols ss2022 + vless_reality_vision + mieru; Core 256 MB / 100 %; vendor DMIT
  from the node; Core + geo pre-staged from the shared Core binary / PQS TW, pinned SHA256 → no download), node 168 moved in
  place (+ keep row, port_moves 3), admin node 233 :10015 with the official creds (+ keep row). Node 168 back after ~13 s,
  :10015 gap 7.0 s. Links of 168 byte-identical; 233's link = official creds (uuid / pbk / sni / empty sid / vision).
  防盗 on (forged SNI www.cloudflare.com refused direct + via Dmit Nathan; real dest answers). Vendor rule live: www.dmit.io,
  dmit.com refused by name + SNI.
- mmwx: server 10 deleted 22:06:23–22:06:55 TW (Custom server-delete, uninstall_agent=false; only official node 41 of
  imolr); host mmwx files → /root/backup-20261127T133604Z-before-flvx-319/removed/ (142 MB), nft mmwxc table deleted, FLVX
  machine protection took over; RAM available 237 → 326 MB; Core RSS 92 MB. Backups: work/ROLLBACK.md of the run dir.
- Real client (mihomo on AWS HK; p50 ms / Mbps; 168 latency only): before → final: :10015 direct 33.0 / 1135 → 42.2 / 1078
  (min 32.2 → 31.9; +9 ms < the +10 ms rule), 救命版 fwd 81 via Dmit Nathan :13346 55.4 / 343 → 38.2 / 352, node 168 Mieru TCP
  28.9 → 28.2, Mieru UDP 30.2 → 28.3, UDP DNS ok on all four.
