# 228 — fleet OTA to fork.122 (planned fork.121) + tunnel 80 relay auth + latency audit (fleet-ota-1106)

User approval 2026-11-06 ~18:40 TW: upgrade every online FLVX agent to the newest fork (fork.121),
火山广州 first, then enable relay auth on tunnel 80 「利群·广港·AWS HK」. Skip all 4 Breadcloud machines.
Priority: low latency / speed first, security balanced, no per-connection pre-work.

Run dir: /root/flvx-workers/runs/fleet-ota-1106

## Checklist
- [x] Inventory: online agents, versions, SSH paths, rule groups / 最优 pairs
- [x] Latency audit (code): relay auth, exit target guard (225), chain 谷歌/苹果 (P1-2), Reality fastest-IP pin (227), landing scan guard / anti-abuse
- [x] Baseline before OTA: per-machine agent CPU / RSS / conns; TTFB p50/p90 (leikwan-sh + dev box); rules, 沪台 6, landing links, 12 subs, 救命版 read-only
- [x] Per-agent backup + restore-agent.sh
- [x] Agent fix: target guard lock-free own-IP cache (122→61 ns, 3→0 allocs/conn) → release fork.122
- [x] Panel → fork.122 (backup pre-fork122)
- [x] 火山广州 OTA → tunnel 80 relay auth on → 利群 rules + AWS HK :10044 no-auth attack test
- [x] Canary: one quiet exit + one entry (Bestvm sg 年付 + Dmit Nathan; r2 checks: blip t8 1.72 s / r109 1.50 s, 15/15 rules = baseline)
- [x] Groups (≤4, never all hops of one rule group, keep one 最优 twin up), re-check after each
- [x] Quiet-period singles (01:49–02:31 TW): Leikwanhost 沪台IPLC, 沪日440, 沪港新春版, 沪日首发, 深港ixp1, vmsilo 沪港, Nobrand 沪港 (Nobrand 沪日 skipped: SSH port unreachable → no backup, 0 FLVX rules)
- [x] Post checks: versions, target guard attack re-test on 2 exits, P1-2 on chain exits, ⚠ gone, 禁止端口扫描
- [x] Repeat baseline, before/after table, restart blips, config push counts
- [x] final.md + summary.md

## Result (2026-11-07 03:45 TW, r1 + r2)
- Panel + 25 agents on 3.0.27-fork.122 (tag → 2fd7e3e4; code 4af2a091). 24 OTAs, 0 failures, 0 rollbacks.
- Not upgraded: Breadcloud ×4 (skipped by user), Nobrand 5cm IPLC 沪日 (SSH :13000 unreachable → no backup; 0 FLVX rules),
  Dmit Echo (user's machine, no SSH key). Tcscloud Seednet was user-installed on fork.122.
- Tunnel 80 relay auth on (1 push, 0 failed probes); AWS HK :10044 no-auth → Unauthorized; 50/50 tunnels enforced.
- Post: 15/15 real rules, 7/7 landing links, 12 subs, 救命版 unchanged; TTFB p50 equal or better (t28 +15 ms = 最优 exit
  pick Datawave HK 2, its route to the probe target is 10 ms longer); agent CPU/RSS equal or lower; 0 orphans.
- Open (user): Nobrand 沪日 / Dmit Echo OTA without rollback point; NAT-host own public IP not in the target guard (AWS HK).
- Details: /root/flvx-workers/runs/fleet-ota-1106/final.md
