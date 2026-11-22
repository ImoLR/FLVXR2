# 296 — khalilgao relays: 救命版 → FLVX, bind to landing nodes, subscription

Ops task (no code change, no release, no OTA). Run dir: /root/flvx-workers/runs/khalilgao-relays/ (summary.md, work/).
Landing nodes of khalilgao on 「Boil Hinet 马年」: 213 SS2022 10016, 216 VLESS Reality 10017 (admin-owned, expiry 11-30).

Scope (user, final 11-23 ~05:10 TW after two earlier changes): FLVX gives him only 沪台 + Dmit Nathan, both bound to his
landing nodes and listed in subscription 33 (沪台 via its V4 entry host). All his 救命版 rows (forwards 87/88/89 + tunnels
100/101/102) deleted; no 沪日 / Nobrand path in FLVX. FLVX forward 144 (AWS HK :10024) deleted by the coordinator (user OK).

## Checklist
- [x] Inventory both panels (救命版 87/88/89 = his, tunnels unshared; 76 targets boilhkt99u → not his; FLVX 144 + 174)
- [x] Backups: FLVX DB + 救命版 DB (zstd, quick_check ok), JSON of deleted 救命版 rows, rollback.py
- [x] Baseline measurements (GZ + AWS HK, 20 × first byte + 10 s speed per path)
- [x] FLVX forward 679 「khalilgao · Nathan → 10017」 (tunnel 74 via AWS HK/Datawave HK 2, → 216), e2e on temp port
- [x] Forward 174 renamed 「khalilgao · 沪台 → 10016」 + bound to 213
- [x] (scope 2, reverted by scope 3) 隧道分组 「Nobrand 沪日IEPL」 tunnels 119–124 + forward 680 created, then deleted, no orphans
- [x] 救命版 88/89 + tunnels 101/102 deleted (04:59 TW); 87 + tunnel 100 deleted (05:14 TW) after 679 e2e OK
- [x] 679 moved to port 24775 (links unchanged), re-tested e2e
- [x] Subscription 33 = 2 direct + 沪台 (paid-v4 host) + Nathan; checked with Clash/mihomo/NekoBox/v2rayN UAs, creds match
- [x] After measurements + warm state (174 −37.7 ms 95 %, 679 −4.3 ms 100 %) + TCP RTT per hop
- [x] Light check: 马年 node/Core unchanged, other forwards on Dmit Nathan / 沪台 OK, 救命版 73/99/91 OK (72/76 broken since 11-14)
- [x] Memories (manian-flvx-node62, old-flux-panel-fallback) + summary.md

## Finding
沪台 line (paid-v4.bgp1.leikwanhost.com:12821 → node 48) dropped to ~5 Mbps from both GZ and AWS HK after 05:19 TW
(latency unchanged; node 48 idle, no limiter; node 48 → 马年 148 Mbps) → LeiKwan side, reported to the user.
