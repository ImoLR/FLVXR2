# 135 — Security hotfix (QA-sweep P0 WebSocket leak + auth/disk findings) → 3.0.27-fork.74

Base: `maintenance/3.0.27-fork.73-landing-node-limits` (= prod fork.73, d2fcc058).
Branch: `maintenance/3.0.27-fork.74-security-hotfix`. Panel only, no agent OTA.
Source: plan 133 QA sweep (`/root/flvx-workers/runs/qa-sweep/findings.md`).

Hard constraint (user): no noticeable CPU/RAM/disk/latency increase; refusals never log per event;
nothing may be able to fill the disk.

## Checklist

### Baseline
- [x] Baseline backend failure set on untouched fork.73
- [x] Prod-copy numbers before: DB size, node_metric rows/day, log bytes/hour, WS msg/s, API latency

### P0 WebSocket (`go-backend/internal/ws/server.go`)
- [x] Node command replies only go to the waiter, never broadcast
- [x] Broadcast = explicit whitelist (metric, status, upgrade_progress); unknown types dropped
- [x] Normal users: only nodes in their monitor scope, node-level fields + their own forward metrics
- [x] Admin keeps today's metric payload; panel/agent upgrade progress admin-only
- [x] type=2 only when `login_monitor_link=true`, node-level public fields only; closed when switched off
- [x] Connection limits: anonymous 4/IP + 64 total, authenticated 8/user → 429, no per-event log
- [x] Bounded per-connection send queue + single writer goroutine + write deadline; slow client closed
- [x] Origin check for browser connections (type 0/2); agents (type 1) unaffected
- [x] Periodic re-check (user disabled / switch off / scope change)

### Auth
- [x] F03 disabled/deleted user tokens rejected (HTTP API + WS), cached status lookup with invalidation
- [x] F02 anonymous `config/list` + `config/get` limited to public keys; admin-only keys need admin
- [x] Login rate limit per IP and per username (bounded map, idle eviction, summary log only)
- [x] bcrypt: verify MD5 legacy + upgrade on login; every password write path stores bcrypt
- [x] Subscription (`open_api/sub_store`) password check uses the same verifier + rate limit

### Disk / logging
- [x] node_metric: raw 1 Hz for newest hour, older rows compacted to 30 s buckets, 24 h retention
- [x] Compaction/prune in small batches, every 5 min, paced
- [x] One-time cleanup on upgrade (+ VACUUM only if proven safe on a prod-size copy)
- [x] Remove per-message `[ws.forward]` log line; routine agent request logs suppressed on success
- [x] Audit other high-frequency log/DB writers (path-latency round log hourly; flow-upload failures already 1/min/node; per-metric DB writes are change-only)

### Small fixes
- [x] F01 expired account shows 「已过期」
- [x] F04 GitHub release check cached (1 h, shared)
- [x] F05 dashboard does not request quota history with userId=0
- [x] F06 non-admin rules page skips admin-only APIs
- [x] F07 over-limit rule count shows 「x / y（已超出）」
- [x] F09 Powered-by badge no longer covers the 24 h chart axis

### Tests
- [x] Full backend suite vs baseline (no new failures)
- [x] New unit/contract tests for every item
- [x] Frontend tsc / lint / build
- [x] qa-sweep WS probe against local build: zero leaked fields
- [x] Prod-copy measurements after

### Release / prod
- [ ] Commit, push, tag `3.0.27-fork.74`, CI + images + release assets verified
- [ ] Prod backup `pre-fork74-*`, prune-backups.sh, upgrade, health + 29 nodes
- [ ] Read-only prod probes (anon type=2 refused, user 26 2-min listen, admin pages)
- [ ] Prod numbers after; summary

## Results (lab = neutralised prod copy in an offline netns, 29 fake agents at 1 Hz)

| | fork.73 | fork.74 |
|---|---|---|
| anonymous `type=2` (switch off) | 31.4 msg/s, 23.6 KB/s incl. command replies (fake IPs, online IPs) | refused 403 |
| anonymous `type=2` (switch on) | — | 10 public node fields only |
| normal user 16 | everything (31 msg/s, all users' rules) | 10 msg/s, 4.1 KB/s, own nodes + own rules only |
| admin | 31.1 msg/s incl. replies | 29.0 msg/s metric only, same metric fields |
| admin WS delivery p50/p95 | 1.1 / 1.9 ms | 1.3 / 2.7 ms |
| panel CPU, agents only / +1 admin | 1.31 % / 1.50 % | 1.17 % / 1.46 % |
| panel RSS | 40 MB | 40 MB |
| panel log | 1.35 MB/h | ~0.14 MB/h (0 in steady 1-admin window) |
| forward/list admin p50/p95 | 16.4 / 23.0 ms | 15.2 / 22.7 ms |
| user/package user p50/p95 | 2.7 / 5.1 ms | 2.1 / 3.3 ms |
| login p50 | 0.6 ms (MD5) | 45 ms (bcrypt cost 10), first legacy login 53 ms |

Stuck client (unit test): worst node command round trip during a 32 KB flood with a non-reading
admin session 0.19 s; the stuck session was dropped. Before: each write could block the node read
loop up to 5 s.

node_metric on the prod copy (2.55 M rows, DB 364 MB after copy): compaction leaves 178–185 k rows
(−93 %); online catch-up 88 s in the background (query p95 7.8 ms vs 1.3 ms idle, one-time);
offline (panel stopped) 16 s + VACUUM 0.5 s → 50 MB file. Charts: 15 min / 1 h raw as before,
6 h / 24 h still ~500 points.

Backend suite: same 15 failing lines as untouched fork.73 (pre-existing), no new failures.
