# 291 — Boil Hinet 马年: mmwx → FLVX native landing (same ports + credentials)

Task `manian-to-flvx` (11-22 TW). Work dir `/root/flvx-workers/runs/manian-to-flvx/`.
Branch `ops/291-manian-to-flvx` (from 3.0.27-fork.157; merges fork.158 of plan 290 before tagging).

## Findings (inventory 11-22 ~19:40 TW)
- Host `ssh mmwx-hinet2` (NAT 192.168.89.38 → HiNet dynamic, boilhinet2.imgamer.top). Services: official xray
  (/opt/mmwxc/core/xray, 12311/10016/10017/10018), mmw-agent, mmwxc-helper, mmwxc-flvx-core (30000/30001),
  flux_agent (救命版, :10001 — never touch), flvx-dnscache, fail2ban, flvx-ctmon, flvx-linemon 25931 socket,
  leikwan-xray-handshake-guard timer (keeps official xray handshake 120 s).
- Inbounds (all SS2022 = 2022-blake3-aes-128-gcm):
  - 12311 SS2022 **single-key** (no clients) — FLVX fwd 587 備線·280/282, 救命版 72/73/99. Live conns.
  - 10016 SS2022 multi-user: imolr (≈1 MB test), **khalilgao (64 GB / 7 d)**, relay-out-123@system (0). Live conns.
    Rule: source geoip:cn → block (禁直连).
  - 10017 VLESS Reality **without flow**, dest lovelive, sid "": imolr (0), khalilgao (126 MB on 11-20). Idle.
  - 10018 SS2022 1 client (imolr / 「keekao ss-10018」, ≈1 MB test). Idle. Rule: source geoip:cn → block.
  - 30000 FLVX shared node 10 「keekao ss2022」 (imolr); 30001 node 95 「test」 (leekao). Idle, probe/test traffic only.
- Relays: Dmit Nathan :24775 = 救命版 flux_agent (→10017); AWS HK :10024 = FLVX fwd 144; Leikwanhost 沪台IPLC
  :12821 = FLVX fwd 174 (mmwx node 140 = paid-v4.bgp1.leikwanhost.com:12821).
- khalilgao = mmwx package 11 「马年 3人车 1/3」 (nodes 65/73/74/132/140, ends 2026-11-30, reset day 23);
  node 132 is on Nobrand 沪港IPLC (stays in mmwx). No FLVX account.
- DDNS today: mmwx server 5 ddns (provider 1) → must move to FLVX node DDNS (A, source panel, takeover).
- FLVX agent limits: native SS2022 always multi-user, VLESS flow must be vision → **code change needed**
  (single-key SS2022, VLESS without flow), else 12311 / 10017 clients break.

## Checklist
- [x] 1. Code (d0ccb0f4): agent renders SS2022 single-key (user_key "") + VLESS Reality flow ""; links/egress use the
      right password / flow; panel-made nodes byte-identical (tests). No capability gate: such nodes exist only via
      this ops import on 马年 (agent ≥ fork.159); an older agent would reject that server's whole desired state.
      go-backend `go test ./...` green, go-gost/x landing + socket green, vet clean.
- [x] 2. Lab (FLVX Core 1929c073 + xray 26.3.27 clients, real 马年 credentials): 12311 single-key ✓, 10016 khalilgao ✓
      (imolr key ✗ as expected), 10017 no-flow ✓ (vision client ✗ = same as today), 10018 ✓, wrong key ✗, UDP ✓ ×3.
- [ ] 3. Release fork.159 (= fork.158 + 291), CI + images + assets, prod panel swap (backup + rollback)
- [x] 4. Backups: FLVX DB runs/manian-to-flvx/work/flvx-gost.db.pre291-20261122T121027Z (quick_check ok); mmwx pg_dump
      mmwx-prod /root/backups/pre-manian-flvx-20261122T121351Z/mmwx.dump; host tarball 马年
      /root/backup-20261122T120806Z-before-flvx-manian/ (+ copy in work/host-backup/); rollback = work/rollback-cutover.py
- [x] 5. FLVX node 62 「Boil Hinet 马年」 (group 落地, port 40000-65535, expiry 11-27 like mmwx) + agent flvxx fork.157
      (migrate answered n: flux_agent PID 557 unchanged, official xray PID unchanged) + native landing server 14
      (range 10016–39999, Core 512 MB / 200 %). DDNS A boilhinet2 source panel + takeover, panel IP family prefer_ipv4
      (host got SLAAC IPv6 → agent used IPv6 → noaddr) → synced 114.26.132.236.
- [ ] 6. Baseline per port (direct + entry paths), real clients
- [x] 7. leekao + node 95: rows backed up (work/leekao-backup.json; 7-day traffic = probe/test only, no forwards/subs),
      node 95 deleted (shared, :30001 released), user 26 deleted.
- [ ] 8. Cutover: 30000 DONE 20:2x TW (mmwx share revoked → FLVX shared Core stopped → native node 212 hot-added, real
      client OK); 30001 deleted. 10017/10018 share the official xray process with busy 12311/10016 → all four in the
      02:00–03:45 window (removing them earlier would need an xray restart/edit = touching busy ports).
- [ ] 9. Verify each port + entry paths + forged-SNI test on 10017; latency/speed vs baseline
- [ ] 10. khalilgao FLVX subscription with the same links (2 direct + 2 relay external)
- [ ] 11. mmwx: delete server 5 (Custom server-delete, pg_dump first); FLVX shared server 3 removed
- [ ] 12. Stop/disable mmw-agent, mmwxc-helper, official xray, mmwxc-flvx-core, handshake guard (backup kept)
- [ ] 13. DDNS via FLVX (verify after the 04:00 IP change); node kit check
- [ ] 14. Panel logs clean 15 min, traffic counting on new nodes, Reality settings / 域名池 check
- [ ] 15. Summary + memories
