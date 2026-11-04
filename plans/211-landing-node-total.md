# 211 — landing node total (节点总连接数 400, monitor 入站/总, 节点详情 info-only, 「总」 labels)

Branch `feature/3.0.27-landing-node-total` from tag `3.0.27-fork.115` (prod). BUILD ONLY — no release, no prod change.

## Findings (verified 2026-11-05)

- **Core `current_total`** (pinned Core, `xray-core-vision-limiter` `common/mmwxcustom/connection/tracker.go`):
  `addTCPState` counts every kernel TCP socket owned by the identity in any state except LISTEN
  (ESTABLISHED, SYN_*, FIN_WAIT*, **TIME_WAIT**, **CLOSE_WAIT**, LAST_ACK, CLOSING, CLOSE);
  `current_total = inbound_tcp.total + outbound_tcp.total`. Port limit `max_total_connections`
  (`port_limits[].max_total_connections`) is checked in `bindInbound` (new client) and on every new
  outbound dial against `portCurrentLocked(tag)` = the same socket occupancy (+ pending dials) →
  rejects NEW connections at the cap, existing sockets stay, TIME_WAIT is not cleared.
  `inbound_current` = admitted client connections (what 「入站」 limits count).
- Real machines (read-only, 11-05 ~03:30 TW): Breadcloud JP and NexKR FLVX landing Cores answer
  control API v7 with the same `proxy_users[].inbound_tcp/outbound_tcp/current_total` fields;
  both had 0 live connections at the time (no traffic), so the TIME_WAIT/CLOSE_WAIT inclusion is
  proven by the lab test with the same pinned Core binary (copied read-only from Breadcloud JP, sha256
  1929c073…): total 8 → 4 clients; after close 4 TIME_WAIT still counted and only 2 new clients fit;
  half-closing target → 3 CLOSE_WAIT counted; 入站 3 + 总 5 → total hit first; 入站 2 + 总 100 → inbound first.
- Agent today: per node either `max_inbound_connections` (conn_mode inbound) **or**
  `max_total_connections` (no conn_mode). Helper (mmwx-custom `flvx_runtime.go limits`) is the
  same either/or. → A node total can be enforced **panel-only** whenever the node's inbound limit
  is 0 (send the total as `max_connections` without `conn_mode`). Both limits at once need the new
  agent (capability `landing_conn_mode` 2) / a Helper change (contract below).
- Prod: native servers 1 Breadcloud JP / 5 NexKR: server total 0, every node inbound 0.
  Shared 2/4/6/7 `max_connections` 1000/500/800/800 = **妙妙屋 share cap** (`shared_caps.max_connections`)
  copied by the sync; the Helper already enforces it as the FLVX Core's global total. The sync
  also copied it into `default_max_connections` and into every shared node with 0
  (`max_connections = 0 OR > cap` → cap) — the 「800 (入站)」 on PQS TW / Boil Hinet 158 nodes.

## Design

- `landing_server.max_connections` (服务器总连接数) hidden + forced 0 on save; one-shot migration sets
  it to 0. Shared sync no longer copies the share cap into it, into `default_max_connections` or
  into node limits; share cap stays in `shared_caps` (read-only in 妙妙屋共享上限 and monitor hint).
- New `landing_server.default_max_total_connections`, `landing_node.max_total_connections`
  (0 = 不限). One-shot migration (column was missing): every server + node → 400; shared copies
  of the share cap in node / default inbound limits → 0.
- Payload: inbound > 0 and total > 0 → both fields only to capable runtimes (agent
  `landing_conn_mode >= 2`, share `node_total` v1); otherwise inbound only (total 「待升级」).
  Inbound 0 and total > 0 → total as `max_connections` without `conn_mode` (works everywhere).
  Content joins the native conn signature / shared hash → revision advances.
- Agent (cap 2): `nodes[].max_total_connections` next to inbound; counters gain
  `tcp {inbound, outbound, established, time_wait, close_wait}` from the snapshot it already reads.
- Panel keeps the breakdown in the in-memory landing rate tracker (no DB writes, no new polling).

## Checklist

- [x] Lab test: pinned Core counts TIME_WAIT/CLOSE_WAIT in current_total and rejects at the port total (`TestNodeTotalNetnsE2E`, runs/landing-node-total/nodetotal-e2e.json)
- [x] Model + one-shot migration (400 defaults, server totals → 0, shared copies → 0) + tests
- [x] Server save: hide/force server total 0, default node total + 「同时应用到现有 N 个节点」
- [x] Node create: default node total; shared sync no longer copies share cap
- [x] Native payload + signature (panel-only path + capability 2 path)
- [x] Shared desired (panel-only path + `node_total` v1 path) + capability parse
- [x] Agent: max_total_connections + tcp breakdown + capability 2 + tests (cd9b291b)
- [x] Counters: tcp breakdown into rate tracker (native + shared)
- [x] Views: node view (inbound/total/limits/pending), server view, monitor (node + server sums + native machine) (ec374be5)
- [x] Frontend: 落地服务器设置 (native + shared)
- [x] Frontend: 节点详情 info-only (+ 转移)
- [x] Frontend: 监控 落地 node row 入站/总/上限 + amber/red + breakdown popover; server row sums + 整机兜底
- [x] Frontend: 用户设置 「总」 labels; landing cards/table/ConnPill/serverConnRow/conn-mode hints
- [x] Backend tests (go test ./...), agent tests, frontend tsc/build/lint vs base
- [x] Demo panel + screenshots (desktop 1440 + H5 390, light + dark) → /root/mmwx-preview/landing-node-total/
- [x] Resource numbers (panel RSS/CPU, monitor payload before/after)
- [x] runs/landing-node-total/mmwx-needed.md, release-notes.md, RELEASE-LEDGER row
- [ ] Push + CI green; summary.md

## Results
- Screens: http://104.145.236.29:8791/landing-node-total/ (48 shots, 96/96 checks, 0 page errors).
- Demo e2e: fake agents (landing_conn_mode 2 / 1) got `conn_mode inbound + max_total_connections 400` /
  `max_connections 400` (no conn_mode) resp.; node with 入站 50 on the v1 agent keeps 入站 only (「总上限待升级」).
- Release notes: runs/landing-node-total/release-notes.md; 妙妙屋: runs/landing-node-total/mmwx-needed.md.
- Tests: backend `go test ./...` green (incl. contract; 3 contract tests updated to the new semantics);
  repo + landing contract tests also on PostgreSQL 18 (temp container); agent `landing` + `socket` green, real-Core
  netns tests (node total, conn mode, CLOSE_WAIT) green; frontend tsc + build OK, eslint 0 (base 0).
- Resources (demo, same seed, fork.115 → 211): panel RSS 39.0 → 38.8 MiB, CPU same; admin monitor 5 478 → 7 762 B
  (gzip 1 020 → 1 442 B) for 7 servers / 9 nodes; node list 12 353 → 13 186 B; agent upload +≈80 B per node / 5 s.
