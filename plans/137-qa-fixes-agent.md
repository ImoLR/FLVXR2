# 137 · QA round-2 agent fixes → 3.0.27-fork.77 (F13 log volume, F14 Reality fd check) + 2 canary OTAs

Source: `/root/flvx-workers/runs/qa-sweep/findings.md` 「第二轮」 F13 + F14, evidence in
`/root/flvx-workers/runs/qa-sweep-r2/evidence/`. User approval 2026-10-23 ~15:20 TW (「可以按你推荐去修吧」).
Standing rule: logs must never be able to fill a node's disk; no connection / CPU / memory regressions.

Base: `origin/maintenance/3.0.27-fork.76-qa-fixes` (20a52e3c, = prod fork.76).
Branch: `maintenance/3.0.27-fork.77-agent-log-level`. Worktree `/root/projects/flvxx/flvxx-qa-fixes-agent`.
Run dir: `/root/flvx-workers/runs/qa-fixes-agent`.
Rollout stops after 2 canaries (coordinator change 10-23 ~18:30 TW): plan 138 (fork.78) does the single all-node OTA.
This release also delivers fork.75's agent-side landing GuardVersion 2.

## Findings before the change (prod, read-only, 10-23 ~20:20 TW)

- Entry nodes (local forward handler) log nothing per connection already (forward handler logs at debug):
  node 20 (553 tcp peak) 7 lines/h, node 45 (723 peak) 176 lines/h = reconnect redeploys (plan 138).
- Relay / exit nodes log 2 info lines per connection + error lines for failed ones: node 47 57 conns/h →
  171 lines / 46.5 KB per hour (LeiKwan pipe health probe 1/min: `<>`, `><`, `relay: unknown command`);
  node 42 216 lines/h (info access + `Network Unreachable` / `route(retry=N)` errors + redeploy).
- `[FailFilter]` Printf: 2 lines per connection on entry nodes with chain selectors (lab).
- `service.go` protocol block sniff: `✅ ALLOW TLS/Reality/AnyTLS` Printf on EVERY allowed TLS connection and
  `🚫 BLOCK …` on every blocked one when a node has protocol blocking on.
- nftables `DEBUG …` Printfs per rule on every (re)deploy.
- Unit safety net already present: `LogRateLimitIntervalSec=30s` / `LogRateLimitBurst=200` (≤ 6.7 lines/s).

## Checklist

### Setup
- [x] Read findings F13/F14 + evidence, fork.67 OTA recipe, plan 136 final
- [ ] Baseline go-gost + go-gost/x suites on untouched base (failure set)

### F13 — log volume
- [ ] Agent default log level warn (no level / unknown level); gost.json `log.level`, `GOST_LOGGER_LEVEL`, `-D`/`-DD` still win
- [ ] Remove `[FailFilter]` Printf (selector/filter.go)
- [ ] Protocol-block sniff: no line per allowed connection; blocked ones = first + 10-min summary per kind
- [ ] nftables `DEBUG` Printfs → silent at default (debug logger)
- [ ] Per-connection warn/error lines (sid-tagged) bounded per service + globally, suppressed count reported
- [ ] Other per-connection / per-packet / per-second Printf/info grep: list + fix the ones scaling with traffic
- [ ] Unit tests (level default, explicit info wins, throttle, sniff summary)
- [ ] Prove `log.level=info` in gost.json still prints access lines and survives config saves

### F14 — Reality fd check
- [ ] Lab: 20 held VLESS Reality Vision + SS2022 control, fork.61 agent+Core vs fork.77, fd/conn + dest conns over time
- [ ] Verdict (regression with contained fix → fix; inherent → document numbers)
- [ ] How connection protection counts sockets; effect on node limits

### Tests
- [ ] go-gost + x suites after (no new failures vs baseline); backend suite only if touched
- [ ] Chain lab 1000-connection burst (3-hop): agent log bytes/conn before vs after, RSS/fd/goroutines back to baseline, traffic correct
- [ ] CI green on branch

### Release + rollout
- [ ] Tag `3.0.27-fork.77`, Images green, Latest, same 10 assets, pinned version + sha256
- [ ] Prod panel backup `pre-fork77` + prune (keep 2) + upgrade + health + 29 nodes online
- [ ] Canary 42 (SSH backup): scheme/IP/1 conn, CPU/RSS/fd/goroutines, diagnose identical, journal lines/10 min before/after
- [ ] Busy relay/exit node (SSH backup): same measurements, journal lines/10 min before/after
- [ ] 2 other nodes measured (not upgraded) as control
- [ ] GuardVersion 2 reported by the upgraded landing/Reality node(s)
- [ ] Summary + plan complete
