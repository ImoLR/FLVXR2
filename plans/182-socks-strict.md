# 182 — socks-strict: 「屏蔽 SOCKS」不再误杀 SS/VMess + 没规则的隧道不做最优切换

Build only (release = plan 183 by a separate release worker). Base: `maintenance/3.0.27-fork.103-batch-1101`
(19ae5071). Branch `feature/3.0.27-socks-strict`. Run dir `/root/flvx-workers/runs/socks-strict`.

User rule (11-01): no added latency, no log risk, no extra CPU / memory.

## Part A — strict SOCKS detection (agent, go-gost/x/service)

- [x] A1 Strict SOCKS5 greeting match (`05 n m1..mn`, 1 ≤ n ≤ 10, len == 2+n, valid method codes, needs 0x00/0x02)
- [x] A2 Strict SOCKS4/4a match (exact length, NUL-terminated userid / hostname, printable, ≤ 255)
- [x] A3 Pipelined greeting+request only if a real client is shown to do it (captured 20 clients: none pipelines → not accepted)
- [x] A4 Per-kind blocked counters (atomic) + `protocol_blocks` in the existing metric report (only on change / once per session)
- [x] A5 Panel: keep counters in memory (no DB), admin-only node-list field, node card line 「协议拦截：…（自 agent 启动）」
- [x] A6 Unit tests with captured real client greetings (curl socks5/socks5h/socks4/socks4a, Go x/net/proxy, sing-box, xray, PySocks, user/pass)
- [x] A7 False-positive measurement old vs new (≥ 1 M random buffers + ≥ 100 k real SS/SS2022/VMess first packets)
- [x] A8 Benchmarks (ns/op, allocs) old vs new
- [x] A9 Lab netns: 20 k SS connections through a socks=1 entry, old vs new agent; real SOCKS still cut; setup p50/p99, RSS/CPU

## Part B — unused tunnels take no part in 最优 switching (panel)

- [x] B1 Find every user of a tunnel (rules, landing, federation, …) and define "in use" (= ≥ 1 forward row, any status)
- [x] B2 One query per round for in-use tunnel ids (no per-tunnel query)
- [x] B3 Unused: keep ranking/latency display, no reorders, no rule-best orders, no switch logs
- [x] B4 Becoming in use: first deploy uses current best order at once (no 2-round wait)
- [x] B5 Unit tests (in-use detection, no push when unused, immediate best on first rule) + contract tests (create/delete rule transitions)
- [x] B6 Local demo panel: 2 tunnels alternating latency, pushes only on the one with a rule; add a rule → best order at once
- [x] B7 Replay today's switch pattern: pushes/h before vs after; panel CPU/RSS per round

## Gates

- [ ] G1 go build + vet (backend, go-gost, go-gost/x)
- [ ] G2 backend go test ./... SQLite + PostgreSQL contract
- [ ] G3 go-gost / go-gost/x tests of touched packages
- [ ] G4 frontend tsc / build / lint (no new errors)
- [ ] G5 Screenshots 390 + 1440 of the counter line
- [ ] G6 Push, CI green
- [ ] G7 `runs/socks-strict/release-notes.md` + summary.md
