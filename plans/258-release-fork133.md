# 258 — release fork.133 (plan 255 线路监测 + 检测端口) + targeted agent OTA ×3

Base: tag `3.0.27-fork.132` (980984c3, = prod since 11-14 23:34 TW). Branch `maintenance/3.0.27-fork.133`, worktree
`/root/projects/flvxx/flvxx-rel133`. Merged: `feature/3.0.27-line-monitor` @ 5644da36 (code 7fa8b36b; base fork.131) —
no conflicts, no file touched by both 256 and 255. Run dir `/root/flvx-workers/runs/release-fork133`.
User approved 11-14 ~23:10 TW (「没问题，这个你安排一下发布吧」), condition: nothing that can drop business nodes outside
the night window.

Scope: panel upgrade (auto-migrate adds 4 NEW tables line_monitor_chart / _line / _sample / _port; nothing existing
changes; rollback = fork.132 image swap, tables stay inert) + agent OTA ONLY Fachost seednet 黑五 (canary, target),
Tcscloud Seednet (target), AWS HK (source, last). Rest of fleet unchanged.
Time rules: disruptive steps (panel swap, OTA) only 02:00–06:00 TW, never 03:45–04:20 (boil 04:00 IP change), start no
later than 03:35; HiNet socket swap after 04:20. Non-disruptive work any time.

## Tasks
- [x] caches cleaned (5.0 → 5.7 G free), worktree + merge, plan doc
- [x] gates: go-backend build/vet/test (SQLite + PG 18), go-gost/x tests, go-gost build, `npm run build`
- [x] push branch, CI green, tag `3.0.27-fork.133`, Images workflow, release Latest + assets validated
- [x] pre-state snapshot (nodes, rules, chains, landing sync, conns on the 3 OTA hosts, panel RSS)
- [x] (≥02:00) panel backup + rollback dir, prune backups, upgrade /opt/flvx-svc, health + nodes, 4 new tables
- [x] OTA Fachost seednet 黑五 (canary) → Tcscloud Seednet → AWS HK (one at a time, SSH backup each, traffic check)
- [x] chart 「AWS HK → Seednet」 (2 lines, 自动开启检测端口, both ✓)
- [x] (≥04:20) HiNet box: remove hand-made flvx-linemon.socket, run product 开启命令 port 25931 / 24 h / allow-only OFF
- [x] chart 「Seednet → HiNet」 (2 lines → boilhinet2.imgamer.top:25931, 测试连通 ✓ ✓)
- [x] watch 10 min: samples every 30 s on 4 lines, panel logs, RSS/CPU
- [x] verify: nodes / rules / chains / landing sync unchanged, versions, interruption seconds
- [x] ledger 255 → 已发布, memory notes, cleanup worktrees + caches, summary, plan complete

## Results
- Gates (11-15 00:32–00:45 TW): go-backend build/vet ok (0 non-unkeyed vet lines), `go test ./...` SQLite all ok; temp PG 18:
  repo ok (368 s), handler ok (256 s), contract ok (373 s); go-gost build ok; `npm run build` ok. go-gost/x: all ok except
  `warmpool TestAgeRetireAndReplace` once ("expired conn handed out", timing under load) — warmpool is byte-identical to
  fork.132 (no diff), rerun `-count=5` + full package ok → flake. Branch CI 43616427365 green.
- Release: tag `3.0.27-fork.133` (annotated) → 65885f9f; Images 43617401923 success; Latest, not prerelease, same 10 assets
  as fork.132, compose/install pinned, gost-amd64 sha256 + `-V` = `gost 3.0.27-fork.133`. Images pre-pulled on the host.
- Pre (01:55 TW, fork.132): 28/28 online, 51 rules all status 1, chains 6 + 8 active, 7 landing nodes, 5 shared landings
  active / sync error '' + integration ok; panel backend 46 MiB. Diagnose rule 102 4/4, 140 1/1, tunnels 97 / 30 6/6.
  OTA hosts: 黑五 + Tcscloud carry no user rule (tunnels 30/97 exit at 黑五 have 0 rules; Tcscloud native landing 12 has
  0 nodes); AWS HK = entry of tunnel 88 (rules 140/143/144/154) + exit of tunnels 4 / 74 (102/103/587/599/109/346).
- Backup `/opt/flvx-svc/rollback/pre-fork133-20261114T175539Z` (quick/integrity ok, local image tags, rollback.sh);
  prune → kept pre-fork132 + pre-fork133. Upgrade 02:00:06–02:00:25 TW, healthy, oldpanel mount ro ok; 28/28 online at
  02:00:34; nodes / rules / chains / tunnels / landing / shared sync identical; 4 new tables (empty); backend log 0 errors.
- OTA (backup `/root/flvxx-backup-<ts>-pre-fork133/restore-agent.sh` each, work/backups.txt), all `succeeded`, no pending:
  Fachost seednet 黑五 fork.130→133: agent stopped 02:00:55.12, started 02:00:55.54, panel WS 02:00:57.7 TW;
  Tcscloud Seednet fork.130→133: 02:02:59.53 → 02:03:00.18 TW, landing Core PID unchanged;
  AWS HK fork.131→133 (11 ESTAB before, 2 checks 02:04–02:05): 02:05:47.86 → 02:05:48.43 TW; diagnose 102 / 140 / 109 ok.
  flux_agent / mmw-agent PIDs unchanged. Fleet: 3×fork.133, 8×fork.131, 15×fork.130, 2×fork.126 (Nobrand). Only
  agent error lines = pre-existing relay 「unauthorized」 scanner summaries on AWS HK service 80_tls.
- Chart 1 「AWS HK → Seednet」 (02:07 TW): 自动开启检测端口 → 黑五 port 23526 ✓ 20.3 ms, Tcscloud port 27002 ✓ 18.9 ms;
  both listeners owned by flvxx; line_monitor_port rows open/open.
- Chart 1 samples 02:07–04:20 TW: 266 per line, all 正常 except 黑五 3 × 断流 04:01:00–04:02:00 TW = boil's real 04:00 IP
  change of Fachost seednet-vds-1 (= 黑五); Tcscloud line clean. 04:20 re-check: 28/28 online, everything identical.
- HiNet box mmwx-hinet2 (04:21 TW): hand-made `flvx-linemon.socket` + `flvx-linemon@.service` copied to run dir
  `work/hinet-old/`, disabled, removed, daemon-reload; product 开启命令 (port 25931, 24 小时, 仅允许来源 OFF — panel says
  both Seednets have no fixed IP) reviewed (touches only `flvx-linemon-25931*` + /var/lib/flvx-linemon/25931) and run →
  socket + watch timer active. Nothing else on the box touched (xray / flux_agent active).
- Chart 2 「Seednet → HiNet」: first 测试连通 黑五 ✓ 10.7 ms, Tcscloud refused — its dnsmasq cache still had the pre-04:00
  IP (114.38.251.205; new 114.26.129.10, TTL 120); one minute later all resolvers on Tcscloud return the new IP and the
  re-test is ✓ 11 ms / ✓ 7.8 ms. Line 4's first sample (04:21:22) = 端口未开启 from that minute, then clean.
- Watch 04:21–04:32 TW: 4 lines × ~22 samples, 30 s apart (one 22 s re-phase when chart 2 was added), no 断流; one
  1028 ms sample on AWS HK → Tcscloud at 04:27:37 (SYN retransmit, counted 正常). Panel backend 46 MiB / 5–9 % CPU (= before),
  log 0 errors. Final 04:32: 28/28 online, rules / chains / tunnels / landing / shared sync identical to pre (only the 3
  versions changed), diagnose 102 / 140 / 97 ok.
- **Found (product bug, not fixed here):** the agent's TcpPing closes successful probes with SO_LINGER 0 (RST, since
  9b82247b 10-09). systemd `Accept=yes` then fails `getpeername` («Transport endpoint is not connected») and does not
  count the connection: the 25931 socket shows `Accepted: 1` after ~90 probes, and journals ~4 warnings/min (~6k/day) on
  the HiNet box. The one-liner's idle watchdog keys on NAccepted → it will remove the port ~24 h after its first run
  (state `1 1794688276` → ≈ 04:31 TW 11-16) although the lines are probing. Agent-side check ports are fine (Go accept
  returns RST'd conns: local test 50/50). Fix options: graceful close for line-monitor TcpPing to custom targets, or a
  watchdog/counter that sees reset connections (+ quiet the socket's warnings). Workaround until then: re-run 复制开启命令
  before the deadline (or pick 3 天).
- Ledger 255 → 已发布 fork.133.
