# 141 — Landing 连接数 计算方式: 仅入站 / 入站+出站

Source: user 2026-10-24 ~01:10 TW voice message (shared 「PQS TW」 每节点默认连接数 500 = Core port in+out TCP all
states; user is used to giving 100 *inbound* like rule 连接数). Brief: `/root/flvx-workers/runs/landing-conn-mode/brief.md`.
Base: `maintenance/3.0.27-fork.85-landing-compact` (fork.85, newer than the expected fork.80). Branch
`feature/3.0.27-landing-conn-mode`. Build + screenshots only — no tag, no release, no prod change, no OTA, no mmwx edit.

## Facts verified on the base
- Agent installs mmwx-custom `v1.4.59` Core = Xray-core-mmwx `8f5b6145`; Helper FLVX Core pin `cfb0f591`; latest
  `v1.4.76` = `b01adef0`. All three have `PortLimit.max_inbound_connections` (admission after auth, per logical request,
  released when the request ends), `Config.max_global_inbound_connections`, and per-user snapshot `inbound_current`.
- Today: node `max_connections` → Core `max_total_connections` (native agent `control.go`, Helper `flvx_runtime.go`
  `limits()`); native server `max_connections` → `max_global_total_connections`; shared server total = mmwx share cap.
- Agent `Apply` rejects a reused revision with different content → any payload change needs a revision bump.

## Design
- `landing_server.conn_mode` (`total`|`inbound`, default `total`) = 计算方式 of 节点连接数 for nodes that follow the
  server (and new nodes). `landing_server.max_conn_mode` = 计算方式 of the native 服务器总连接数 (shared total is owned
  by 妙妙屋 = always 入站+出站). `landing_node.conn_mode` nullable (NULL = follow server), like `flow_type`.
- Server save: changing `conn_mode` pins following nodes to the old mode unless 「应用到现有节点」 is ticked; ticked =
  every node of the server follows the server again and gets the server's 节点连接数 (one tx, one revision bump).
- Effective mode = configured mode if the runtime supports it, else `total` (+ hint): native agent capability
  `landing_conn_mode >= 1` (new WS field, node column); shared = share `conn_modes.versions` contains 1
  (contract `landing-conn-mode-v1`).
- Payloads: `conn_mode: "inbound"` per node / `max_conn_mode: "inbound"` top level only when effective inbound
  (omitted for total → existing payloads byte-identical). Native revision bump via `landing_server.conn_mode_sig`.
- Counters: agent + Helper report `inbound_connections` (Core `inbound_current` sum per tag) next to `connections`;
  stored in `landing_node.inbound_connections`; views show the count in the node's effective mode + label.

## Tasks
- [x] Model + migration (server conn_mode / max_conn_mode / conn_mode_sig, node conn_mode / inbound_connections,
      node.landing_conn_mode); mode resolution helpers + validation
- [x] Server save (pin / apply-to-existing), node update override, create node (follow server)
- [x] Agent: Desired/Node conn_mode, applyLimits inbound fields, Counter inbound_connections, WS capability
- [x] Panel native: capability persist, payload mapping + revision sig, counter ingest
- [x] Shared: landingcustom Share.ConnModes / Node.ConnMode / NodeStatus.InboundConnections, desired + status
- [x] Views: server/node/monitor connections in effective mode, labels, pending hints, user view label
- [x] Frontend: selectors + hints (server settings, node details), labels (cards, monitor, user view)
- [x] Unit tests (backend + agent) + contract tests SQLite + PostgreSQL; baseline comparison
- [x] Real Core lab (netns): inbound 3 accepts 3 with ≥3 outbound sockets, rejects 4th; total unchanged
- [x] Prod DB copy: payload/desired/plan identical before/after, list latency, payload size
- [x] Contract doc `/root/flvx-workers/contracts/landing-conn-mode-v1.md`
- [x] Screenshots desktop 1440 + H5 390, light + dark; review; gallery on :8791
- [x] Push branch, CI green
- [x] final.md + summary.md

## Result (2026-10-24, build only — no tag, no release)
- Commits c6b704c2 … a5520bff on `feature/3.0.27-landing-conn-mode` (base fork.85 7f4b1022); CI Build Check
  40065361123 green (backend, frontend, agent, PostgreSQL contract).
- Core check: agent Core v1.4.59 = 8f5b6145, Helper pin cfb0f591, v1.4.76 = b01adef0 all have
  `PortLimit.max_inbound_connections` + snapshot `inbound_current` → no Core change.
- Tests: new unit tests (repo resolution/save/pin/apply/revision/desired/counters, handler payload gating + views,
  landingcustom wire, agent control payload/validate/counters) + contract `TestSharedLandingConnModeContract`
  (SQLite + PostgreSQL); `go test ./...` SQLite and PostgreSQL: 17 ok, the same 15 baseline failures as fork.85
  (identical lists); `TestLandingTargetListQueryCount` kept (agent capability joined into existing queries);
  admin server field-set test extended by the 5 new keys. go-gost/x `landing` + `socket` ok.
- Real Core netns lab (`conn_mode_netns_test.go`, Core 8f5b6145): 仅入站 limit 3 → 3/5 accepted with 8 tracked
  sockets, released ≈5.1 s after close (downlinkOnly), reopen 3/3; 入站+出站 limit 3 → 1/5.
- Prod DB copy (offline): native payloads + shared desired byte-identical base vs new (also with all agents
  upgraded), existing view values unchanged; list p50 1.65→1.82 / 0.91→1.22 / 0.57→0.66 ms.
- Payload: Core control +31 B per 仅入站 node, 0 for 入站+出站; counter +≈25 B per node. Bundle +4.8 KB.
- Contract `/root/flvx-workers/contracts/landing-conn-mode-v1.md` (mmwx changes §6, not implemented).
- Gallery: 58 shots, 56/56 checks, 0 page errors, 0 writes —
  http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/landing-conn-mode/
- Release round: panel (columns only, rollback without --restore-db) → OTA native landing nodes 21 + 55 →
  mmwx Custom + Helper (one release) per contract §6.

## Release (fork.86, task `release-landing-conn-mode`, 2026-10-24)
Approval: user 2026-10-24 ~04:40 TW waived the screenshot gate for this queue; the release worker is the gate.
Branch `maintenance/3.0.27-fork.86-landing-conn-mode` from the feature head 2e2bb9e1 (= fork.85 7f4b1022 + plan 141,
fast-forward, no conflicts). mmwx Custom/Helper side is NOT built (contract only) → FLVX side only; shared servers
degrade cleanly (仅入站 shows 「需要妙妙屋侧升级后生效」 and stays 入站+出站).

- [x] Gallery gate (release worker opened 14 of the 60 shots covering every view — server settings native/shared/old
      agent, node details pending, cards, all-nodes table, monitor, user monitor; desktop + H5, light + dark — plus the
      build's 56/56 automated checks; no real defect → no code change, no re-shoot; H5 label wrap next to a select is
      the same pre-existing pattern as 在线 IP / 流量上限)
- [x] Rebase/branch, `go build ./...`, backend tests SQLite + PostgreSQL (only the 15 known failures), frontend tsc/build/lint
      — fast-forward (no conflicts); go-backend + go-gost build ok; `go test ./...` SQLite 17 ok / 15 FAIL and PostgreSQL
      17 ok / 15 FAIL, both lists identical to fork.85; `TestSharedLandingConnModeContract` sqlite+postgres PASS;
      go-gost/x landing + socket ok; tsc 0 errors; build ok (index 2,991.25 kB, same as the build round); eslint 96 errors
      (= fork.85), warnings 3762 → 3754
- [x] Equivalence gate on a fresh read-only prod DB copy (native payloads + shared desired identical fork.85 vs fork.86), copy deleted
      — servers 1/5 native payloads (105 / 1303 B) and shared 2/3/4/6 desired byte-identical, also with all agents
      upgraded; server/node/monitor views: 0 changed values, only the new keys; list p50 +0.1–0.8 ms in-process
- [x] Push branch, CI green, tag `3.0.27-fork.86`, release Latest with the same asset set as fork.85
      — branch head 04800c57, CI Build Check 40067907191 green (4 jobs); tag → 04800c57; Build and Push Images
      40068025889 green; release gate 13/13 (Latest, 10 assets = fork.85, compose + scripts pinned, gost sha256,
      ghcr amd64+arm64); released agent `gost 3.0.27-fork.86`
- [x] Prod panel: validated backup + rollback dir `pre-fork86-<ts>`, upgrade `/opt/flvx-svc`, prune (keep 2)
      — `/opt/flvx-svc/rollback/pre-fork86-20261024T060354Z` (quick/integrity ok; rollback.sh → fork.85, no
      `--restore-db` needed); upgraded 14:04:03 → healthy 14:04:12 TW; pruned pre-fork84 + fork.83 images
- [x] Prod verify: version, health, nodes online, migration once (2nd restart), new UI in bundle, real-JWT admin/non-admin checks, data untouched
      — version.json `3.0.27-fork.86-mvlzisdv` (local + public); 30/30 online; new columns all default (server
      conn_mode/max_conn_mode = total ×6, sig '' ×6, node conn_mode NULL ×4); revisions 273/230/182/55/20/36 unchanged;
      2nd restart: config tables + schema identical, no migration lines, 30 reconnects 0 updated/0 failed; bundle has
      the 计算方式 / 仅入站 / hint texts; admin + @jiuwei read-only API: 0 changed values (only live counters), admin
      gets connMode/maxConnMode/connModeSupported, @jiuwei node list only connModeEffective, server list no conn
      fields, unauth 401; 16 config tables identical, landing_node identical except the 2 new columns
- [x] OTA native landing nodes 21 (canary, SSH backup) then 55 (panel OTA), Core PID / traffic checks
      — node 21: SSH backup `/root/flvxx-backup-20261024T060550Z-pre-fork86`, reconnect 4 s, wss→wss, Core PID
      890894 unchanged, diagnose 27/27 → 27/27, capability landing_conn_mode=1, server 1 rev 273 unchanged (6 tunnels
      re-applied once after the agent restart, as on every agent restart since fork.79); node 55: panel OTA, reconnect
      4 s, Core epoch of landing node 97 unchanged, server 5 rev 20 + egress hash unchanged, capability 1
- [x] After 10 min: CPU/RSS, DB size, log lines vs fork.85
      — backend 33.4 MiB / 3–7 % CPU (fork.85 33.7 MiB / 4–6 %), DB 57.11 MB (same), 0 log lines / 10 min, restarts 0

Open after fork.86: mmwx Custom + Helper side of `landing-conn-mode-v1` not built (shared servers show the hint and
stay 入站+出站); PQS TW stays 500 入站+出站 until an admin switches it; node 21/55 are the only fork.86 agents (others
fork.79, 48 fork.82); `sharedCaps.core` alternates between share-list version and status epoch (pre-existing, cosmetic).
