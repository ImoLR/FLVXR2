# 148 — core-pin-dht: native landing Core pin → Custom v1.4.77 (Core 2bf03c9, DHT / UDP tracker) + TIME_WAIT default 200

Approval: user 2026-10-24 ~15:45 TW 「能修就修……需要设置的你登上管理员账号去搞定」; release authorised, screenshot gate
waived 10-24. Task `core-pin-dht` (one worker: build + release + prod + native-landing OTA + Core confirm).

Base: `maintenance/3.0.27-fork.91-landing-chip-polish` (f8056d08, newest release). Branch
`feature/3.0.27-core-pin-dht` → release branch `maintenance/3.0.27-fork.92-core-pin-dht`.

## Inputs (mmwx, read 10-25 03:50 TW)
- `release-core-sync-dht` DONE: Custom **v1.4.77** (Latest, 10 assets), Core `2bf03c9ae4f46ae6880ce2df7322ab68e476cba6`
  (`Xray 26.10.21 … 2bf03c9 (go1.27.3)`), amd64 `a175bd06…764a`, arm64 `faa5932f…caab` — re-downloaded and verified
  here against the files and `checksums.txt`. All 11 mmwx Cores (incl. FLVX shared 5/7/10/16) already run it.
- `core-sync-dht`: FLVX needs **no guard flag** — the Core exempts connections on `flvxg-*` inbounds from a loopback
  source; the FLVX renderer already uses `flvxg-<port>` on 127.0.0.1. Deprecations: `freedom.domainStrategy` only warns
  at start (still works); new freedom default blocks private/reserved targets for vless/ss inbounds (FLVX already routes
  them to block via `flvx-safety`); REALITY dest must negotiate ALPN (prod dest www.lovelive-anime.jp does);
  `api adrules` geo expansion fixed in the Core; API limit 16 MiB.
- SS2022 implementation switched to XTLS's own (protocol unchanged, mmwx tested on prod). The user is still to decide on
  the mmwx side whether to keep it; FLVX native landings follow the same Core.

## Design
- `CoreVersion` v1.4.74 → v1.4.77, `CoreSHA256` → 2bf03c9 hashes; `CorePreviousSHA256` → b01adef (v1.4.74) hashes, so a
  node upgraded from today's Core keeps reconciling on it while the new download waits/retries.
- No renderer change (no guard flag, keep `freedom.domainStrategy` so the still-running old Core sees identical config
  → no revision churn, no Core reload before the admin confirms).
- 「屏蔽 BT」 hint: TCP plain BT + UDP uTP / DHT / UDP tracker recognised, encrypted BT not.
- TIME_WAIT default for NEW nodes 500 → 200: `repo.DefaultMachineTWBuckets`, model column default (default-only, no
  row rewrite), frontend `MACHINE_DEFAULTS.twBuckets`; tests updated.
- Rollout: fork.92 → prod panel → OTA native landing nodes only (21 canary with SSH backup, then 55 / any other native
  landing) → admin confirms the Core restart per server, one at a time → verify version / chip / client / BT live.

## Tasks
- [x] Pin bump + previous-pin fallback + comments
- [x] 屏蔽 BT hint text
- [x] TIME_WAIT default 200 (backend const, model default, frontend) + tests
- [x] Agent landing tests incl. real Core (new), upgrade test old b01adef → new
- [x] netns E2E: plan 107 egress, plan 127 abuse + DHT-only / UDP-tracker-only BT clients blocked, normal UDP passes
- [x] Guarded connection counts (real Core): guard hop not counted
- [x] Resource numbers old vs new Core (RSS / fd / threads)
- [x] Backend tests SQLite (+ PostgreSQL) vs base; frontend tsc / build
- [ ] Push branch, CI green, tag `3.0.27-fork.92`, release verified
- [ ] Prod panel backup + rollback dir + prune, upgrade, verify
- [ ] OTA node 21 (canary, SSH backup) → confirm Core → verify; then 55 (+ others) one at a time
- [ ] Live checks: running Core version, chip gone, real client, BT blocked live (temp node, deleted after)
- [ ] After 10 min: agent / Core RSS fd threads, panel unchanged, logs quiet
- [ ] Summary + plan complete

## Build results (2026-10-25 TW)
- Commits `5d33f21f` (pin + tests), `2d03262f` (TIME_WAIT default) on `feature/3.0.27-core-pin-dht` (base fork.91 f8056d08).
- Agent: `landing` unit tests pass; real-Core tests with Core 2bf03c9 (+ `FLVX_LANDING_OLD_CORE` = b01adef): 21 pass incl.
  `TestLandingRealCoreUpgradeRestart` (b01adef → confirmed restart → 2bf03c9; new Core advertises
  `flvx.reality_guard_hop_uncounted`, old does not), `TestGuardDestLegReleaseRealCore`, `TestChainRealCore*`,
  `TestEgressRealCore*`, `TestRealCoreCloseWaitTimeout`; `machineprotect`, `socket`, `service` ok.
- netns E2E on 2bf03c9, all PASS: `TestAbuseGuardNetnsE2E` (plan 127), `TestConnModeNetnsE2E` (plan 141),
  `TestEgressNetnsDataPlane` (plan 107), `TestOnlineIPv6NetnsE2E`, `TestGuardHopCountNetnsE2E` (new),
  `TestUTPNetnsE2E` (extended):
  - new Core, SS2022 and VLESS Reality: uTP download 0 B, 3/3 real uTP SYNs unanswered; DHT-only libtorrent 0 B,
    0 DHT replies; UDP-tracker-only libtorrent 0 B, tracker saw 0 requests; real libtorrent/Transmission DHT + tracker
    first datagrams 4/4 dropped; dig, kernel WireGuard, QUIC / STUN / NTP / WireGuard-init / plain first packets pass.
  - control b01adef: uTP blocked as before, but DHT-only and tracker-only clients finish the 4 MiB download (5–6 DHT
    replies, tracker 4 requests), 4/4 replays answered → the test sees the difference.
  - guard counts: new Core 0 sockets counted right after 20 guarded right-SNI probes, 15/15 users under limit 30
    (held total 30, 0 rejects); b01adef: 6/15 users, 41 global-limit rejects, probes 10/20.
- Test changes needed by the new Core (found by mmwx, re-checked here): freedom blocks documentation ranges like private
  ones → namespaces use 11/12.0.1.0/24; Reality dest must negotiate ALPN → test dests send ALPN; the new SS2022 client
  closes ~1 s later → conn-mode release window 6 → 7 s. Production Reality dest (www.lovelive-anime.jp) negotiates
  ALPN; prod has 0 native Reality nodes.
- Resources (`TestEgressResourceProfile`, same plan, new vs old Core): 1 node 31.8 vs 30.6 MiB; 50 nodes 34.1 vs
  34.0 MiB, 13 vs 12 threads, 108 fds both; + WARP + AI geosite 35.0 vs 33.5 MiB; **+ 屏蔽中国大陆 / 禁止大陆直连
  (geoip:cn + geosite:cn + source rule) 65.8 vs 44.1 MiB, idle 71 vs 49 MiB** (+22 MiB, Core geo matchers); 1000
  connections 143 vs 139 MiB, 2 min after 149 vs 144 MiB; CPU per step ≈ +0.2 s on rule load. Native servers run with
  MemoryMax 256 MB → headroom stays > 100 MB.
- TIME_WAIT default: prod DB copy (57 MB) migrated with fork.91 vs fork.92 code: only the node table DDL differs
  (DEFAULT 500 → 200, GORM rebuilt the table in ~0.1 s), all 83 tables' data identical, integrity_check ok, FK check
  clean, 2nd start no-op; fork.91 code on the migrated copy turns it back to DEFAULT 500 with identical data (rollback
  without `--restore-db`). Prod: 30 nodes, all already 200.
- Backend `go test ./...`: SQLite 17 ok / 15 FAIL and PostgreSQL 17 ok / 15 FAIL, both = the fork.91 release lists exactly;
  new `TestMachineTWDefaultChangeKeepsRows{SQLite,Postgres}` pass. Frontend `tsc --noEmit` 0 errors, eslint on the 2
  changed files clean.
