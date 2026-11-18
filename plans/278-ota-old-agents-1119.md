# 278 — night OTA of the 13 old-version agents → panel version (11-19 02:00–03:30 TW)

Task `ota-old-agents-1119` (Claude high). OTA only, no release. Target = what panel `node/upgrade` offers
(prod panel 3.0.27-fork.154; agent code = fork.152 = fork.147 + QUIC check).

Why: Bestvm JP B (native landing, guarded Reality :31000) runs fork.138 → guard defer 5 (~7 s silent hold);
fork.145+ = 130 (~252 s). 12 agents on fork.130/131/134 still run the old warm pool (warmPool=1, 2 per key, 30 min sleep).

Targets: Lightlayer TW, Vmsilo TW, Mkcloud 深港ixp 2, Bestvm HK STD, vmsilo 沪港, Bestvm TW, Datawave TW 1,
Fachost seednet 黑五, Dmit T1, Tcscloud Seednet, NexKR, Bestvm sg 年付, Bestvm JP B (last).
Not touched: Nobrand ×2 (fork.126), other fork.138 hosts, fork.143 ×4, Bestvm US, the 7 屏蔽 TLS entries, anything mmwx.

Business on targets: Bestvm JP B (12 forwards, 3 landing nodes), Bestvm sg 年付 (exit of 1 used tunnel), NexKR (landing
ss2022 :30002, 救命版 traffic), Dmit T1 (mmwx shared landing, mieru — mmwx process, not ours). Others: 0 forwards, 0 landings.

## Checklist
- [x] Prep: tools (runs/ota-old-agents-1119/work, from quic-block), temp speed forwards 655 (tunnel 92 → JP B) / 656 (tunnel 8 → SG)
- [x] Baseline: state, node list, rule diagnose, warm record.py, landing checks, Core / mmwx PIDs, JP B silent hold before
- [x] Canary Lightlayer TW (5 min check)
- [x] Vmsilo TW, Mkcloud 深港ixp 2, Bestvm HK STD, vmsilo 沪港, Bestvm TW, Datawave TW 1, Fachost seednet 黑五
- [x] Dmit T1 (mmwx PIDs unchanged, shared landing reachable)
- [x] Tcscloud Seednet, NexKR (Core PID unchanged, landing link e2e)
- [x] Bestvm sg 年付 (before/after diagnose + RTT + speed)
- [x] Bestvm JP B (before/after, Core PID unchanged, guard defer 130, silent hold, Reality + non-Reality probe)
- [x] After roll: diagnose vs baseline, all online, record.py compare
- [x] Cleanup temp forwards / temp files; summary, ledger, memory

## Log
- 02:05 TW prep: tools copied from quic-block (nodecheck / ota / one / backup); temp speed forwards 655 (tunnel 92, entry vmsilo 沪日首发 → exit Bestvm JP B → dev box :41287) and 656 (tunnel 8, entry Mkcloud 深港ixp 1 → exit Bestvm sg 年付); baseline pulls 40 MB in 5.6 s / 5.0 s.
- 02:06 TW baseline: 30/30 online; diagnose 64 forwards, 59 all-ok, partial/fail before any OTA: #6 2/4, #77 0/1, #78 1/2, #84 0/1, #95 0/1; record.py 24 537 streams / 43 miss; landing 193 NexKR 23.8 Mbit/s, 200/201/207 JP B 55.7/59.9/57.3 Mbit/s (20 MB each); JP B :31000 silent hold 7.6 s (defer 5); NexKR load 15 before (pre-existing).
- 02:09–02:12 TW canary Lightlayer TW → fork.154 in 11 s, warmPool=2, 0 error lines after 5 min.
- 02:14–02:23 TW Vmsilo TW, Mkcloud 深港ixp 2, Bestvm HK STD, vmsilo 沪港 → fork.154 (back in 7–10 s each); 0 forwards, 0 landings.
- (round 1 headless process exited at 02:24 before the vmsilo 沪港 after-check; round 2 ran it at 02:26: online fork.154, no regression.)
- 02:27 Bestvm TW, 02:30 Datawave TW 1 → fork.154 (7 s), 0 forwards, 0 error lines.
- 02:33 Fachost seednet 黑五 → fork.154: only flvxx PID changed; flux_agent / mmw-agent / mmwxc-helper / xray PIDs and listen ports = baseline.
- 02:36 Dmit T1 → fork.154: only flvxx PID changed; flux_agent / flvx-dnscache / mmw-agent / mmwxc-flvx-core / mmwxc-helper PIDs = baseline;
  shared landing (mieru, mmwxc-flvx-core :30000) panel state active/applied rev 130, TCP connect 208–224 ms before and after
  (one SYN-retransmit sample each run on the dev box → Dmit path; the port is served by the unchanged mmwx Core).
- 02:39 Bestvm sg 年付 → fork.154 (6 s): forward #63 diagnose 3/3 70.9 → 3/3 70.9 ms, entry RTT p50 3.7 → 3.9 ms, speed (656) 67.7 → 64.5 Mbit/s.
- 02:42 Tcscloud Seednet → fork.154: flvx-landing-core PID 429842 unchanged (no landing nodes on it).
- 02:45 NexKR → fork.154 (8 s): Core PID 3680061 unchanged; landing 193 ss2022 20 MB 27.7 → 29.4 Mbit/s, trace 2/2, egress unchanged.
- 02:50 Bestvm JP B → fork.154 (7 s): Core PID 386589 unchanged; guarded :31000 (flvxl-200) TCP_DEFER_ACCEPT 5 → **130**
  hot-applied (applied.json 02:50:53, no Core restart); 9 forwards diagnose 24/27 → 24/27 (the 3 fails = #78, partial already in
  baseline: target 126.51.211.56:6666 no route to host, logged before the OTA), hop ms and entry RTT unchanged; speed (655) 61.3 → 64.1 Mbit/s;
  landing 200/201/207 20 MB 58.9/56.4/57.9 → 26.4 (re-test 58.9, noise)/57.8/57.0 Mbit/s, trace 2/2 each (Reality client OK);
  silent conn hold on :31000 from the dev box **7.6 s → 250.8 s** (one conn); non-Reality probe still gets the dest cert
  (CN=*.lovelive-anime.jp, verify OK). JP B error lines (no route to host for that one user target) are pre-existing.
- 03:00–03:03 diagnose all: 64 forwards, 59 all-ok, partial/fail {#6 2/4, #77 0/1, #78 1/2, #84 0/1, #95 0/1} = identical to baseline.
  30/30 nodes online: fork.154 ×13, fork.138 ×10, fork.143 ×4, fork.126 ×2, fork.145 ×1.
- 03:03 record.py: tunnel hops 98.76 % (02:06) → 98.55 %, dead 0; every Bestvm JP B / Bestvm sg 年付 hop 99–100 %.
  One new 「目标不支持预热」 hop (AWS HK entry → boilhinet:10018, same shared target already unsupported on tunnel 78; not a target host).
- 03:04 temp forwards 655 / 656 deleted via API (DB rows gone, ports 42178 on vmsilo 沪日首发 and 62507 on Mkcloud 深港ixp 1 closed).
- No old warm pool left: all 13 targets run fork.154 (warmPool=2, 冗余). Rollback per host: `sh /root/flvxx-backup-<ts>-pre-fork154/restore-agent.sh`
  (paths in runs/ota-old-agents-1119/work/backups.txt).
