# 153 — OTA every online node agent to the newest fork (after plan 148)

Task `ota-all-latest` (user approval 2026-10-24 ~23:00 TW 「按理推荐的修就可以了」).
No code change, no release, no panel upgrade. Run dir: `/root/flvx-workers/runs/ota-all-latest/`.

## Target

- Newest Latest release = `3.0.27-fork.93` (plan 152, panel only). Panel runs fork.93.
- `git diff 3.0.27-fork.92 3.0.27-fork.93 -- go-gost` is empty → fork.93 agent == fork.92 agent (plan 148's fork).
- Before: 27 nodes fork.79, node 48 fork.90, native landing 21 / 55 fork.92.
- Skip 21 / 55 (already on 148's agent code; avoids a needless restart next to the landing Core on the 464 MB node 21).
- OTA path: admin API `POST /api/v1/node/upgrade` (panel sends its own version), one node at a time.

## Checklist

- [x] Read memory + previous OTA runs (reconnect-stability fork.79, ota-all-fork55); copy/adapt scripts (TARGET fork.93)
- [x] Inventory: 30/30 online, SSH probe of 23 nodes (unit, version, disk, RAM; flux_agent left alone)
- [x] Snapshot: node list, prod counts, full forward/tunnel diagnose sweep (229/235 ok, 6 old target-side fails), latency (35 tunnels, 60 s), node 48 DDNS (CT/CU = current IPv6), real client test 5 paths 3/3
- [x] Resource baseline: agent RSS/CPU/fd on 5 busy nodes (2/23/47/48/52) + panel backend ~29 MiB / 2–6 % CPU, 0 non-request log lines/10 min
- [x] Canary: node 22 (no tunnels/forwards, SSH backup) → fork.93, reconnect 4 s, diff-only redeploy 0 changes, RSS 62→58 MB
- [x] Rest one at a time (SSH backup first on 19 more nodes), busy entry nodes last: 28/28 on fork.93, reconnect 2–6 s, 0 skipped/failed. Two stops, both cleared: 52 (first check too strict — an agent restart empties runtime revisions, so the panel re-pushes the node's objects once by design; changed the check to failed/orphans only) and 39 (exit→google probes failed: node-side IPv6 ECMP with a dead RA-learned router, seen before its OTA; recheck 16/16)
- [x] After: 30/30 online, sweep 230/235 (0 regressions, 1 fixed), panel_ip_family identical, counts identical, node 48 DDNS CT/CU = current IPv6, flux_agent untouched on 23 SSH nodes
- [x] Real client test through busy entries 23 / 32 / 2 (temp admin rules via CN jump hosts, deleted) + 48 CT/CU (rule 156): 5/5 paths 3/3 before and after
- [x] Resource numbers after (agent RSS lower after restart, CPU/fd follow traffic; panel 29→33 MiB, 0 non-request log lines/10 min before and after); node backups pruned to 2 newest on 12 nodes
- [x] Memory + summary updated; plan committed as complete

## Result (2026-10-25 08:17–09:03 TW)

- 28/28 agents on `3.0.27-fork.93`; 21 / 55 stay on fork.92 (same agent code); 30/30 online; no node skipped or failed.
- Diff-only reconnect still works on fork.93 agents: forced WS reconnect on node 17 (same PID) → `tunnels unchanged=5 updated=0, commands=1, 60ms`.
  After an agent restart (OTA) the panel re-pushes the node's objects once by design (revisions are in memory).
- Rollback per node: SSH nodes `sh /root/flvxx-backup-20261025T001722Z-pre-fork93/restore-agent.sh` (→fork.79; node 48 →fork.90);
  console-only nodes 25 / 49 / 50 / 51 / 54: `systemctl stop flvxx && cp -a /etc/flvxx/flvxx.old /etc/flvxx/flvxx && systemctl start flvxx`.
- Found (pre-existing, not changed): node 39 IPv6 ECMP with a dead RA-learned router (fe80::9a03:9bff:fe03:fcc7);
  node 51 is an entry of tunnels 25–33/39/42/43 (its IPv6 is in their in_ip) but the 11 rules on them only listen on node 23
  (forward_port rows only on 23; node 51 runs just the 11 chains; rule ports refuse on 51).
