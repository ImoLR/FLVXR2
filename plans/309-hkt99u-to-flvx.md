# 309 — Boil HKT 99u fully into FLVX, remove it from 妙妙屋 (keep users) + cross-user landing audit

Ops only (no release unless unavoidable). Task `hkt99u-to-flvx`, run dir `/root/flvx-workers/runs/hkt99u-to-flvx/`.

Boil HKT 99u = FLVX node 63 + native landing server 15 (plan 300) = mmwx server 2 (`ssh mmwx-s2`).
Reference machine: Boil Hinet 马年 (FLVX node 62 / landing server 14, plan 291).

**Status 2026-11-25 21:50 TW: STOPPED at the gate (step 0) — nothing changed anywhere.** Both mmwx users that could
use 99u are expired (king 11-24 08:00 TW, max 11-25 08:00 TW) and have no live connection, but both had traffic in
the last 7 days (king ~17 MB/day until expiry, max 248 MB on 11-23), which the brief defines as a stop. Steps 1–2
wait for the user's decision; step 3 (read-only audit) was done.

## Checklist
- [x] 0. Gate (read-only): every mmwx user / package / subscription that can use 99u (server 2 nodes), expiry, enabled,
      7-day traffic, live connections on mmwx ports; 救命版 / FLVX use of mmwx inbounds on 99u. Stop if any is live.
      → **failed (7-day traffic: king, max)** — work/gate.md
- [ ] 1a. Baseline business: 263 「🇭🇰 利群广港」 + 备线 148, 诊断, node 63 state, 救命版 forwards on 99u. (not run: gate)
- [x] 1b-read. Diff 99u vs 马年 (read-only) — work/diff-vs-manian.md
- [ ] 1b-apply. Close the gaps (FLVX DDNS takeover of boilhkt99u.imgamer.top first, journald/wtmp caps, node expiry,
      Core limits, leftover 利群 timers). (not run: gate)
- [ ] 2a. Backups: mmwx PG dump, 99u mmwx config/files tarball, FLVX DB copy; ROLLBACK.md. (not run: gate)
- [ ] 2b. Delete mmwx server 2 records (Custom server-delete: share 2, packages 8/27, nodes 110/111/136), keep users
      king / max; their subscriptions still render. (not run: gate)
- [ ] 2c. On 99u: stop + disable mmwx xray / mmw-agent / helper (files kept), mmwx ports closed, FLVX ports untouched;
      救命版 flux_agent stays (tunnel 89, 0 forwards; user rule: never stop flux_agent). (not run: gate)
- [ ] 2d. After checks: 263 + 备线 real request, 诊断, node 63 online, 救命版 forwards, other mmwx servers fine. (not run)
- [x] 3. Audit (report only): 73 landing ports (FLVX native + shared, mmwx inbounds used by FLVX / 救命版, 救命版
      forwards) → **0 cross-user**; plan 308's "same-user only" confirmed — work/audit-table.md
- [x] 4. Summary (runs/hkt99u-to-flvx/summary.md), ledger row 309, memories, plan committed.

## Notes
- Gate data (mmwx server 2): nodes 110 「自己用 ss-10016」 (:10016, imolr, no package), 111 「max ss-10015」 (:10015,
  package 8 「max 4人车」), 136 「king ss-10017」 (port :10018, package 27 「@king」); packages 8/27 have 0 assignments;
  0 user_inbound_configs; FLVX share 2 revoked (0 nodes); 0 renewal requests. The host xray config was rewritten at
  max's expiry (11-25 08:00:58 TW) and now only carries the admin key on all three inbounds. imolr :10016 traffic
  stopped 11-24 (plan 300 moved 利群 to native :30000; 救命版 fwd 76 deleted 11-24, 0 traffic ever).
- 救命版: node 60 「Boil HKT 99」 has tunnel 89 「t1」 (火山云广州 → MKCLOUD IXP 新春版 → 99u :51378), 0 forwards.
- **Hidden dependency found:** node 63 address, landing server 15 public_host, 备线 148 target and 利群 rule 1 (vendor
  forward 263 target) all use `boilhkt99u.imgamer.top`, which mmwx server 2's DDNS keeps up to date (FLVX's own record
  is `boil-hkt99u`). Before deleting server 2, FLVX node 63 must take over `boilhkt99u` (A, source 上报出口 IPv4,
  takeover; the name already exists, so no NXDOMAIN caching risk), then mmwx DDNS off. Same pattern as 马年 (plan 291).
- Machine protection: while the mmwx Helper runs it owns the nft protection (`mmwxc_machine_protection`); the FLVX
  agent takes over (`flvx_machine_protection`) once the Helper is gone (go-gost/x/machineprotect).
- Agent: 99u and 马年 both on fork.165 = newest agent in the fleet (fork.166–172 panel-only) → no agent restart needed.
