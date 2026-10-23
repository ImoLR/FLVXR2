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
- [x] Baseline go-gost + go-gost/x suites on untouched base (failure set)

### F13 — log volume
- [x] Agent default log level warn (no level / unknown level); gost.json `log.level`, `GOST_LOGGER_LEVEL`, `-D`/`-DD` still win
- [x] Remove `[FailFilter]` Printf (selector/filter.go)
- [x] Protocol-block sniff: no line per allowed connection; blocked ones = first + 10-min summary per kind
- [x] nftables `DEBUG` Printfs → silent at default (debug logger)
- [x] Per-connection warn/error lines (sid-tagged) bounded per service + globally, suppressed count reported
- [x] Other per-connection / per-packet / per-second Printf/info grep: list + fix the ones scaling with traffic
- [x] Unit tests (level default, explicit info wins, throttle, sniff summary)
- [x] Prove `log.level=info` in gost.json still prints access lines and survives config saves

### F14 — Reality fd check
- [x] Lab: 20 held VLESS Reality Vision + SS2022 control, fork.61 agent+Core vs fork.77, fd/conn + dest conns over time
- [x] Verdict (regression with contained fix → fix; inherent → document numbers)
- [x] How connection protection counts sockets; effect on node limits

### Tests
- [x] go-gost + x suites after (no new failures vs baseline); backend suite only if touched
- [x] Chain lab 1000-connection burst (3-hop): agent log bytes/conn before vs after, RSS/fd/goroutines back to baseline, traffic correct
- [x] CI green on branch

### Release + rollout
- [x] Tag `3.0.27-fork.77`, Images green, Latest, same 10 assets, pinned version + sha256
- [x] Prod panel backup `pre-fork77` + prune (keep 2) + upgrade + health + 29 nodes online
- [x] Canary 42 (SSH backup): scheme/IP/1 conn, CPU/RSS/fd/goroutines, diagnose identical, journal lines/10 min before/after
- [x] Busy relay/exit node (SSH backup): same measurements, journal lines/10 min before/after
- [x] 2 other nodes measured (not upgraded) as control
- [x] GuardVersion 2 reported by the upgraded landing/Reality node(s)
- [x] Summary + plan complete (`runs/qa-fixes-agent/summary.md`)

## Results

### Baseline (untouched fork.76, `runs/qa-fixes-agent/tests/base-*.txt`)
go-gost: all ok. go-gost/x: all ok except 2 landing tests that fail only because the long TMPDIR makes the
unix socket path > 108 bytes (`bind: invalid argument`); with TMPDIR=/root/tq they pass → real baseline = 0 failures.
`TestEgressRealCoreAcceptsRenderedConfig` (only with FLVX_LANDING_TEST_BINARY) fails on base too: needs geosite.dat.

### F13 changes
- `x/logger`: `DefaultLevel = warn` for an empty/unknown level (main's first logger, loader default, named loggers).
  Explicit `log.level` in gost.json, `GOST_LOGGER_LEVEL`, `-D`/`-DD` still win; `log` survives `saveConfig`
  (config.Global() keeps it; `loader_log_test.go`).
- Per-connection warn/error lines (logger has `sid`, or kind=listener): budget 20/min per service (burst 20) and
  120/min per process; next written line carries `suppressed: N`. Only at the default warn level — a logger raised to
  info/debug/trace writes everything. No goroutine/timer (lazy token buckets), map capped at 4096 keys.
- `selector/filter.go`: `[FailFilter]` Printf removed (2 lines per dial on entries).
- `service/service.go`: `✅ ALLOW TLS/Reality/AnyTLS` per allowed connection removed; `🚫 BLOCK …` = first line +
  `×N（近 10 分钟）` summary per kind (`logsafe.EventSummary`, lazy).
- nftables `DEBUG …` per-rule Printfs on every (re)deploy → debug logger (nil-safe).
- `服务名以_tls结尾，跳过观察器启用` per service per deploy removed.
- `ApplyLandingDesired` (panel re-sends every minute, 59 lines/h on node 21) treated as a routine command:
  no `🔔 收到命令` line; failures first/summary/recovery via the existing RoutineLogger.
- Grep result, left as is (do not scale with traffic): `✅ 配置已保存到 gost.json` / `🔔 收到命令` per
  state-changing command (scale with redeploys → plan 138 diff-only redeploy), WS session lines, OTA lines,
  RoutineLogger-throttled probe/report failures, machine protection (RoutineLogger), UDP `bypass:` warn (per packet,
  but sid-tagged → budgeted).

### F13 proof
- Real agent in a netns, relay service, 300 failing connections (`tests/loglevel-e2e.txt`):
  gost.json `log.level=info` → 300 `<>` + 300 `><` + 300 error lines (911 lines, 235 KB);
  no `log` → 0 access lines, 20 error lines (30 lines, 4.9 KB).
- Chain lab, 1000 downloads through 线路机 entry → exit A/B → user node (`chainlab/*-results.json`):
  fork.76 agents: entry +267 KB (267 B/conn), exits +696 KB each (696 B/conn), 2557/2245/2239 lines;
  fork.77 agents: entry +246 B (0.2 B/conn), exits 0 B; all 1000 downloads ok both runs; C1–C8 pass;
  Core sockets/goroutines back to baseline (pipes see F14).

### F14 lab (`runs/qa-fixes-agent/f14/`, accept.py S2+S4 lab, concurrent TLS dest, same pinned Core v1.4.59)
20 held echo streams through a client Core; Core fds by type and netns /proc/net tcp rows (= machine-protection count):

| build | proto | held +3 s | held +9 s | 50-conn burst +0 s | burst +6 s |
|---|---|---|---|---|---|
| fork.61 | SS2022 | sock +40, rows +42 | sock +40 | rows +19 | |
| fork.61 | Reality Vision | sock +40, pipes 0, rows +40, dest 0 | sock +40 | sock +46, rows +70, dest 0 | rows +4 |
| fork.76 | SS2022 | sock +40, rows +41 | sock +40 | rows +0 | |
| fork.76 | Reality Vision | sock +80, pipes +38, rows +101, dest 20 | sock +40, pipes +38, rows +61 | sock +126, pipes +98, rows +197, dest 50 | pipes +98, rows +60 |
| fork.77 | Reality Vision | sock +48, pipes +10, rows +69, dest 4 | sock +40, pipes +10, rows +60 | sock +4, pipes +78, rows +59, dest 1 | pipes +78, rows +57 |

Verdict: **regression from the plan-127/128 guard (fork.69+), contained fix applied.**
- Held connections cost 2 sockets in every build (inbound + outbound) — Reality = SS2022.
- The QA's "4 fds per conn" = 2 sockets + 2 pipe fds. The pipes are Go's pooled splice pipes of the guard's
  dokodemo→freedom dest leg (Xray splices raw TCP→TCP); they stay in the pool until the next GC (≤ ~2 min), are not
  per held connection and are not sockets (machine protection does not count them).
- The ~5 s dest leg came from the shared `direct` outbound's level-0 policy (downlinkOnly 5 s) after REALITY drops the
  guard connection. Fix: guard allow rules use their own freedom outbound `flvxg-direct` on undefined level 9 (Core
  built-in 1 s/1 s), hot-added via API before the rules, removed with the last guard; no Core restart, user inbounds
  untouched. Dokodemo `userLevel` alone does not help (dokodemo uses DispatchLink, no own timer).
- Residual (by design of the guard): every new Reality connection still ends its dest leg with a graceful close →
  1 TIME_WAIT row for 60 s (fork.61: REALITY's direct dest close left none). TIME_WAIT is capped by
  tcp_max_tw_buckets (500 when protection is on).
- Machine protection (整机连接上限, plan 118) counts every row of /proc/net/{tcp,tcp6,udp,udp6} in any state.
  Effect per new Reality connection: fork.76 +3 rows for ~5–9 s + 1 TW row 60 s; fork.77 +3 rows for ≤ ~1–2 s +
  1 TW row 60 s. Held connections: 2 rows, same as SS2022. So held Reality connections never trigger early
  refusals; heavy Reality churn costs ≈ 60 TW rows per new-conn/s (capped 500).
- Per-node 连接数上限 (Core tracker, keyed by inbound tag): the guard leg belongs to the helper inbound `flvxg-*`
  (no limit), not to the user's node inbound — unaffected (code reading of the Core tracker).
- Prod today: no VLESS Reality landing nodes (servers 3 and 5 carry 3 SS2022 nodes; guard_nodes 0) → no current
  prod effect; the fix matters once Reality nodes are created.
- Tests: `TestApplyEgressGuardsOnly` (outbound added before rules, removed with the last guard),
  `TestGuardRealCoreDomainDest` (real Core: allowed SNI reaches dest, others closed), `TestAbuseGuardNetnsE2E`
  (netns, real Core) pass; S4 Reality 2 MiB byte-exact; chain lab C6 now asserts 2 sockets per stream at +3 s.

### Final suites (`runs/qa-fixes-agent/tests/final-*.txt`)
go-gost/x 15 packages ok, 0 failures; go-gost ok; `-race` on logger/logsafe/service/socket/landing/nftables/
selector/config/loader ok; real-Core landing tests ok except `TestEgressRealCoreAcceptsRenderedConfig`
(geosite.dat missing — fails identically on the untouched base). Backend untouched (no panel change).

## Release + rollout (10-23 TW)
- Tag `3.0.27-fork.77` → d75b3839 (branch `maintenance/3.0.27-fork.77-agent-log-level`); CI Build Check 39960799319,
  Build and Push Images 39961042184 green; Release Latest, same 10 assets as fork.76, compose pinned to :fork.77,
  install scripts PINNED_VERSION/REPO, gost sha256 ok, ghcr amd64+arm64 (`runs/qa-fixes-agent/release-validation.json`).
  Panel code identical to fork.76 (`git diff 3.0.27-fork.76 3.0.27-fork.77 -- go-backend vite-frontend` empty).
- Prod panel: rollback `/opt/flvx-svc/rollback/pre-fork77-20261023T133959Z` (validated SQLite, quick/integrity ok,
  fork.76 images tagged `local/flvxx-*:pre-fork77-20261023T133959Z`); prune kept it + `pre-node55-port-split`
  (deleted pre-fork76 + ghcr fork.75 images). Upgrade 21:40:12 → healthy 21:40:30, version.json
  `3.0.27-fork.77-mvl09ftg`, 30/30 nodes online within ~16 s, 26 reconnect redeploys 0 failures.
- Canary 42 「Bestvm HK 年付」 21:43: wss→wss, IP 163.53.18.89 + prefer_ipv4 unchanged, 1 panel WS session,
  diagnose 97 ok before = after (same 3 target-side failures), flux_agent PID 429 untouched, landing_guard 0→2.
- Busy exit 47 「AWS HK」 21:45: wss→wss, sockets 2→2, diagnose 15/15 = 15/15, flux_agent PID 634 untouched,
  landing_guard 0→2.
- SSH backups (both): `/root/flvxx-backup-20261023T132549Z-pre-fork77/restore-agent.sh` (+ pre-fork67 kept).
- No other node OTA'd (plan 138 does the all-node OTA): 26 nodes fork.67, 21 fork.70, 55 fork.76.

| node | CPU % | RSS MB | fds | threads | journal per 10 min |
|---|---|---|---|---|---|
| 42 canary (fork.67→77) | 0.283 → 0.283 | 57.7 → 57.8 | 19 → 19 | 8 → 7 | 12 lines / 2382 B → 7 / 642 B |
| 47 busy exit (fork.67→77) | 0.433 → 0.400 | 66.9 → 66.6 | 36 → 38 | 9 → 8 | 21 / 5612 B → 10 / 1830 B |
| 21 control (fork.70) | 0.250 → 0.250 | 59.3 → 58.2 | 12 → 12 | 9 → 9 | 10 / 550 → 10 / 550 |
| 52 control (fork.67) | 0.167 → 0.150 | 60.4 → 61.3 | 7 → 6 | 9 → 9 | 2 / 182 → 0 / 0 |

Before = 21:2x TW (fork.67), after = 12 min after the OTA (window fully fork.77). 47's 60-min before: 172 lines /
45 KB per hour; after ≈ 60 lines / 11 KB per hour — what is left is the LeiKwan pipe probe (`relay: unknown command`,
1/min, fixed rate). 42's remainder = TcpPing first-failure/recovery pairs. Goroutines: not measurable on prod agents
(no debug endpoint); lab Core goroutines back to baseline.

## Open (handed to plan 138 / later)
- Panel backend CPU 9.2 % → 7.5 % (60 s averages, 25 min after the restart) vs 3.9 % measured after the fork.76
  upgrade; panel code identical → load-side (evening, reconnecting nodes); check with plan 138.
- Residual per new Reality connection: 1 TIME_WAIT row for 60 s (guard design); pooled splice pipes until GC.
- Redeploy lines (`收到命令` / `配置已保存`) per reconnect → plan 138 diff-only redeploy.
- GuardVersion 2 on 42, 47, 55 only; node 21 (fork.70) reports 1 until the plan-138 OTA.
- mmwx shared guard (Custom v1.4.75) already uses the same dedicated level-1 outbound — consistent.
