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
- [ ] Panel unit/contract tests + `go test ./...` in go-backend (SQLite + PG) + go vet; `npm run build` untouched
- [ ] Conn-limited lines check (DB + memory) — findings in this doc
- [ ] Lab: 2-hop path, bursts 6 / 12 / 20, hit rate before (fork.135 agent) vs after, refill time; RSS / fd / goroutines
      per pooled conn and 16 × 4 keys; handshakes/min
- [ ] Commit + push, CI green
- [ ] Release: `maintenance/3.0.27-fork.136-warm-redundant`, tag `3.0.27-fork.136`, CI + Images + Latest assets
- [ ] Prod panel upgrade (backup + rollback script + prune), verify agents / forwards / warmPoolState / old agents 4 per key
- [ ] Ledger row 264, OTA target list for the night worker, record.py fields, memory notes
- [ ] Stats after OTA + evening peak (worker `warm-redundant-stats`)

## Notes
