# 259 — line monitor: custom-target check port never sees probes (RST close vs systemd Accept=yes)

Base: tag `3.0.27-fork.133` (65885f9f, = prod since 11-15 02:00 TW). Branch `fix/3.0.27-linemon-rst-close`, worktree
`/root/projects/flvxx/flvxx-linemon-fix`. Run dir `/root/flvx-workers/runs/linemon-rst-fix`. BUILD ONLY (no release,
no OTA, no prod change) — user approved the fix 11-15 ~15:35 TW and wants to see the result first.

## Problem
The agent's TcpPing closes every successful attempt with SO_LINGER 0 (RST, plan 082 / fork.34: no TIME_WAIT pile-up
from the 60 s path-latency prober). Against the 「复制开启命令」 one-liner (systemd `flvx-linemon-<P>.socket`,
`Accept=yes`, `MaxConnectionsPerSource=4`) the RST reaches the target before systemd takes the queued connection off
the accept queue: systemd's per-source accounting (MaxConnectionsPerSource) calls getpeername() on the reset socket →
`getpeername failed: Transport endpoint is not connected` (error level, one per probe), the connection is dropped and
`NAccepted` never grows. The watchdog then sees no
activity and deletes the port after the idle window (HiNet 25931 on mmwx-hinet2; stopgap 3-day window until
≈11-18 15:45 TW). Agent-side check ports are not affected (Go accept() returns reset sockets and counts them).

## Design
- Panel → agent: TcpPing gets an optional `graceful: true` — sent ONLY for line-monitor probes / 测试连通 of custom
  targets (target node id 0). Old agents ignore the field (unchanged RST, = today); old panels never send it.
- Agent, graceful close: after a successful connect, wait (bounded, ≤ 1 s read deadline, small byte cap) for the
  peer to close first. EOF → close with RST (the peer has accepted and closed already; no TIME_WAIT on either side,
  same footprint as the agent's own check ports). Deadline / cap hit → plain Close() (FIN, never RST, so a slow
  systemd still counts it). Latency = connect time as before; no extra goroutine; non-graceful path unchanged.
- One-liner watchdog fallback: when NAccepted did not move, it also counts systemd's `getpeername` lines for its
  own socket unit since the last activity (journalctl -u, read-only) as activity — so an old agent / any RST-closing
  prober no longer gets the port deleted. Re-running the command is optional (only for that fallback).

## Tasks
- [x] worktree + branch from fork.133, plan doc (259 = next free on all refs)
- [x] lab: systemd bookworm + trixie targets, real panel + fork.133 agent → reproduce (NAccepted stuck, getpeername)
- [x] agent: TcpPingRequest.Graceful + bounded graceful close path, unit tests (RST path unchanged, graceful FIN,
      peer-never-closes bound, banner drain)
- [x] panel: send `graceful` for custom targets (line probes + 测试连通), tests
- [x] one-liner watchdog: getpeername fallback, generator test
- [x] gates: go-gost build, go-gost/x tests, go-backend build/vet/test (SQLite + temp PG 18), compare vs base
- [x] lab after: new panel + new agent (NAccepted +1/probe, 0 getpeername over ≥ 20 probes, watchdog keeps / deletes),
      compat old agent + new panel, new agent + old panel, old one-liner + new agent, fallback with old agent
- [x] resources: TIME_WAIT / fds / goroutines on the source, probe duration
- [x] push branch, CI green
- [x] ledger row 259 (待发布), final.md + summary.md, cleanup (containers with -v, caches)

## Results
Code commit `e4bee9c0` (CI Build Check 43692270457 green). Files: `go-gost/x/socket/tcp_ping_close.go` (new close
path), `websocket_reporter.go` (TcpPingRequest.graceful → tcpPingHostClose), `go-backend/internal/linemon/engine.go`
(Target.Graceful), `handler/line_monitor.go` + `line_monitor_port.go` (graceful for custom lines + 测试连通),
`linemon/checkport.go` (watchdog fallback). No DB change, no frontend change, no new listener / goroutine.

Lab (`runs/linemon-rst-fix/lab`, never committed): Debian 12 (systemd 252) + Debian 13 (systemd 257) systemd
containers with the real one-liner (test window W=120 s, tick 20 s), real panel + agent in a third container.

| Case | NAccepted after 25 probes | getpeername lines | TIME_WAIT src / target | watchdog |
|------|---------------------------|-------------------|------------------------|----------|
| before: fork.133 panel + agent | 0 / 0 | 25 / 25 | 0 / 0 | port removed after 150 s although probed every 5 s |
| A: 259 panel + agent, new one-liner | 25 / 25 (+1 per 30 s from the chart, 10 ok samples/line in 300 s) | 0 / 0 | 0 / 0 | kept 300 s while measured; removed 123–124 s after pause |
| B: fork.133-installed one-liner, 259 panel + agent (no re-run) | 25 / 25 | 0 / 0 | 0 / 0 | — |
| C: 259 panel + fork.133 agent, new one-liner | 0 / 0 (40 probes, RST as today) | 40 / 40 | 0 / 0 | fallback keeps it 164 s (> W); removed 144 s after probes stop |
| D: fork.133 panel + 259 agent | 0 / 0 (RST as before) | 25 / 25 | 0 / 0 | — |

(bookworm / trixie.) 测试连通 API call: graceful ≈ 64–74 ms vs RST ≈ 38–39 ms in the lab (systemd spawns the
sandboxed /bin/true before it closes; measured latency = connect time, unchanged). Agent before/after 25 graceful
probes: RSS 43.6 → 46.7 MB, threads 12 → 12, fds 11 → 11.

Gates: go-gost build + tests ok; go-gost/x all ok (landing control-socket tests need a short TMPDIR — unix socket
path > 108 bytes with /root/flvx-tmp/lm259, ok with /root/t259; env only); go-backend build ok, vet 0 non-unkeyed,
`go test ./...` 0 non-ok lines; PG 18.6: repo / handler / linemon / contract ok, 0 skips.

Release (later, needs user approval): panel upgrade + OTA of the custom-line sources only — Fachost seednet 黑五 and
Tcscloud Seednet (AWS HK only probes node targets: not needed). Both sides are needed (panel sends the flag, agent
honours it); either alone = today's behaviour. HiNet 25931 starts counting without re-running the command;
re-running is optional (gives the fallback and resets the 3-day stopgap window).
