# 320 — wings: mmwx user → own FLVX normal user (ops, no code)

Task `wings-to-flvx` (2026-11-28 TW). Prod panel fork.177, admin API only (same calls as the UI).
Run dir `/root/flvx-workers/runs/wings-to-flvx/` (work/, summary.md, rollback). Inventory:
`/root/flvx-workers/runs/wings-to-flvx-inventory/inventory.md`.

User decisions (final, 11-28): normal user `wings`, expiry 2027-03-24 00:00 TW, 600 GB 双向, reset day 25,
总连接数 150, 总 IP 数 5; 2 nodes on landing server 7 (Boil Hinet 158 shared core, 30000–30999; SS2022 + VLESS
Reality Vision) = one-off user-approved exception to the 158 hands-off rule (hot add only); tunnel grants only
(no forwards): 沪台 v6 always, 沪台 v4 (tunnel 78) only if fwd 113 was really used, Dmit Nathan; caps 沪台 200 GB /
Dmit 100 GB, expiry = user expiry; 1 subscription with the 2 nodes; delete dead FLVX fwd 140 + 救命版 fwd 91 (+ its
tunnel if unshared); live 救命版 fwd 90 + FLVX fwd 113 stay (delete script prepared, not run); 1 test per node.

## Checklist
- [x] Read memory + inventory; map API calls (user/create, landing/user/save, landing/node/create, tunnel/user/assign, sub/create)
- [x] Validated gost.db backup before the first change
- [x] fwd 113 usage check (DB snapshots 11-08 … 11-28) → v4 decision
- [x] 158 read-only baseline (official xray PID, mmwxc-flvx-core PID, listeners)
- [x] Create user `wings` (password → secrets/wings-flvx.pass 0600)
- [x] Grant landing server 7
- [x] Create 2 landing nodes (SS2022 + VLESS Reality Vision) — hot add on 158 shared core
- [x] Tunnel grants (沪台 v6, 沪台 v4 per decision, Dmit Nathan)
- [x] Subscription for wings (2 nodes) → sub URL in secrets file
- [x] Back up + delete FLVX fwd 140 and 救命版 fwd 91 (+ tunnel 104 only if unshared)
- [x] Prepare (not run) delete script for 救命版 fwd 90 + FLVX fwd 113
- [x] Real-Core test of each new node, billing to wings, no leftover session
- [x] 158 PIDs unchanged + usb2 nodes 132/133 still work
- [x] Rollback script (work/rollback.py, work/created.jsonl); memory updated; plan committed complete

## Outcome (2026-11-28 01:00–01:15 TW)
- User 90 `wings` (role user): exp 1805817600000 (2027-03-24 00:00 TW), 600 GB, reset 25, 总连接数 150, 总 IP 数 5;
  defaults kept: 总转发数 10, 利群转发数 3, 默认速率 0, no groups, landing max nodes = global (5), rule caps 5/10 global.
- Landing server 7 granted; nodes 235 「台灣 Hinet · SS」 :30002 SS2022 aes-128 + 236 「台灣 Hinet · Reality」 :30003
  VLESS Reality Vision (dest www.airbnb.com, server guard on). Server 7 rev 78 → 82 applied. 158: xray PID 2128826,
  mmwxc-flvx-core 4012323, helper 4011853 unchanged; usb2 132/133 still carrying traffic.
- Grants: user_tunnel 522 (tunnel 79 沪台, 200 GB), 523 (tunnel 78 沪台 v4, 200 GB), 524 (tunnel 74 Dmit Nathan, 100 GB).
  v4 granted: fwd 113 carried ~16.4 GB 11-01 → 11-28 (0.004 GB in Oct; ~0.07 GB last 24 h), from 16 prod DB copies.
- Sub 39 「wings · 台灣 Hinet」 owned by wings (2 direct entries).
- Test (dev box, mihomo → 158 Core): both nodes 5/5, exit 114.46.101.42, ~28 Mbps; billed to wings (40 MB 双向); 0 ESTAB after.
- Deleted: FLVX fwd 140 (AWS HK :10025), 救命版 fwd 91 (JP-B :13068; tunnel 104 kept, shared with fwd 95).
- Pending (script ready, not run): FLVX fwd 113, 救命版 fwd 90 + tunnel 103 → run dir work/pending-delete.py.
- Backups: run dir work/flvx-gost.db.pre320-20261127T165652Z.zst, work/oldpanel-gost.db.pre320-20261127T170810Z.zst;
  rollback work/rollback.py [account|fwd140|old91|all] --apply.
