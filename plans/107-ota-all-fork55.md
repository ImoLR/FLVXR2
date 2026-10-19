# 107 — OTA all online node agents to 3.0.27-fork.55

Requested 2026-10-19 by the user ("你先上去面板把那个节点都升级了吧"). Goal: every online node runs the fork.55 agent
(= fork.54 agent code: per-node 连接面板 IPv4/IPv6 setting + IPv6 self-heal). No code change, no release, no panel upgrade.
Run dir: `/root/flvx-workers/runs/ota-all-fork55/`.

## Constraints
- Panel OTA only (`POST /api/v1/node/upgrade`), strictly one node at a time; entry nodes with many forwards last.
- Stop rule: node not back within 3 min or diagnose worse than its baseline → stop the batch, recover only that node.
- Offline nodes skipped. Node 36 (= mmwx prod host): agent only. Other panels' `flux_agent` services never touched.
- Node 21: native landing core (`flvx-landing-core.service`) must keep running and keep receiving config.
- `panelIpFamily` must stay unchanged (only 42 = `prefer_ipv4`).

## Tasks
- [x] Snapshot: node id/name/version/online/panelIpFamily, forward/tunnel counts, prod counts, latency refresh baseline
  (29/29 online at 07:21Z, 22 already fork.55, only 42 has panelIpFamily=prefer_ipv4; latency 37 tunnels / 48 ok, ~60 s)
- [x] SSH backups of the agent (binary, config.json, unit, gost.json) on every node with SSH access
  (21 30 31 34 35 36 42 45 47 52 53 → `/root/flvxx-backup-20261019T072308Z-pre-fork55/restore-agent.sh`)
- [x] Canary: one low-risk node (no forwards) → OTA → reconnect + version + diagnose vs baseline
  (34 Dmit T1 3.0.27 → fork.55: request 3 s, reconnect 4 s, NRestarts 0, flux_agent untouched, `-C` ignored as designed)
- [x] Batch: the rest one at a time (zero-forward nodes → exits → entry nodes last), 07:27–08:41Z
  - Order: 20 25 50 54 53 31 35 36 | 14 (stuck) | 17 30 52 21 | 49 39 45 12 26 42 47 18 15 | 48 (skipped) | 32 2 23 51
  - 26 nodes verified on fork.55; reconnect 2–8 s; every node's diagnose equal to its own baseline.
  - 52: panel request hit the 5-min timeout (slow CN-side mirror download), agent finished and reconnected 8 s later.
  - 21: `flvx-landing-core` kept PID 29900, `landing/desired.json` refreshed after OTA, landing server 1 online.
  - 42: batch stop at 08:24Z on transient failures outside node 42 (tunnel 8 exit 39→google:443, tunnel 11 12→14 /
    14→google); 3× re-diagnose OK, full re-diagnose = baseline 102/2 → resumed. 42 keeps `prefer_ipv4`, WS over IPv4.
  - 14 Lightlayer TW (upstream 3.0.27): request timed out; download 30%→70% by 07:45Z then hung (old agent has no read
    timeout). No second request (would race on `/etc/flvxx/flvxx.new`). Still 3.0.27, online, diagnose unchanged.
  - 48 Leikwanhost 沪台IPLC (fork.37): skipped, zero changes. Root fs 100% full: `/var/log` 8.6 GB of our agent's
    1/s `⚠️ nft quota reconcile failed: … netlink receive: no such file or directory` (same unthrottled log in fork.55);
    `/etc/flvxx/gost.json` = 0 bytes since 07:21Z. Needs cleanup + an agent fix before OTA.
  - Side effect of the first fork agent on 30/31/34/35/36 (SSH-verified; likely also 17/20/25/39, no SSH):
    `net.ipv6.conf.default.accept_ra` 1→2 (fork.36 RA compat); real interface values unchanged.
- [x] Post-batch: online 29/29 (= baseline), 27/29 on fork.55 (14, 48 not), counts 29/11/43/31/143 unchanged,
  panelIpFamily unchanged (only 42), final sweep 29 forwards + 43 tunnels: 0 regressions, tunnel 80 fixed, 7 old
  target-side failures; latency 37 tunnels / 48 ok, intervals 59.3 / 60.4 / 59.4 s; landing servers 1+2 online.
- [x] Memory + summary updated
