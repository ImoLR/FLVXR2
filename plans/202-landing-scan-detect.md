# 202 — 落地端口扫描检测（内置「禁止端口扫描」）— STOPPED at the research gate

Base: tag `3.0.27-fork.110` · branch `feature/3.0.27-landing-scan-detect` · build only.
Brief: `/root/flvx-workers/runs/landing-scan-detect/brief.md` (user approved the design 2026-11-03, revised ~19:40 TW:
users see a locked 内置 row, no observe week, over a threshold → node disabled at once, admin re-enables).
Plan number: 201 was already taken on origin (`feature/3.0.27-probe-tcp-sort`, 201-probe-tcp-sort-table), so this is 202.

The brief has a hard gate in item 2: *verify the pinned Core can stream per-connection routing decisions
(e.g. `SubscribeRoutingStats`); if it cannot do so cheaply, STOP after research, change nothing, report options
with costs.* It cannot, so no code was changed. This document records the research and the options.

## Research result

Pinned Core = Custom `v1.4.77` = `xray-core-vision-limiter` commit `2bf03c9a` (go-gost/x/landing/types.go).

1. **`RoutingService.SubscribeRoutingStats` is permanently off.** `app/router/command/command.go:145` registers the
   gRPC service as `NewRoutingServer(router, nil)`; with a nil channel the RPC returns
   `"Routing statistics not enabled."` (`command.go:107-110`). No config switch changes this (the line is upstream
   code from 2020, unchanged by the fork). Even when the channel exists, the only publisher in the whole tree is
   `TestRoute` with `PublishResult` (`command.go:100-103`) — real traffic never publishes a routing decision.
   `grep routingStats` finds nothing outside that file.
2. **The fork's connection control socket (`/v1/snapshot`, the one the agent already uses) has no destinations.**
   `common/mmwxcustom/connection/tracker.go` sees every outbound TCP dial with identity + destination in
   `Manager.acquire(ctx, destination)` (`tracker.go:1075`), but it only keeps counters (current, new rate,
   TCP states, online source IPs). Nothing per destination is exported.
3. **Access log** (`log.access`) is the only stock Core output that carries `inbound tag + destination` per
   connection (`from … accepted tcp:IP:port [flvxl-N >> outbound] email: …`). It is set to `none` today and is read
   only at Core start (`baseConfig`, types.go). Its writer never blocks the data path
   (`common/log/logger.go`: 128-entry channel, drops when full) but `CreateFileLogWriter` opens the path at start
   with a blocking `open(O_WRONLY)`.
4. Blocking a node is already cheap: the agent's `/v1/config` `blocked_identities` closes the node's sockets
   (incl. pre-auth) and the inbound is withheld — same path as a disabled node (control.go `applyLimits`).
   Detection input is the only missing piece.

Prod landing inventory (read-only DB query): native = Breadcloud JP (node 21, 1 landing node) and NexKR (node 55,
1 landing node); shared mmwx = Breadcloud US 年付 (0), Boil Hinet 马年 (2), Dmit T1 (0), PQS TW (1).

## Options (with costs)

Synthetic micro-benchmark (Go 1.24, this dev box, GOMAXPROCS=2; `runs/landing-scan-detect/bench/bench_test.go`):

| | A. Counter inside the Core (mmwx Core change) | B. Access log → FIFO → agent (FLVX only) | C. Per-node SO_MARK + NFLOG (FLVX only) |
|---|---|---|---|
| Where | `Manager.acquire` already holds the identity + dst + mutex for every TCP dial; add 3 bounded sets per identity, expose hits on a new `/v1/scan` (+ `/v1/features` flag), optional instant self-block | Core `log.access` = FIFO in the landing dir; agent reads + parses lines, counts per inbound tag | each landing node gets its own copy of every outbound with a node mark; nft `ct state new` + mark → NFLOG group → agent |
| CPU per new conn | **~53 ns** (8 ns once a node is already flagged) | **~1.0 µs** + 427 B garbage (line format + 1 pipe write + parse) ≈ 0.2–0.4 % of one core at 2000 new conns/s | ~1 netlink msg per SYN, cheap |
| Memory | bounded: 100k-IP scan with caps of 4096 keys = 2.4 MiB per node window (caps of 1024 suffice for thresholds ≤ 1000 → < 1 MiB) | pipe 64 KiB + 128-entry channel; agent counters same as A | same as A |
| Releases | mmwx Core + Custom release (mmwx session, per release-per-project), FLVX pin bump, Core restart on landing nodes 21/55 at OTA | FLVX agent OTA + one Core restart on 21/55 to switch the access log on | FLVX agent OTA; routing plan rework, no Core restart |
| Risks / limits | none new on the data path; also usable later by mmwx-shared servers through the same control socket (Helper release) | Core start **blocks** opening the FIFO when the agent is not reading (reboot with a broken agent, Core crash-restart while agent down) → native landing down until the agent returns, needs an extra holder (systemd `.socket ListenFIFO=` or agent-held O_RDWR + unit ordering); per-connection log lines (in memory only) conflict with the brief's "no per-connection logging"; drops under bursts (only misses, never false hits); does not help shared servers | outbounds × nodes, every egress feature (chain proxy, WARP, profiles) touched; scans through a chain-proxy outbound are invisible (the dial target is the proxy) |
| Verdict | **recommended** | workable fallback if no mmwx release is wanted | not recommended |

Rejected without cost table: polling `/proc/net/tcp` or conntrack (misses scans to closed ports, which are reset
within milliseconds; no node attribution).

## Checklist
- [x] Worktree `flvxx-landing-scan-detect` on `3.0.27-fork.110`, plan number checked on origin (201 taken → 202)
- [x] Read agent landing runtime (baseConfig, control socket, blocked identities, Core unit)
- [x] Verify pinned Core RoutingService / SubscribeRoutingStats → not available
- [x] Check the fork control socket and access log as alternatives
- [x] Prod landing inventory (read-only)
- [x] Micro-benchmark options A and B (CPU per conn, bounded memory at 100k IPs)
- [x] STOP report with options (`runs/landing-scan-detect/summary.md`)
- [ ] Decision by user/coordinator: A (mmwx Core change first) or B (FLVX-only access-log FIFO)
- [ ] Build (agent + panel + UI + lab + screens) — not started, blocked on the decision
