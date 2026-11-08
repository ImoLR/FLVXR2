# 241 — dns-resweep: full DNS / rule / Reality re-sweep after the local DNS cache rollout (report-only)

Task brief: `/root/flvx-workers/runs/dns-resweep/brief.md`. Baseline: `runs/dns-rule-sweep-1108/` (11-08). Rollout: plan 239 (`runs/dns-local-cache/`).
Run dir + scripts: `/root/flvx-workers/runs/dns-resweep/work/` (driver `night.sh`, unit `flvx-dnsresweep-night`).
Report-only: no config changes, no restarts, no OTA, no release. Only temp admin `dns-` landings (deleted at the end).

## Checklist
- [x] Read memory, 11-08 sweep report + scripts, plan 239 rollout result
- [x] Copy scripts to the new run dir; adapt to the cache (dnscache discover section, dnsmasq upstreams probed, cold-name test,
      Echo via fleet key, dynamic pass2 links, mmwx admin-link fetch + owner check, kit binaries to the Shanghai box)
- [x] New `ip_watch.py` (05:00 stale-IP watch, DNS only) + `resample.py`; dry-run outside the window
- [x] 01:30 p1 full pass (DB backup + prune, temp landings, DNS, e2e CN/US, Reality) — done 02:12; extra: PQS UDP-DNS filter check, Breadcloud JP/JP2 outage from 02:08 logged
- [x] ~03:00 resample (Go path ×120 + 40 cold names) — done 03:08
- [x] 04:35 + 05:02–06:30 IP-change watch every 5 min; boil state read-only on mmwx-prod
- [x] 06:10 pass2 on the 5 家宽
- [x] Cleanup: temp nodes deleted (0 `dns-%` rows), scratch dirs + test clients gone
- [x] Compare with 11-08 baseline; stale-window table; 04:00 recommendation
- [x] sweep-report.md + final.md + summary.md; memory `dns-rule-sweep-1108` status line

## Result (11-09 06:50 TW)
- 5 s stalls 164/5840 → 1/5120 (Nobrand KFC 1cm, resolved, no dnsmasq); e2e 5 s-shaped 8 → 0; dnsmasq 23/23 up, 0 restarts, RSS 2–5 MB, no revert.
- 05:00: DDNS done 05:08 (158/马年/PQS; HKT 429, 黑五 redial failed); our dnsmasq hosts ≤38 s; CN resolvers 223.5.5.5 ≤50 min, vendor DNS ≤70 min, flapping → recommend 04:00.
- New P1: PQS TW drops UDP DNS for more names (pchome/ruten/nasa/noaa/weather.gov), TCP ok → proposal `server=127.0.0.1#5335` on PQS (await user).
- Breadcloud JP: NodeQuality benchmark via root password login 01:14 → OOM killed flvxx, /dev/null deleted → SSH dead 01:46–04:33 (hypervisor reboot); Breadcloud JP+JP2 network outage 02:08–02:44.
- Cleanup: 0 `dns-%` rows, scratch dirs/clients gone. Report: runs/dns-resweep/sweep-report.md, final.md.
