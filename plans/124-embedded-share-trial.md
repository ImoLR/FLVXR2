# 124 — embedded-share trial on 「Dmit T1」(10) (AnyTLS / Snell / Mieru end to end on prod)

Task `embedded-share-trial` (run dir `/root/flvx-workers/runs/embedded-share-trial`). Follows plan 121 (feature),
plan 123 (FLVX fork.66 on prod) and mmwx v1.4.72 / Helper v0.7.11 (`embedded-share-mmwx`). User approved the trial
2026-10-21. No code change expected; no release, no OTA, no change to existing users/nodes/packages.

## Checklist
- [x] 1. Baseline (read-only): Dmit T1 RAM/CPU/swap, official VLESS node (10015) works, x-ui ports, Helper version,
      FLVX node 34 state + its rule port range, prod panel health, existing landing servers.
- [x] 2. FLVX node 34 port range clear of 30000–30999 (change only if it overlaps); no native landing on 34.
- [x] 3. Share Dmit T1 from Custom (30000–30999, Core 128 MB / CPU 50 %, 500 conns); verify Core started, pinned,
      `protocol_caps` / `server_mode` / `busy_ports` in the share list; FLVX sync shows the server.
- [x] 4. Enable 扩展协议 (AnyTLS / Snell / Mieru) on the FLVX landing server.
- [x] 5. Temporary test user + direct grant; nodes AnyTLS, Snell, Mieru (TCP; + one UDP-underlay check).
- [x] 6. Real clients (mihomo / sing-box) from this host (+ 火山广州 if useful): TCP byte-exact, UDP, billing up+down,
      connection cap, online-IP cap, disable → disconnect, re-enable.
- [x] 7. After: official VLESS still works, Core + machine RAM/CPU, logs quiet.
- [x] 8. Cleanup: delete test nodes + user; verify no leftovers; share + 扩展协议 stay enabled (unless failed).
- [x] 9. Summary (`summary.md`, Chinese) + memory update + plan committed.

## Notes
- 04:05 TW baseline: Dmit T1 avail 182 MB, swap 219 MB used, 1 core; official VLESS (node 41) 通; prod DB backup
  `runs/embedded-share-trial/backup/gost-pre-trial-20261021T200525Z.db.gz`.
- Node 34 port `10000-65535` → `10000-29999,31000-65535` (only field changed; before JSON in run dir backup/).
- Shared 04:07 TW; FLVX Core failed `203/EXEC Permission denied`: `/opt/mmwxc` and `/etc/mmwxc` were 0700 root (pre-existing
  from the old Custom Core), Helper only sets modes on the `flvx-core` subdirs → **mmwx Helper bug**. Host fix: `chmod 0711`
  both (before = 700, saved on the host in /root/flvx-trial-20261022/perms-before.txt). Core up 04:15 TW, caps advertised.
- FLVX landing server 4 「Dmit T1」 created by sync; protocols + anytls/snell/mieru (revision 2→3).
- Temp user 47 `trial_es_10212117` + nodes 85 AnyTLS :30000, 86 Snell v4 :30001, 87 Mieru :30002 (active in 9 s).
- mihomo v1.19.32 configured only from the panel's Clash entries (+ Mieru `transport: UDP` variant, + IPv6 variants):
  TCP 4 MiB download + 2 MiB upload echo byte-exact, UDP DNS to 1.1.1.1 + 8.8.8.8 correct per target — all 4 variants;
  same from 火山广州 (leikwan-vps, 1 MiB / 256 KiB / UDP). Mieru UDP underlay upload slow (2 MiB echo 110 s).
- Billing exact: AnyTLS up 2.10 / down 6.30 MB (download billed), Snell 2.11 / 6.34, Mieru 6.40 / 14.68; billed = raw × 2
  (双向 ×1); user in/out delta = sum of node bills to the byte.
- Conn cap 2: 2nd concurrent session refused, 1st alive (all 3); control at 500: 4 concurrent OK. Online-IP cap 1:
  same IP 2nd session OK, IPv6 source refused, without cap IPv6 OK (all 3). Disable: live sessions (incl. Mieru UDP)
  cut, new refused, applied 9 s; re-enable 14 s, same ports + credentials.
- Load (30 held + 10 × 2 MiB parallel per protocol): Core RSS peak 51 MB (cap 128), CPU peak 6 %, min MemAvailable 140 MB.
  Idle after: Core 39 MB, 0 % CPU, avail 167 MB, swap unchanged 219 MB. Core logged 2 lines (expected refusals).
- Cleanup: nodes deleted after Custom confirmed, user deleted, 0 rows left; no listeners in 30000–30999. Official VLESS
  (node 41) 通 before and after. Panel healthy, 29/29 online. Share + 扩展协议 kept, no user granted yet.
