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

- [x] G1 go build + vet (backend, go-gost, go-gost/x)
- [x] G2 backend go test ./... SQLite + PostgreSQL contract
- [x] G3 go-gost / go-gost/x tests of touched packages
- [x] G4 frontend tsc / build / lint (no new errors)
- [x] G5 Screenshots 390 + 1440 of the counter line
- [x] G6 Push, CI green
- [x] G7 `runs/socks-strict/release-notes.md` + summary.md

## Results (build round 1, 2026-11-01)

- 20 real SOCKS clients captured (curl socks5/5h/4/4a/user:pass, Go x/net/proxy, PySocks, Xray, sing-box 5/5u/4/4a,
  Chromium 5/4): all send the greeting alone → strict match, no pipelined form.
- False positives old → new: 120,022 real first reads (sing-box + Xray SS aes-128-gcm / chacha20-ietf-poly1305 /
  2022-blake3-aes-128-gcm / VMess) 244 (≈1/492) → 0; 2,000,000 random first packets 3,721 (≈1/537) → 0.
- Lab netns, real agents, 20 k SS/SS2022/VMess connections per run, prod flags (http=tls=socks=1): old cut 36/46/40/41,
  new 0 ×4; real SOCKS5/5h/4/4a still cut. Setup latency without possible false blocks identical (p50 82.0 vs 81.9 ms);
  agent CPU new ≤ old; RSS same (70–73 MB HWM).
- Detector 0 allocs; isProtocolBlocked 5.3–5.5 ns both; SOCKS check ≈ 2.0–2.2 ns both.
- Demo panel (real panel + 3 agents, alternating exits): fork.103 switched rule-less T2 ×2; new ×0; T1 (rule) ×2 both;
  first rule on T2 → one `first-rule` push inside the create call (16 ms).
- Prod replay (read-only logs since fork.103, 06:25–16:20 TW): 118 最优 pushes, all on tunnels with 0 rules → 0.
- Panel cost: +1 query per 60 s round (6 µs p50 on a prod DB copy); decision step unchanged within noise.
- Gates: build + vet (only pre-existing leikwan testapi vet note), backend go test ./... + contract on SQLite and
  PostgreSQL green, go-gost/x service/socket/handler + go-gost green, frontend tsc + vite build green, eslint node.tsx
  60 problems = fork.103 baseline, CI green, screenshots 390/1440 in runs/socks-strict/screens.
- Release notes: runs/socks-strict/release-notes.md (OTA only nodes 2, 15, 23, 32, 48, 50, 51, 54; no DB change).
