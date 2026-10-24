# 147 — open items of 139 / 141 / 129 (built, RELEASED as fork.89 — complete)

Source: user 2026-10-24 ~14:20 TW 「第四点你看着就修吧，把它能修复的就全部修复吧」 + plan 141 setting is FLVX-native
only (「妙妙屋那边…不要动」); ~15:50 TW 「其他的问题能修就赶紧修」 (items 7, 8). Brief:
`/root/flvx-workers/runs/open-items-fix/brief.md`. Base: `maintenance/3.0.27-fork.88-landing-onboarding-hints`
(fork.88 = prod, b3a87297). Branch `feature/3.0.27-open-items-fix`. No tag, no release, no prod write, no OTA, no mmwx change.

## Checklist
- [x] 1a shared servers: conn-mode selectors replaced by fixed 「入站+出站」 label; hint dropped; backend refuses/ignores
      inbound for shared; shared push payload byte-identical to fork.85/88 (unit test)
- [x] 1b prod copy check for shared 仅入站 rows → migration only if any
- [x] 1c contract `landing-conn-mode-v1.md` marked 暂缓
- [x] 1d H5 「服务器总连接数（0 不限）」 row alignment (390 px, light + dark)
- [x] 2  report-ip: JWT skip-list + node-secret auth + fill-empty-only rule + per-node rate limit + WS path for new agents
- [x] 2b prod copy: list of nodes that would get a field filled
- [x] 3  agent: per-inbound bind failure isolation + exact port/reason; panel short Chinese error + never-used auto-move (≤3)
- [x] 4  stale shared counts excluded from every total / live row (verify + fix)
- [x] 5  edge landing range give-back + tests (both edges, comma specs)
- [x] 7  native Reality guard dedicated short-timeout direct outbound; real Core lab before/after numbers
- [x] 8  「落地 Core 待升级」 own amber chip on server card (admin, desktop + H5)
- [x] 6  tests: unit + contract SQLite + PostgreSQL, go-gost/x incl. real bind conflict; baseline comparison; frontend
      tsc/build/lint
- [x] 6b screenshots desktop 1440 + H5 390, light + dark; gallery on :8791
- [x] 6c resource numbers (agent CPU/RSS lab, panel request count)
- [x] push, CI green, rollout notes, final.md + summary.md

## Notes

### 1 — plan 141 setting is FLVX-native only (shared = fixed 入站+出站)
- Repo: `LandingConnModeSupported` is false for every shared server (even a share advertising
  `conn_modes` v1), `LandingNodeConnMode` returns total for shared, new `LandingConnModeFixed`. So the shared
  desired never carries `conn_mode`; views report configured = effective = total, pending false, `connModeFixed`.
- Save paths: shared server `connMode: inbound` refused (「共享服务器的节点连接数固定按入站+出站计算」), apply-to-existing
  ignored on shared; shared node `connMode: inbound` refused, `total` stored as NULL; a legacy stored 仅入站 (none on
  prod) is cleared on the next save instead of blocking it.
- Prod copy (fork.88 DB, 10-24): all 6 servers `conn_mode`/`max_conn_mode` = total, 0 landing nodes with a
  conn_mode → **no migration**. `SharedLandingDesired` for shared servers 2/3/4/6 on the prod copy: fork.88 vs this
  branch **byte-identical** (1543 B JSON, 0 `conn_mode`). vs fork.85 the only difference is plan 129's
  `reality_guard` (intended, released in fork.87).
- UI: shared server settings + shared node details show a read-only 「入站+出站」 field (same Input component, same
  height) where the select was; hint + apply checkbox gone; save helpers no longer send conn-mode fields for fixed
  servers/nodes. Native servers unchanged (selectors still there).
- Contract `/root/flvx-workers/contracts/landing-conn-mode-v1.md` marked 「暂缓（用户 10-24：妙妙屋侧不动）」 at the top
  (original kept as `/root/tmp-open-items/…orig` during the run only).
- H5 defect: 服务器总连接数（0 不限） + 总连接数计算方式 take one full row each below `sm` (label one line, nothing pushed
  down); desktop stays two columns (tops aligned). Checked by bounding boxes in the shots (40/40).

### 2 — report-ip
- Before: `/api/v1/node/report-ip` was not in the JWT skip-list → every agent report was answered 401 `bad_token`
  while the agent only checked HTTP 200 (the envelope is always 200) and logged success. Before that (upstream) the
  handler OVERWROTE `server_ip` + `server_ip_v4` + `server_ip_v6` (new format) or `server_ip` (old format); the WS
  `ReportPublicIP` message (never sent by our agents) overwrote `server_ip` too.
- What reads these fields: tunnel entry `in_ip` and hop dial addresses prefer `server_ip_v4`/`server_ip_v6` and use
  `server_ip` only when both are empty (`buildTunnelInIP`, dial selection); new native landing servers copy
  `server_ip_v4` (else `server_ip`) into `public_host`; node-sync rewrites tunnels when any of them changes; DDNS
  (plan 142) owns all three on its nodes. So even "fill an empty v4/v6" can switch tunnels from an admin's domain to
  a literal IP or add a second address family.
- Now: route skips JWT, handler authenticates by node secret (401 for missing/unknown), and both paths use
  `repo.NodePublicIPFill`: DDNS nodes never; only global-unicast public addresses; `server_ip` filled only when it is
  empty (with the matching family field); `server_ip_v4`/`server_ip_v6` filled only when empty AND `server_ip` already
  is exactly that literal (pure record, no dialed/shown address changes). Conditional UPDATE (`… IS NULL OR = ''`) so
  a concurrent admin edit wins. Per-node gate: ≥1 min between reports, same addresses ignored for 30 min; no write
  unless something is filled; one log line per fill.
- **Fields that can change now:** only empty ones, and only as above. Prod copy check with the real code (each node
  reporting its own literals and unrelated public v4/v6): **30 nodes, 0 fills** (all have `server_ip`; literal mains
  already have their family field; domain mains (31/35/45/49/55) and v6 mains (50/51) never get the other family;
  node 20 is private 10.7.x; node 48 is DDNS).
- Transport: new agents send `ReportPublicIP` {public_ip_v4, public_ip_v6} over the WS session (AES with the node
  secret inside wss) instead of the plain-HTTP POST with the secret in a header; fork.88-and-older panels ignore
  that message shape (they only read `public_ip`, so they cannot overwrite anything). Old agents keep using HTTP →
  now authenticated + fill-empty. Rate unchanged on the agent (≤ once per 10 min / local-address change).

### 3 — native landing port busy on the machine
- Agent (`go-gost/x/landing/inbound_errors.go`, `control.go`): adds are tried per tag in a stable order; a failed
  `adi` no longer returns: the half-registered tag is removed (`rmi`, real Core keeps the tag after a failed listen),
  the port is probed (bind TCP, plus UDP for SS2022, same listen address) → `port_in_use` + "TCP: address already in
  use" / "UDP: …", else `add_failed`; reported in `Status.node_errors` (always present on new agents); retried with
  backoff 10 s → 30 s → 1 m → 2 m → 5 m → 10 m (reset by a new revision or a changed config); routine log = first
  failure line, 10-min count, one recovery line.
- Panel: `landingSyncSnapshot.NodeErrors`; `ApplyNativeLandingNodeErrors` after each successful native sync:
  stale reports (port changed) ignored; `port_in_use` ports remembered 24 h in `landing_server.native_busy_ports`
  (≤64) and skipped by `landingFreePort`; a node that is active, created < 1 h ago, never carried bytes, not
  referenced by a rule and has < 3 moves gets the next free port (`port_moves`+1, revision+1) and the server is
  pushed again right away (≤3 extra pushes per sync); otherwise `sync_error` = 「端口 N 被这台机器上的其他程序占用（TCP:
  address already in use）」 (+「，已自动换端口 3 次仍失败」), shown as 节点异常 + red reason on the 落地 page and monitor for
  admin and owner. Fast path: no report and no stored error → one COUNT, no transaction. Old agents (no
  `node_errors`): behaviour as today (whole apply fails → nothing recorded or moved).
- Real Core lab (Core b01adef, busy TCP listener on node 1's port, node 2 free, then 6 ticks):
  fork.88 agent: apply error 「landing gRPC adi failed」 (no port), extra CLI calls in 6 ticks adi 7 + rmi 6 +
  statsquery 6 (= +19); this branch: apply ok, free inbound listens, node 1 reported with port + reason, extra CLI
  calls 0 (steady state lsi 6 + statsquery 6 both versions); after the program exits the next revision adds it.
  Core RSS 30.2→31.4 MiB, fds 10→12 across the test (no growth from retries).

### 4 — removed shared nodes keep reporting old counts
- Verified (mmwx Helper `flvx_runtime.go record()`, read-only): removed or disabled nodes are reported with state
  `removed` and Core `CurrentTotal` of their blocked identities → FLVX stores `remote_state=removed`.
- Every FLVX total/live view already uses `LandingLiveConnections` / `LandingModeConnections` (removed → 0): server
  admin list (`connections`, mode sums), node list/details, monitor (node rows, server sums, summary), user views;
  the frontend only shows backend numbers; caps on shared servers are enforced by mmwx, native counters are fresh
  per collect (no stale state). Share-level `connections` from Custom is not shown anywhere; the rule picker shows
  no counts. Deletion finalize keeps plan 139's 3-min grace. → no code change needed; new end-to-end regression
  `TestSharedRemovedNodeStaleConnectionsContract` (SQLite + PostgreSQL: removed node reporting 29 → server list 9,
  node row 0, monitor server sum 9, monitor row 0).

### 5 — edge landing range give-back
- Rule: every carve is recorded in `landing_server.rule_carved` (rule-spec syntax, add-only column). Ports leaving
  the landing range return when recorded as carved or (pre-147 rows) inside the hull [min, max] of the current rule
  spec, never while a landing node / chain node / guard holds them (held ports stay recorded). An edge carve
  (10000-65535 − 60000-65535) now comes back; a landing range the node never had in its rule range is never added.
  Pre-147 landings carved at an edge are still not given back automatically (nothing recorded) — prod has none
  (servers 1 and 5 are middle carves).
- Tests: 28-case math table (both edges, comma specs, partial overlap, held ports, move/shrink/grow, re-enable) +
  `TestNativeLandingEdgeGiveBack` (SQLite + PostgreSQL).

### 7 — native Reality guard dest leg
- Already fixed in plan 137 (F14, fork.77): the guard allow rules use the dedicated freedom outbound `flvxg-direct`
  on an undefined level (Core default uplink/downlink-only 1 s) for native landings and line-chain copies alike (one
  guard per dest/serverNames group); nodes 21/55 run it (fork.87). mmwx's note assumed FLVX still used `direct`.
- Re-measured with the current pinned Core b01adef (`TestGuardDestLegReleaseRealCore`, 20 new connections through
  the guard, dest keeps the connection open like a web server): shared `direct` → release median/max 5.0 s, +40 Core
  sockets for 5 s; `flvxg-direct` → 1.0 s / 1.0 s, +40 sockets gone by +1.5 s. Authenticated users never touch the
  guard; links/keys/ports unchanged. No code change.

### 8 — 「落地 Core 待升级」
- `ChipLine` items can be pinned (shown first, never folded); the chip moved to the admin line (FLVX 自有落地 …)
  with the same warning/flat chip style; desktop + H5 shots.

### 6 — tests / screenshots
- Baseline (fork.88, untouched worktree): `go test ./...` SQLite 10 failing tests, PostgreSQL the same 10
  (backup export/import, federation nodelay + 3 dual-panel, 2 legacy migrations, renewal anchor, service monitor,
  address-in-use contract); go-gost/x landing + socket ok.
- After (this branch): `go test ./...` SQLite 17 packages ok, PostgreSQL 17 ok; failing tests **identical** to the
  baseline lists in both modes (same 10 names, nothing new). New/changed tests: repo `TestSharedLandingConnModeFixed`,
  `TestLandingConnModeResolution` (+shared cases), `TestLandingRuleSpecMath` (28 cases), `TestNativeLandingEdgeGiveBack`,
  `TestNodePublicIPFillRule`, `TestFillNodePublicIPs`, `TestPublicIPReportGate`; handler `TestLandingPortBusyNodeErrors`,
  `TestLandingPortBusyKeepsReferencedNode`; contract `TestSharedLandingConnModeContract` (rewritten for 147),
  `TestNodeReportIPContract`, `TestSharedRemovedNodeStaleConnectionsContract`, admin field set +connModeFixed —
  all SQLite + PostgreSQL where the suite has both.
- go-gost/x: socket ok; landing ok without the Core binary. With the pinned Core b01adef supplied
  (`FLVX_LANDING_TEST_BINARY`) the new real-Core tests pass (`TestLandingPortBusyRealCore`,
  `TestGuardDestLegReleaseRealCore`) and two older real-Core tests fail **identically on the fork.88 base with the same
  binary** (`TestLandingRealCoreUpgradeRestart` needs the previous-pin Core, `TestEgressRealCoreAcceptsRenderedConfig`
  needs geo data) → pre-existing lab-environment gaps, not this change. go-gost `go vet` clean, agent builds.
- Frontend: tsc 0 errors; eslint on all changed files 0 problems; prettier clean; `npm run build` ok
  (index 3,001 kB, +~10 kB vs fork.88).
- Resource: agent — no new goroutine/timer/poll; a busy port now costs 0 extra Core CLI calls per 10 s tick (was
  adi+rmi+statsquery ≈ 3 per tick); steady state unchanged (lsi + statsquery per tick); public-IP report = one small
  WS message per (re)connect at most every 10 min instead of an HTTP POST; Core RSS 30–31 MiB in the lab. Panel — no
  new endpoint or frontend polling (request count unchanged; new agents send one HTTP request less per report);
  native sync adds one indexed COUNT per server per minute (no transaction unless an error is reported); report-ip
  writes only when it fills (0 on prod); one log line per fill / per moved node.
- Gallery (34 shots, 40/40 checks, 0 page errors, 0 saves):
  http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/open-items-fix/

## Rollout notes for the release worker
- Panel: next fork tag from this branch (fork.89 expected). Add-only columns `landing_server.rule_carved` /
  `native_busy_ports` (text, '') and `landing_node.port_moves` (int, 0) → rollback to fork.88 needs **no**
  `--restore-db`. No data migration (prod copy: no shared 仅入站 rows).
- After the panel upgrade: old agents' report-ip calls stop showing up as `bad_token` (they are authenticated now)
  and fill nothing on prod (0 of 30 nodes). Shared desired payloads identical to fork.88 (no revision bump, no Helper
  re-apply expected).
- OTA: only the native landing nodes **21** (canary, SSH backup as in plan 141/129) then **55**. The Core pin is
  unchanged (b01adef) → no 「落地 Core 待升级」 / Core restart needed; the agent restart does not restart the Core.
  Other agents do not need this OTA (their only change is the WS public-IP report, which fills nothing).
- Check after OTA: node 21/55 landing status `running`, `node_errors` empty in the apply reply (panel log has no
  「moved never-used nodes」 line), servers 1/5 revisions unchanged unless a node error appears.
- Never node 36; no mmwx change of any kind.

## Result (build only — no tag, no release, no prod write, no OTA, no mmwx change)
- Branch `feature/3.0.27-open-items-fix` on fork.88 (b3a87297): f4ba9c71 agent, 38f1e59d panel, bb02bd1e UI,
  5b47d202 test + plan; CI Build Check 40085365700 green (backend, frontend, agent, PostgreSQL contract).
- Gallery: http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/open-items-fix/

## Still open
- Landings carved at an outer edge BEFORE this release have no carve record → their edge ports still need a manual
  widen after disabling (prod: none, servers 1 and 5 are middle carves).
- Line-chain copies (exit-node chain nodes, 0 on prod): the agent reports a busy loopback port, the panel does not
  show it yet (chain ids are not landing nodes).
- mmwx Custom/Helper still reports the last connection count of removed nodes (mmwx side untouched by decision);
  FLVX ignores it everywhere.
- report-ip never adds a second address family or replaces a domain: a node whose v4/v6 field is empty keeps it
  empty unless `server_ip` already is that literal (by design, 0 fills on prod).
- Lab gaps: `TestLandingRealCoreUpgradeRestart` / `TestEgressRealCoreAcceptsRenderedConfig` need the previous Core
  pin / geo data (fail the same on fork.88).

## Release (fork.89)
Task `release-open-items-fix` (Claude, 10-24). Branch `maintenance/3.0.27-fork.89-open-items-fix` = feature head 4ea9ae49
(fast-forward on fork.88 b3a87297, code identical to the build) + plan-doc commits only. Rollout: panel, then OTA of the
native landing nodes 21 (canary, SSH backup) and 55 only; no mmwx change, never node 36.

- [x] R1 gallery gate: 11 of 34 shots opened (shared settings desktop/H5, H5 total row, Core chip desktop/H5, shared node
      details H5, all-nodes desktop/H5 with port-busy rows, user nodes H5) — no defect from this build → no fix, no re-shoot.
      (Pre-existing, not touched: on H5 long labels such as 「流量上限 (GB，计费流量，0 不限)」 wrap and push their input down.)
- [x] R2 build + tests on the release branch: go-backend + go-gost build ok; go-gost/x landing/socket/service ok;
      backend SQLite 17 ok / PostgreSQL 17 ok, failing lists identical to fork.87/88 (10 tests + 5 subtests, each mode);
      frontend tsc 0, build ok (index 3,001.33 kB), eslint src 96 errors / 3754 warnings = identical to fork.88 base.
- [x] R3 prod-copy checks (fresh read-only copy, deleted after): report-ip fill rule on 30 nodes × 5 report variants
      → **0 fills**; shared desired fork.88 vs fork.89 byte-identical (1543 B, 0 `conn_mode`); with the egress plan:
      native payloads (21: 105 B, 55: 1303 B) and shared payloads identical; admin landing views only add `connModeFixed`;
      list latency unchanged (server/list p50 2.6 ms both).
- [x] R4 push, CI green, tag `3.0.27-fork.89`, release Latest with the same 10 assets — branch CI 40087128569 green
      (backend, frontend, agent, PostgreSQL contract); tag → 00a968ab; Build and Push Images 40087239795 green; release
      validation 14/14 (Latest, not prerelease, 10 assets = fork.88, compose + both scripts pinned to fork.89, gost sha256 ok,
      `gost 3.0.27-fork.89`, ghcr backend/frontend amd64 + arm64).
- [x] R5 prod (10-24 18:56 TW): rollback dir `/opt/flvx-svc/rollback/pre-fork89-20261024T105647Z` (DB quick_check +
      integrity_check ok, local image tags, rollback.sh, no `--restore-db` needed); upgraded, healthy after 13 s; prune kept
      pre-fork89 + pre-fork88 (pre-fork87 removed).
- [x] R6 prod checks: version.json `3.0.27-fork.89-mvm9zerl` local + public; 30/30 online; every node address field,
      DDNS records, tunnel / chain / forward-port addresses identical before/after (also after the agents' reports);
      only add-only columns appeared (rule_carved / native_busy_ports '' on all 6 servers, port_moves 0 on all 4 nodes),
      old columns identical except traffic counters; 2nd restart: schema + config identical, nothing re-ran; report-ip
      29× `-> 200` per restart, **0 `bad_token`**, 0 fills; shared servers 2/3/4/6 `connModeFixed=true` (入站+出站),
      native 1/5 selectable; @jiuwei view unchanged (no new fields), save 403, report-ip without / wrong secret 401;
      landing views pre/post differ only in live counters + `connModeFixed`; server revisions 276/231/183/56/23/37 unchanged.
- [x] R7 OTA (panel `node/upgrade`): node 21 canary (SSH backup `/root/flvxx-backup-20261024T110139Z-pre-fork89`) and
      node 55, both back in 4 s on fork.89; Core not restarted (21 PID 1228701, exe = disk b01adef, not deleted; 55 node 97
      epoch = 16:18:23 TW = the plan-129 confirmed restart), `core_stale_epoch` empty on both → no Core confirm needed;
      diagnose 21: 27/27 → 27/27; real sing-box client 3/3 on nodes 10/95/97/105 before and after; no port-busy lines.
- [x] R8 10-min numbers (window 19:05–19:15 TW, after the OTA): backend 33.5 MiB, CPU 4.0–5.4 % (3-min /proc average
      4.3 %) vs fork.88 31.9 MiB / 2.4–3.6 % (fork.83–88 ranged ~2–7 %; no new periodic work); DB 57.22 MB (57.11 before);
      11 log lines / 10 min (all reconnect records of the known flapping nodes 12/26/45/55) vs 13; `bad_token` 0 (was one per
      reconnect); final check: config tables, node addresses, server revisions identical to the pre-upgrade backup except
      traffic counters and the add-only columns.

### Rollback
- Panel: `bash /opt/flvx-svc/rollback/pre-fork89-20261024T105647Z/rollback.sh` (→ fork.88, no `--restore-db` needed);
  further back `pre-fork88-20261024T090759Z` (→ fork.87).
- Node 21: `ssh mmwx-s11 sh /root/flvxx-backup-20261024T110139Z-pre-fork89/restore-agent.sh` (→ fork.87, Core untouched).
- Node 55 (no SSH): node console `systemctl stop flvxx && cp -a /etc/flvxx/flvxx.old /etc/flvxx/flvxx && systemctl start flvxx`.
