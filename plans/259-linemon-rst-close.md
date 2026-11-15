# 259 — line monitor: custom-target check port never sees probes (RST close vs systemd Accept=yes)

Base: tag `3.0.27-fork.133` (65885f9f, = prod since 11-15 02:00 TW). Branch `fix/3.0.27-linemon-rst-close`, worktree
`/root/projects/flvxx/flvxx-linemon-fix`. Run dir `/root/flvx-workers/runs/linemon-rst-fix`. BUILD ONLY (no release,
no OTA, no prod change) — user approved the fix 11-15 ~15:35 TW and wants to see the result first.

## Problem
The agent's TcpPing closes every successful attempt with SO_LINGER 0 (RST, plan 082 / fork.34: no TIME_WAIT pile-up
from the 60 s path-latency prober). Against the 「复制开启命令」 one-liner (systemd `flvx-linemon-<P>.socket`,
`Accept=yes`, `MaxConnectionsPerSource=4`) the RST reaches the target before systemd takes the queued connection off
the accept queue: systemd's per-source accounting calls getpeername() on a reset socket → `getpeername failed`
(error level, 2 lines per probe), the connection is dropped and `NAccepted` never grows. The watchdog then sees no
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
- [ ] gates: go-gost build, go-gost/x tests, go-backend build/vet/test (SQLite + temp PG 18), compare vs base
- [ ] lab after: new panel + new agent (NAccepted +1/probe, 0 getpeername over ≥ 20 probes, watchdog keeps / deletes),
      compat old agent + new panel, new agent + old panel, old one-liner + new agent, fallback with old agent
- [ ] resources: TIME_WAIT / fds / goroutines on the source, probe duration
- [ ] push branch, CI green
- [ ] ledger row 259 (待发布), final.md + summary.md, cleanup (containers with -v, caches)

## Results
