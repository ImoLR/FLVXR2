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
- [ ] Model + migration (server conn_mode / max_conn_mode / conn_mode_sig, node conn_mode / inbound_connections,
      node.landing_conn_mode); mode resolution helpers + validation
- [ ] Server save (pin / apply-to-existing), node update override, create node (follow server)
- [ ] Agent: Desired/Node conn_mode, applyLimits inbound fields, Counter inbound_connections, WS capability
- [ ] Panel native: capability persist, payload mapping + revision sig, counter ingest
- [ ] Shared: landingcustom Share.ConnModes / Node.ConnMode / NodeStatus.InboundConnections, desired + status
- [ ] Views: server/node/monitor connections in effective mode, labels, pending hints, user view label
- [ ] Frontend: selectors + hints (server settings, node details), labels (cards, monitor, user view)
- [ ] Unit tests (backend + agent) + contract tests SQLite + PostgreSQL; baseline comparison
- [ ] Real Core lab (netns): inbound 3 accepts 3 with ≥3 outbound sockets, rejects 4th; total unchanged
- [ ] Prod DB copy: payload/desired/plan identical before/after, list latency, payload size
- [ ] Contract doc `/root/flvx-workers/contracts/landing-conn-mode-v1.md`
- [ ] Screenshots desktop 1440 + H5 390, light + dark; review; gallery on :8791
- [ ] Push branch, CI green
- [ ] final.md + summary.md
