# 264 — 预热池 冗余（adaptive size floor 6 / ceiling 16, parallel refill, idle sleep 6 h）

Branch `feature/3.0.27-warm-redundant` (from tag `3.0.27-fork.135`, worktree `/root/projects/flvxx/flvxx-warm-redundant`).
Task `warm-redundant` (coordinator design, 11-16 ~17:00 TW). Release = panel now (`3.0.27-fork.136`), agent OTA at night
(separate worker `warm-redundant-ota`), stats after the 11-17 evening peak (`warm-redundant-stats`).

## Why
Prod 1 h window 11-16 15:49–16:49 TW: supported hops 3605 streams, 470 misses (13 %). Worst: vmsilo 沪日首发 → Bestvm JP B
(tunnel 102) 87 %, **Bestvm JP B → Bestvm US 77 % (cold 202 ms)**, tunnel 92 entry 83 %, port-forward rule 「99」 67 %.
Cause: 2 conns per key (panel `warmPoolSize`, agent `MaxSize` 4) while browsers open 4–6 streams at once, and refill is
one dial in flight per key → after a burst the pool refills serially (~2 RTT each, ~400 ms on JP→US).

## Design (decided by the coordinator)
1. Adaptive size per key, agent-local on the existing 5 s tick: floor 6, ceiling 16 (panel-pushed);
   want = clamp(max(floor, ⌈1.5 × peak5s⌉ + 2), floor, ceiling), peak5s = max streams per 5 s tick over the last 1 h
   (12 × 5-min maxima ring); every non-wake miss → want +2 at once (≤ ceiling); −1 per 15 min without a miss, never
   below the formula / floor.
2. Parallel refill: up to 4 dials in flight per key; a failing target keeps one dial (failure waves count once, rest as
   before); agent `DefaultCap` 64 → 192, `MaxSize` 4 → 16.
3. Idle sleep 30 min → 6 h, pushed by the panel (`idleSleepS`, default 21600), agent default 6 h when absent.
4. Payload backward compatible: `chains` / `forwards` = floor; new top-level `ceil`, `idleSleepS` only for agents with
   `warmPool=2` (CapVersion 2). Old agents (warmPool=1) get the floor clamped to their max 4 → 2 → 4 with the panel-only
   release.
5. Report: `wakeN` (misses while a woken / new key has no conn yet, subset of missN), `want`, `peak5s` per key; panel
   keeps them in the 1 h buckets and adds `wakeN` / `burstMissN` / `want` / `peak5s` to the warmPoolState hop view
   (JSON only, no UI change).
6. No UI change.
7. Conn-limited lines: check nodes with vendor conn limits; per-node pooled-conn budget if any.

## Tasks
- [x] Agent: adaptive want (formula, miss growth, slow shrink, floor/ceiling), parallel refill (4, failure waves),
      cap 192 / max 16, idle sleep from payload (default 6 h), CapVersion 2, report wakeN / want / peak5s
- [x] Agent unit tests (fake clock) + go vet + `go test ./...` in go-gost/x (+ go-gost)
- [x] Panel: constants in warm_pool.go (floor 6, ceil 16, idle sleep 21600 s), version-aware payload, ws version getter,
      report fields, buckets, hop view fields; per-node budget if a vendor conn limit is known
- [x] Panel unit/contract tests + `go test ./...` in go-backend (SQLite + PG) + go vet; `npm run build` untouched
- [x] Conn-limited lines check (DB + memory) — findings in this doc
- [x] Lab: 2-hop path, bursts 6 / 12 / 20, hit rate before (fork.135 agent) vs after, refill time; RSS / fd / goroutines
      per pooled conn and 16 × 4 keys; handshakes/min
- [x] Commit + push, CI green
- [x] Release: `maintenance/3.0.27-fork.136-warm-redundant`, tag `3.0.27-fork.136`, CI + Images + Latest assets
- [x] Prod panel upgrade (backup + rollback script + prune), verify agents / forwards / warmPoolState / old agents 4 per key
- [x] Ledger row 264, OTA target list for the night worker, record.py fields, memory notes
- [x] Agent OTA (night worker `warm-redundant-ota`, 02:00–03:30 TW 11-17) — done 02:25 TW, 11 hosts → fork.138
- [ ] Stats after OTA + evening peak (worker `warm-redundant-stats`)

## Notes

### Conn-limited lines (checked 11-16, prod DB read-only + memory)
- No node carries a known vendor connection limit: node `remark` / `admin_notice` mention none; memory has none for
  Leikwanhost 沪台IPLC (its 6 port-forward rules to boilhinet / boilhinet2 / pqstw targets sleep most of the day).
- LeiKwan pipe tunnels (1 on prod, tunnel 80) are already excluded from pools (`ListWarmChainOwners`).
- Line-monitor ports (Fachost seednet 黑五 :23526, Tcscloud Seednet :27002) are monitor listeners, never pool targets.
- Our own limit that pooled conns DO count against: 整机连接上限 (machineprotect, every /proc/net row; 1000 on every
  node, 火山广州 1500). The panel now sends a per-node pooled-conn budget `cap` = 1/5 of that limit when it is below the
  agent default 192 (no prod node today: 1000 / 5 = 200 ≥ 192). Busiest 7-day tcp_conns: vmsilo 沪日首发 471, Bestvm JP B
  435 — worst-case growth (≈ 70 ESTAB + ≈ 40 TIME_WAIT on vmsilo 沪日首发; ≈ 80 + 20 on Bestvm JP B incl. inbound pools
  of 3 entry keys) stays far below 1000.
- Pooled conns stay out of user conn limits / 入站连接数 / online-IP / scan detection (unchanged: outbound on the dialing
  node, pre-request on the next hop; the dialing agent still subtracts its idle pooled conns from `tcp_conns`). New guard
  test `TestWarmRelayServiceHasNoConnLimit`: tunnel relay services carry no limiter keys and the relay `readTimeout`
  stays 130s (not touched — changing relay metadata would re-create exit services and drop live conns).

### Tests (11-16)
- go-gost/x `go test ./...` all ok (+ `-race ./warmpool/`), go vet ok; go-gost root ok.
- go-backend `go vet ./...` ok; `go test ./...` SQLite 28/28 packages ok; PG 18 (repo + handler + contract) ok.
- New: agent `warmpool_redundant_test.go` (formula, 4 dials in flight, burst-peak growth, miss growth to ceiling, slow
  shrink, shrink paused by misses, panel ceiling/floor, wake vs burst misses, new-key misses = wake, failure wave counts
  once + rest + single probe, cap 192 / panel cap / min 8, idle sleep 6 h + panel value + clamp, old-panel payload,
  report JSON, 0 allocs for the accounting); updated sleep tests (30 min → 6 h), backoff test (wave of 2 + 1 retry).
  Panel `warm_pool_redundant_test.go` (payload by agent version incl. upgrade + machine budget, SQLite + PG; budget table;
  hop view wake/burst/want/peak5s incl. old-agent hop; relay no-limit guard); ws `TestWarmPoolVersionFromHandshake`.

### Lab (11-16, runs/warm-redundant/lab, netns, no internet)
Path client → E (entry) → M (middle) → X (exit) → target; one-way delay E↔M 25 ms, M↔X 45 ms (≈ JP→US-like middle hop);
cold dial E→M ≈ 100 ms, M→X ≈ 180 ms (TCP + TLS). 4 tunnels E→M→X (4 keys per hop), bursts on one of them.
A = fork.135 panel + fork.135 agents (2 per key), B = fork.136 panel + fork.135 agents (= prod after the panel-only release,
4 per key), C = fork.136 panel + plan-264 agent. spaced = 6 bursts 4 s apart; rapid = 4 bursts, next starts 0.25 s after
the previous finished; overlap = 4 bursts started 0.3 s apart without waiting (refill speed). Hit % per hop from the
agents' own counters (panel hop view deltas); TTFB both hops warm ≈ 297 ms, cold ≈ 580 ms.

| burst | A spaced / overlap | B spaced / overlap | C spaced / overlap |
|-------|--------------------|--------------------|--------------------|
| 6     | 33 % / 29–33 %     | 67 % / 38–50 %     | **100 % / 100 %**  |
| 12    | 17 % / 15–17 %     | 33 % / 19–25 %     | **100 % / 67–100 %** |
| 20    | 10 % / 9–10 %      | 20 % / 11–15 %     | **80 % / 45–60 %** (ceiling 16 of 20 by design) |

C after the first 6-burst: want 6 → 11 (peak) → 16 (two bursts in one 5 s tick); refill measured on the agent (lab-only
`/warm` endpoint, warmlab build tag): 6 conns E→M 202 ms / M→X 389 ms; 16 conns 302–408 ms / 570–746 ms; never more than
4 dials in flight. Serial refill (fork.135) of the same: 2 × 100 / 2 × 180 ms for 2 conns, 16 conns would be 1.6 s / 2.9 s.
0 stream errors in all 27 series.

Resources (E agent, plan-264 build, 4 chains + 2 port rules): pools off → 64 pooled E→M conns: +71 fd, +71 goroutines
(1 fd + 1 goroutine per idle conn), RSS +2.4 MB (≈ 35 KB per conn incl. TLS state); M (inbound 64 + outbound 64):
RSS +3.8 MB (≈ 30 KB per socket). 5 min with 64 idle pooled conns and no streams: 215 handshakes (43 / min ≈ 0.67 per
conn per min, age 90–110 s), 601 KB both ways (2.8 KB per handshake with the lab cert). Prod pooled conn (vmsilo 沪日首发
→ Bestvm JP B, `ss -i`): 1517 B sent + 1910 B received per TLS handshake ≈ 5 KB with headers → ≈ 4.3 MB / day per pooled
TLS conn; plain-TCP rule pools ≈ 0.4 KB per conn lifetime ≈ 0.35 MB / day.

### Release (11-16)
- Tag `3.0.27-fork.136` → 4678141f on `maintenance/3.0.27-fork.136-warm-redundant` (= feature head, fork.135 + plan 264).
  CI 43843768465 / 43843750156 green, Images 43843772804 green, Latest = fork.136, 10 assets, validation ALL OK
  (compose pinned, install scripts PINNED_VERSION, gost-amd64 sha256, `gost -V` = 3.0.27-fork.136).
- Prod panel 18:08:55–18:09:05 TW (panel only, no DB change). Backup / rollback
  `/opt/flvx-svc/rollback/pre-fork136-20261116T100845Z/rollback.sh` (fork.135 image swap); prune kept pre-fork135 + pre-fork136.
- After: 29/29 nodes online, versions unchanged; 51/51 rules status unchanged; every agent reconnected with the diff
  redeploy (all unchanged except Mkcloud 深港ixp 1 `tunnels updated=2` — same as the 11-02 prod upgrade log, the
  memory-only 最优 order re-push, not from this change); old agents now hold 4 idle per key (vmsilo 沪日首发 → Bestvm JP B
  t92 2 → 4, t102 → 4, Bestvm JP B → Bestvm US → 4); warmPoolState on 8 tunnels with `wakeN` / `burstMissN`; backend
  healthy, 0 restarts, RSS 72.6 → 38 MiB; no warm-pool errors in the log.
- Agent OTA: night worker `warm-redundant-ota` 02:00–03:30 TW (targets runs/warm-redundant/ota-targets.md).

### Agent OTA 11-17 02:02–02:25 TW (worker `warm-redundant-ota`)
- Version installed = **3.0.27-fork.138** (panel hands out only its own version; `go-gost/` diff fork.136..fork.138 = 0 lines,
  so this is the plan-264 agent). Panel `node/upgrade` per host, one at a time, SSH backup first
  (`/root/flvxx-backup-2026111?T18????Z-pre-fork138/restore-agent.sh` on each host; layout-aware: `/etc/flvxx` or `/etc/flvx_agent`).
- Order + back-online time: Dmit Echo (canary 02:03, 6 s, 5 min watch) → vmsilo 沪日首发 02:07 (8 s) → Bestvm JP B 02:09 (6 s) →
  vmsilo 沪美 02:12 (10 s) → Datawave HK 2 02:14 (8 s) → Leikwanhost 沪台IPLC 02:15 (7 s) → Mkcloud 沪日ixp 440 02:17 →
  Dmit Nathan 02:19 (7 s) → Bestvm HK 年付 02:21 (7 s) → AWS HK 02:22 (7 s) → Mkcloud 深港ixp 1 02:24 (7 s). 0 failures, 0 restores.
- After: 30/30 nodes online; 51/51 rules, chains, tunnels, landing nodes, shared servers unchanged (state.py cmp); every
  reported hop from an OTA'd node carries `want` (6–13) / `peak5s`; no `capped`; awake keys hold 6 idle (was 4);
  journals: only the usual reconnect re-push burst, then quiet; error lines = pre-existing kinds (client timeouts, relay unauthorized scans).
- vmsilo 沪日首发: agent RSS 148 → 66 MB (restart), 53 ESTAB (t92 → Bestvm JP B 14, t102 6, rules 6–7 each);
  Bestvm JP B: RSS 61 → 58 MB, → Bestvm US 6 + → Datawave HK 2 6.
- Hops whose target closes idle conns (`unsupported`) and dead targets (`rest`) are re-learned by the new agent after its restart
  (same as any agent restart). Hit-rate comparison needs daytime traffic → stats worker after the 11-17 evening peak.

### Prod estimate after the night OTA (resource rule)
- Pool keys today: 14 chain keys (10 entry + 4 middle) + 27 port-forward rule keys on 11 hosts. With 6 h sleep most keys
  used during the day stay awake: ≈ 30–35 awake × floor 6 + a few bursting keys (沪日首发 → JP / → US both hops, rule
  「99」) up to 16 → **≈ 200–250 idle pooled conns fleet-wide in the day** (fork.135: ≈ 24–40), fewer at night.
- Busiest: **vmsilo 沪日首发** ≤ 72 (2 chain keys + rule 99 at 16, 4 rules at 6; typical 40–50) → +72 fd / goroutines,
  ≈ +2.5 MB RSS, TLS handshakes ≈ 32 × 4.3 MB ≈ 140 MB/day + TCP rule pools ≈ 15 MB/day. **Bestvm JP B** ≈ 22 outbound +
  ≈ 38 inbound pooled sockets → ≈ 260 MB/day handshake bytes both directions. 整机连接数 stays < 600 of 1000 at the 7-day peak.
- Fleet handshake traffic ≈ 120 TLS pooled conns × 4.3 MB ≈ 0.5 GB/day. No new timers / goroutines per key / logs per stream.
- Longer pooled-conn age would cut that: age is capped at 90–110 s by the relay `readTimeout: 130s` on middles/exits.
  Raising it (e.g. 600 s → age ≈ 540 s, ≈ 5× fewer handshakes) changes relay handler metadata = re-creates every relay
  service (live conns drop) → only worth it bundled with another planned relay change, in an OTA window. Not done here.

### Idle sleep 6 h → 2 h (plan 271, user 11-17 ~15:00 TW)
- **Changed 11-17 16:02:40 TW** (panel fork.142 swap 16:02:32–16:02:40 TW): panel `warmPoolIdleSleepS` 21600 → 7200.
  The panel keeps the last pushed SetWarmPools in memory only, so the restart re-pushed the state to every warmPool=2
  agent (the 11 fork.138 hosts + the 4 利群入口 on fork.140) on reconnect (30/30 back within ~17 s, redeploys updated=0
  failed=0, no `SetWarmPools failed` lines). The agent applies `idleSleepS` live (5 s tick); no rule service or chain is
  re-created. Keys idle for > 2 h went to sleep right after 16:02:40 TW.
- warmPool=1 agents (fork.130/131/134, Nobrand fork.126 has no pool) never get `idleSleepS` and keep their own 30 min.
- Agent default `IdleSleepAfter` is 2 h from fork.142 on (no OTA for it; same value as the panel push).
- **Stats:** wake misses (`wakeN`) after 16:02 TW will be higher by design (a key idle 2–6 h now sleeps and dials its
  first stream cold). Count them apart from burst misses; compare hours before / after 16:02 TW.
