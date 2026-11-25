# 309 — Boil HKT 99u fully into FLVX, remove it from 妙妙屋 (keep users) + cross-user landing audit

Ops only (no release unless unavoidable). Task `hkt99u-to-flvx`, run dir `/root/flvx-workers/runs/hkt99u-to-flvx/`.

Boil HKT 99u = FLVX node 63 + native landing server 15 (plan 300) = mmwx server 2 (`ssh mmwx-s2`).
Reference machine: Boil Hinet 马年 (FLVX node 62 / landing server 14, plan 291).

**Status 2026-11-25 23:45 TW: DONE (round 2, user GO).** 99u is FLVX-only; mmwx server 2 + users king/max deleted. Round 1 history:
**2026-11-25 21:50 TW: STOPPED at the gate (step 0) — nothing changed anywhere.** Both mmwx users that could
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

## Round 2 — GO (user 11-25 night: 「99u 现在没人在用，直接换 … 那两个用户 … 直接删除」)
Task `hkt99u-to-flvx-r2`, run dir `/root/flvx-workers/runs/hkt99u-to-flvx-r2/`. Gate lifted; delete mmwx users king + max.
Boil Hinet 158 hands off. No panel release.
- [x] R1. Backups: mmwx PG dump, 99u mmwx files tarball, FLVX DB copy; ROLLBACK.md.
- [x] R2. king / max footprint on other servers (user deletion preview) — delete only 99u parts if any.
- [x] R3. FLVX node 63 DDNS takes over `boilhkt99u.imgamer.top` (A, 上报出口 IPv4); synced; mmwx server 2 DDNS off.
- [x] R4. mmwx: delete server 2 (Custom server-delete: share 2, packages 8/27, nodes 110/111/136).
- [x] R5. mmwx: delete users king + max; other users' subs still render.
- [x] R6. 99u host: mmwx xray / mmw-agent / helper stopped + disabled + files to dated backup dir; 3 old 利群 timers off;
      journald 200M + wtmp 50M; FLVX machine protection took over.
- [x] R7. FLVX: landing server 15 Core 384 MB / 100 %; node 63 expiry 2026-12-07 monthly.
- [x] R8. Sanity: node 63 online, :30000 listening, one 诊断; 救命版 tunnel 89 state; diff table after.
- [x] R9. Remove /root/custom-api-309.sh from mmwx-prod; prune backups; memories, ledger, summary; plan committed.

### Round 2 results (11-25 23:25–23:45 TW)
- R1: mmwx-prod /root/backups/pre-hkt99u-flvx-20261125T152553Z/mmwx.dump; 99u /root/backup-20261125T152553Z-before-flvx-only/
  (mmwx-files.tgz + removed/); FLVX DB copy runs/hkt99u-to-flvx-r2/work/flvx-gost.db.pre309-20261125T152553Z; ROLLBACK.md.
- R2: king / max deletion preview: 0 packages / subs / inbound bindings / private nodes anywhere; emails only on server 2;
  latest xray snapshots of servers 9/10/12/14/15/16/17 carry neither → whole-account delete.
- R3: node 63 DDNS + record boilhkt99u.imgamer.top (A, extra, source reported, takeover) → synced 58.152.255.186, record_id
  held by FLVX; node row unchanged (keep-address). mmwx remote_servers 2 ddns_enabled → 0. (mmwx never deletes CF records:
  boilhinet2 / boil survived their server deletes.)
- R4: Custom server-delete op 4aed6100… → deleted (share revoke, packages 8/27, nodes 110/111/136, Helper uninstall,
  official server, sweep, verify); uninstall_agent=false (mmw-agent removed by hand). Left by design: mmwxc_flvx_shares /
  counters rows for share 2 (dropped by flvx_retired after 24 h), deletion + lifecycle audit rows; FLVX shared server 11
  stays as revoked history (same as 马年 server 3).
- R5: users king + max deleted (Custom user delete, 0 items). Subs of arxiv / ken / khalilgao / usb / wings render
  (/x/<code>?t=clash: 4 / 3 / 2 / 7 / 6 proxies, none 99u).
- R6: helper uninstalled by the delete; xray / mmw-agent stopped + disabled; leikwan-540-ss-sync / leikwan-backup-ss-sync /
  leikwan-xray-handshake-guard timers + services + scripts off; files moved to removed/; ports 10015/10016/10018 closed;
  journald flvx-size.conf (200M), wtmp maxsize 50M; FLVX agent log 23:33 「未再检测到妙妙屋助手，连接保护改由 FLVX 负责」,
  nft flvx_machine_protection present. RAM used 667 → 511 MB.
- R7: landing server 15 Core 256 MB / 50 % → 384 MB / 100 % (revision 28 → 29, MemoryMax 402653184, no restart);
  node 63 expiry 2026-12-07 00:00 TW, month; remark updated.
- R8: node 63 online fork.165; :30000 tcp + udp listening (flvx-landing-core); 诊断 263 all 8 items green
  (e2e 28.4 ms, landing 8.4 ms, 备线 148 10.6 ms). 救命版 node 60 / tunnel 89 「t1」 status 1, 0 forwards, :51378
  listening, flux_agent PID 502 untouched. Diff table: runs/hkt99u-to-flvx-r2/work/diff-after.md.
- R9: /root/custom-api-309.sh removed from mmwx-prod; mmwx backups pruned to 2 newest; prune-backups.sh run.

## Round 3 — tests only (user: 「正常测试都做，不用等夜里窗口」)
Task `hkt99u-to-flvx-r3`, run dir `/root/flvx-workers/runs/hkt99u-to-flvx-r3/`. Measure only: no config change, no restart.
- [x] T1. Post-change tests (11-25 23:45–23:52 TW) — all normal, no regression, no alert:
  - DDNS: node 63 `boil-hkt99u` (main) + `boilhkt99u` (extra, takeover) synced 58.152.255.186; CF API both A
    58.152.255.186 TTL 120 unproxied, record ids match; node address unchanged.
  - Real SS2022 requests (5× generate_204 + exit IP + one 30 MB download), exit 58.152.255.186 on all:
    via 263 (aliyun-sz → 百度云广州 :10018) 5/5, 173 Mbps; via 备线 148 (aliyun-sz → Mkcloud 深港ixp 1 :35046) 5/5,
    146 Mbps; direct (aws-hk → :30000) 5/5, 181 Mbps. Before (11-24, 10 MB): direct 115/132, via 263 140/151.
  - 诊断 263 ×3: 8/8 each, e2e 13 ms; 148 516 (cold) / 11.1 / 11.3 ms. RTT aws-hk → :30000 p50 3.5 / max 11.0 ms (before 2.9/2.7).
  - Host: node 63 online fork.165; landing 「利群263 99U」 active; :30000 tcp+udp; Core RSS ~123 MB (MemoryMax 384 MB,
    CPU 100 %), not restarted since 11-24 00:32; flvx_machine_protection present; 10015/10016/10018 closed; 救命版
    node 60 / tunnel 89 OK, flux_agent PID 502 untouched. No fresh pre-change baseline existed (see summary).
