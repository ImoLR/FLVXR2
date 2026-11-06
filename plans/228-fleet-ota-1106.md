# 228 — fleet OTA to fork.121 + tunnel 80 relay auth + latency audit (fleet-ota-1106)

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
- [ ] Panel → fork.122 (backup pre-fork122)
- [ ] 火山广州 OTA → tunnel 80 relay auth on → 利群 rules + AWS HK :10044 no-auth attack test
- [ ] Canary: one quiet exit + one entry
- [ ] Groups (≤4, never all hops of one rule group, keep one 最优 twin up), re-check after each
- [ ] Quiet-period singles: Leikwanhost 沪台IPLC, 沪日 / 沪港 IPLC entries
- [ ] Post checks: versions, target guard attack re-test on 2 exits, P1-2 on chain exits, ⚠ gone, 禁止端口扫描
- [ ] Repeat baseline, before/after table, restart blips, config push counts
- [ ] final.md + summary.md
