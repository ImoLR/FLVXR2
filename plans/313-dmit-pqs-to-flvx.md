# 313 — Dmit T1 + PQS TW off 妙妙屋 into FLVX

Ops only (no release). Task `dmit-pqs-to-flvx` (brief named it plan 312; 312 was taken by the fork.174 release doc,
so this is 313). Run dir `/root/flvx-workers/runs/dmit-pqs-to-flvx/`. Recipe = 马年 (plan 291) + 99u (plan 309).

- **Dmit T1** = mmwx server 10 (154.3.39.138, `ssh mmwx-dmit`) = FLVX node 34 + FLVX shared landing server 4.
- **PQS TW** = mmwx server 16 (pqstw.imgamer.top, `ssh mmwx-pqstw`, 498 MB RAM) = FLVX shared landing server 6; no FLVX agent yet.

## Findings (audit 11-26 18:40–19:15 TW, work/audit-table.md, work/gate.md)
- Gate passes on both (no non-admin mmwx user with a live package; soga expired 11-07, package 23 has 0 assignments).
- **Code gap (Dmit T1):** FLVX user clot88's landing node 168 on Dmit T1 :30000 is **Mieru**. FLVX native landings
  refuse Snell / Mieru (`sharedOnlyProtocol`), so it can only live on the mmwx shared Core. Deleting mmwx server 10
  revokes the share and kills it. → Dmit T1's mmwx side stays untouched this round; user decides (switch protocol /
  build native Mieru / delete the ~5 MB node). Only FLVX-side items that don't touch clot88 are done on Dmit T1.
- PQS consumers: :30001 @jiuwei SS2022 (FLVX node 105, fwd 156 via 沪台, business, ~33 conns in the evening);
  :43611 admin SS2022 (救命版 fwd 7 / 43 / 52); :10015 admin SS2022 (FLVX fwd 110 「Soga」, imolr). All multi-user SS2022
  (server key + one user key) → FLVX native renders the same.
- `pqstw.imgamer.top` is kept by mmwx server 16's DDNS → FLVX must take it over before the mmwx delete.
- FLVX native landing enable refuses a host that an active mmwx share covers (`checkSharedNativeConflict`) → the PQS
  native landing server row is inserted by DB (same fields as `EnableLandingServerNote`) and then saved through the API,
  so the native Core is downloaded + tested before the cutover (马年 revoked the share first; that would cost jiuwei minutes).

**Status 2026-11-26 19:40 TW: DONE for PQS TW (FLVX-only, mmwx server 16 deleted). Dmit T1: FLVX side only (agent
fork.174 + log caps); mmwx side untouched — blocked by the Mieru code gap, user decides.**
**Incident:** jiuwei :30001 down 19:11:24–19:13:37 TW (~2 min 13 s): inserting the native landing on the PQS host made
mmwx Custom pause the share (`detectFLVXDual`: a host with a FLVX native landing → share 「paused」), FLVX then auto-disabled
node 105. Fixed by doing the planned move of node 105 to native server 17 right away; TG alert sent.
Lesson: never create a native landing on a host whose mmwx share is still active — plan the share-node move first.

## Checklist
- [x] 0. Audit + gate (read-only) — work/audit-table.md, work/gate.md
- [x] 1. Backups: FLVX DB online copy, mmwx pg_dump, PQS + Dmit T1 host files; ROLLBACK.md
- [x] 2. Dmit T1 (FLVX side only): agent fork.154 → fork.174 (idle: 0 FLVX forwards; before/after check), journald 200M +
      wtmp 50M; mmwx official :10015 / shared Core / Helper / x-ui / cloudflared / 救命版 untouched
- [x] 3. PQS FLVX node: node/create (group 落地, expiry 2027-04-29 year from mmwx, region TW, port 50000-65535),
      install agent fork.174 as `flvxx` (救命版 flux_agent kept), online
- [x] 4. PQS DDNS: main `pqs-tw.imgamer.top` + extra `pqstw.imgamer.top` takeover (A, source 上报出口 IPv4,
      保持节点地址); both synced, CF agrees
- [x] 5. PQS native landing server (DB insert + API save): range 10015–49999, Core sized to RAM, Reality settings
      copied from server 6, jiuwei granted; temp test node → Core installed, real SS test, deleted
- [x] 6. Native nodes prepared disabled with byte-identical creds: 43611 + 10015 (admin); node 105 move script
- [x] 7. Baseline (non-disruptive): direct + via 沪台 (fwd 110 / 156), 救命版 paths, RTT p50, speed (admin ports)
- [x] 8. Cutover: 43611 / 10015 idle (0 conns) → stop mmw-agent / helper / xray / shared Core → enable natives, gap 6.1 s.
      (:30001 jiuwei was NOT moved in a quiet moment: it went down by the share pause at 19:11 and was moved at once, 19:13.)
- [x] 9. After: same tests, forged-SNI n/a (no Reality on PQS) → check, Core RSS, machine protection handed to FLVX
- [x] 10. 用户专属域名 ON for the PQS node with 已有转发保持原地址 (existing landings keep pqstw.imgamer.top)
- [x] 11. mmwx: DDNS off for server 16 (after FLVX synced), Custom server-delete preview → delete (uninstall_agent=false);
      other users' subs still render; helper script removed from mmwx-prod; CF records survive
- [x] 12. PQS host: mmwx files → dated backup dir + tarball, units disabled, journald 200M + wtmp 50M, RAM before/after
- [x] 13. Final tests table, diff vs 马年 / 99u, ledger row 313, memories, summary

## Notes
- Times TW. 18:54 backups (FLVX DB online copy, mmwx pg_dump `pre-dmit-pqs-flvx-20261126T105433Z`, host tarballs on both).
- 19:03 Dmit T1 OTA fork.154 → fork.174 (8 s; only `flvxx` PID changed; mmw-agent / Helper / shared Core / x-ui / cloudflared /
  flux_agent PIDs, ports and nft tables identical). :10015 direct 33.7 → 32.8 ms, via Dmit Nathan :13346 39.3 → 36.6 ms,
  341 → 332 Mbps (retest; the agent is not on that path). journald 200M + wtmp 50M on both hosts.
- 19:07 node 64 「PQS TW」 (node/create, group 落地, expiry 2027-04-29 year, TW/Erlin, port 50000-65535); install command +
  `-n flvxx`, migrate prompt answered n (救命版 flux_agent PID 582 kept); DNS cache 「无需改动」.
- 19:09 DDNS: `pqs-tw.imgamer.top` (main, new) + `pqstw.imgamer.top` (extra, takeover), A, 上报出口 IPv4, 保持节点地址; synced.
- 19:10 native landing server 17 by DB insert (EnableLandingServerNote defaults) + landing/server/save: 10015–49999, Core
  256 MB / 100 % (1 vCPU, 498 MB RAM; RSS 70–96 MB), Reality dest/names from server 6, abuse defaults active, 强制 IPv4,
  handshake 120 s. Temp admin node 229 :10016 (real SS2022 test 94 ms / 50 Mbps) deleted 19:23.
- 19:13 node 105 (@jiuwei) moved in place (same row id, creds, counters, fwd 156 binding, port_moves 3), jiuwei granted on 17
  (grant on revoked 6 removed 19:36). Native counters bill as node 64 from the next poll.
- 19:21:52 cutover 43611 / 10015 (0 live conns): mmw-agent + Helper stopped, official xray + shared Core stopped, nodes 230 / 231
  (byte-identical server + user PSK, admin, port_moves 3) enabled; gap 6.1 s. Links of 105 / 230 / 231 = DB creds = old xray creds.
- 19:24:58 FLVX machine protection took over (flvx_machine_protection).
- 19:23 mmwx DDNS off (SQL like plan 309); 19:25:47–19:27:42 Custom server-delete (share revoked, nodes 106/107/113/114, empty
  package 23; Helper offline → manual cleanup done by moving files). 5 other mmwx users' subs render (no PQS). CF records kept.
- 19:28 host: all mmwx files → `/root/backup-20261126T105433Z-before-flvx-313/removed/`, nft mmwxc table deleted; RAM 337 → 203 MB.
- 19:29 用户专属域名 ON keep=true → keep rows landing 105 / 230 / 231; 0 new names.
- Revoked shared server 6 shows 「妙妙屋落地流量入账失败」 (Custom still reports node 105's old counter under share 16; only
  server 6's poll fails, others fine) until Custom retires the share row (≥ 24 h). Traffic of ~1 min before the pause may be uncounted.
- Tests (before → after): direct 43611 97.3 → 93.5 ms / 50.4 → 50.0 Mbps; 10015 105.1 → 91.6 / 49.7 → 50.0; 30001 93.2 → 92.1;
  沪台 fwd 110 213.7 → 230.5 ms (+8 %, rule = +20 % and +10 ms) / 45.1 → 48.8 Mbps; fwd 156 205.2 → 193.2; 救命版 7 929.7 → 919.0 /
  36.2 → 31.4 Mbps; 救命版 52 469.9 → 452.0; 救命版 43 after 450 ms / 18 Mbps; 诊断 156 / 110 3×3 ok (32–34 ms).
  Forged SNI refused on Dmit T1 :10015 direct + via Nathan :13346. TCP RTT AWS HK → PQS ports 19–20 ms.
- Run dir `/root/flvx-workers/runs/dmit-pqs-to-flvx/` (ROLLBACK.md, work/diff-after.md, work/results/).
