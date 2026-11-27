# 317 — Snell + Mieru on native FLVX landings (BUILD + lab + screenshots)

Task `native-snell-mieru` (Claude xhigh), branch `feature/3.0.27-native-snell-mieru` from tag
3.0.27-fork.176 (7b5aab50 = prod). BUILD ONLY: no tag, no release, no prod change, no OTA.

## Why
- User 11-27: every mmwx box moves to FLVX native landings. Dmit T1 is blocked: clot88's node 168 there is
  **Mieru** on the mmwx share, and native landings refuse Snell / Mieru (`sharedOnlyProtocol`).
- 「这个协议是肯定要全部全部都要支持的」: native landings support every protocol the shares support, done the
  way AnyTLS was (plan 274). Which protocols are shown stays the admin's per-server 协议 checkboxes.

## Design
- Agent: `landing_snell 1` / `landing_mieru 1` capabilities; Snell inbound = mmwx render (users psk / version /
  email, obfsMode/obfsHost for http/tls; TCP only, UDP over the stream); Mieru inbound = mmwx render (users
  username / password / email; one inbound on TCP + UDP of the port, no transport key). Both public inbounds
  defer accept (TCP_DEFER_ACCEPT 130, TCP sockets only) like plan 274 AnyTLS, so silent warm-pool conns stay
  in the kernel. Mieru's port counts UDP wherever ports are handled (publicPorts → machine protection,
  inbound errors, user rate).
- Panel: nodes withheld from agents below the capability (port / creds / counters stay), revision bumps only
  on servers that have such nodes, server 协议 switch-on / node create refused with 「节点程序版本过低…」 on old
  agents, view flags `snellAvailable` / `mieruAvailable` (+ reasons) on native servers. Links / Clash / Surge /
  subscriptions are source-agnostic → byte-identical to a shared node with the same fields.
- UI add-only: the 扩展协议 box on native servers offers Snell / Mieru (existing Snell / Mieru settings rows),
  create-node offers them when ticked. New servers' default stays ss2022 + Reality.
- 线路机 stays Reality + AnyTLS; chain exits unchanged.

## Checklist
- [x] 1. Investigate: agent / backend / frontend touch points (plan 274 pattern), mmwx render + caps, Core
      Snell UDP fix in the pinned Core.
- [x] 2. Agent: types (consts, structs, validate, inbound render, defer accept), UDP ports for Mieru
      (publicPorts, inbound_errors, user_rate), capability report in websocket_reporter.
- [x] 3. Agent unit tests: inbound JSON for Snell v4/v5 + obfs, Mieru, validation, publicPorts / rate UDP.
- [x] 4. Backend: model columns + ws ingestion + SetNodeLandingCapabilities, nativeSends / reasons / create
      + switch-on gates, desired render (snell / mieru objects), revision signature, view flags.
- [x] 5. Backend unit/contract tests: caps gating, old-agent refusal, desired JSON, byte-identical links / Clash /
      Surge / subscriptions native vs shared.
- [x] 6. Frontend: 扩展协议 box + create-node for native Snell / Mieru (add only); `npm run build`.
- [ ] 7. Full tests: go-backend vet + test (SQLite + PG handler/repo/contract `-p 1`), go-gost/x landing + socket.
- [ ] 8. Lab (netns, real pinned Core, mihomo ≥ 1.19.32): Snell v4/v5 × obfs none/http/tls TCP + UDP DNS;
      Mieru TCP + UDP: connect, counters, conn cap, online-IP cap, disable → cut, rate, warm idle hold, CPU vs VLESS.
- [ ] 9. Prod-DB copy: render before/after byte-identical for every existing server; node 168 as native → link /
      Clash / sub identical to today.
- [ ] 10. Screenshots desktop + 390 px + dark → gallery :8791/native-snell-mieru/.
- [ ] 11. mmwx lessons list; draft Dmit T1 cutover plan (below).
- [ ] 12. Push branch, CI green, ledger row 待发布 (等用户看截图), summary.

## Evidence
(filled in as tasks finish)

## mmwx lessons (Snell / Mieru on the shared Core) — what was reused, what not
- Reused as is: the Core inbound render (users psk / version / email + obfsMode / obfsHost; Mieru users username /
  password / email, no transport key), the validation rules (PSK / password 16–64 alnum, Mieru user 4–32 alnum, obfs
  host = DNS name), Snell v6 left out, Mieru = TCP + UDP on one port for machine protection (Helper
  `appliedPorts`) — natively in `publicPorts` → machine protection, user rate and the inbound-failure probe.
- Reused: block identities first, then remove the inbound (`rmi`) on disable — the contract found Mieru over a UDP
  underlay is cut only by `rmi`; the native agent already does block → remove (lab: disable cut below).
- Snell UDP fix gate (Helper advertises Snell UDP only with Core feature `snell.udp_per_packet_target`): not needed
  natively — the pinned Core v1.4.83 (f6f58447) and the previous pin v1.4.77 (2bf03c9a) both contain upstream fix
  3f844db6 (git ancestry), and the real-Core test reads the feature from `/v1/features`. Native client configs keep
  `udp: true`, so a moved node's config is byte-identical to a capable share's.
- Improvement over mmwx (not copied back here): mmwx renders Snell / Mieru without sockopt, so a silent warm-pool conn
  reaches the shared Core and is held only by the 120 s handshake (Custom v1.4.95), and counts against the node's 连接数
  meanwhile. Native Snell / Mieru defer accept like plan-274 AnyTLS (TCP_DEFER_ACCEPT 130, TCP socket only): the silent
  conn stays in the kernel (~247 s), never reaches the Core. Suggest the same to mmwx later (contract-change note) —
  not needed now, every mmwx box is moving to native.
- mmwx defect noticed (not ours to fix, no effect on FLVX): `internal/flvx/render.go` renders nothing protocol-specific
  when the protocol / object do not match (falls through to a bare inbound) and always renders VLESS with flow vision
  (a moved no-flow Reality client would break there — FLVX native handles flow "" since plan 291). Validation in
  `validate.go` catches the first case before render.
- Kept: Mieru UDP CPU hint (`MIERU_UDP_HINT`) and server default 客户端传输 TCP — the lab repeats the CPU comparison.

## Draft cutover plan — Dmit T1 (NOT executed)
Done by a later task, after the user approves the screenshots and fork.177 is released. Names: FLVX node 「Dmit T1」
(node 34, 154.3.39.138), its mmwx share = landing server 4 「Dmit T1」 (mmwx server 10), clot88's node 168 「1」
(Mieru, :30000, username 10 / password 24 chars, opts `{"transport":"TCP"}`, 0 forwards, 0 subs), mmwx official
:10015 VLESS Reality (admin, dest lovelive, 防盗; 救命版 fwd 81 goes through Dmit Nathan :13346 to it).

Before (no user impact):
1. Release fork.177 (panel: + `node.landing_snell` / `node.landing_mieru`, rollback = image swap, columns stay).
2. OTA **only Dmit T1** to fork.177 (idle: no FLVX forwards; Core PID unchanged; the agent restart does not touch
   official xray / mmwx / 救命版 flux_agent). Check `node.landing_mieru = 1` for node 34.
3. Backups: FLVX DB copy, mmwx DB dump (mmwx-prod), Dmit T1 host files (official xray config, mmwxc-helper /
   mmwxc-flvx-core state). Read the official :10015 credentials from the host config into a 0600 file (never printed).
4. Scripts ready (copy plan 313's `ls-create.py` / `nodes313.py` / `cutover313.py` / `rollback-cutover313.py`):
   native landing server on node 34 = range covering 10015 and 30000 (e.g. 10015–39999 after a busy-port probe; 救命版
   flux_agent ports and the share range checked), protocols ss2022 + vless_reality_vision + mieru (+ snell only if the
   user wants it shown), vendor DMIT, public host 154.3.39.138, Reality 防盗 on for the :10015 node, Core limits sized
   for the host.

Cutover, in a quiet moment (02–06 TW or `ss` shows no live conns on :10015 / :30000) — creating the native landing on a
shared host pauses the share at once (memory dmit-pqs-to-flvx), so that IS the cutover moment:
5. `landing/server/enable` node 34 → native server N (share 4 pauses; node 168 is down from here, ~0 traffic).
6. Move node 168 in place: `UPDATE landing_node SET landing_server_id = N WHERE id = 168` (same id / port 30000 /
   username / password / protocol_opts / counters / expiry) + `user_subdomain_keep` row (node 34, kind landing, ref 168,
   ref_created = its created_time) so its 直连 keeps 154.3.39.138 (node 34 has 用户专属域名 ON). The agent validates
   Mieru creds as 4–32 / 16–64 alphanumerics; 168's are FLVX-made (10 lowercase / 24) — check before the update.
7. Insert the admin node for :10015 (VLESS Reality, same UUID / flow / private key / short id / dest / serverNames,
   status disabled) + keep row; stop mmw-agent, mmwxc-helper, official xray and mmwxc-flvx-core on Dmit T1; enable the
   node (API → sync); wait until flvx-landing-core listens on 10015 and 30000 (TCP **and UDP** for 30000); print the gap.
8. Verify: links API of 168 before/after byte-identical (prod-DB copy proof below); mihomo from a neutral box with
   clot88's unchanged link, Mieru TCP + UDP transport; 救命版 fwd 81 via Dmit Nathan :13346 (real client) + direct
   :10015; counters move; admin ⚠ none.
9. Then delete mmwx server 10 + its users / data with Custom server-delete (as PQS TW, plan 313); the revoked share
   row 4 shows 「妙妙屋落地流量入账失败」 until Custom retires it (cosmetic). 救命版 flux_agent untouched.
Rollback before step 9: disable the :10015 node, `landing_server_id = 4` for 168, delete native server N, start
official xray + mmwxc-flvx-core + mmwxc-helper + mmw-agent (share resumes). After step 9: restore from the backups.
Open: expiry mismatch — FLVX 2027-01-13 (year) vs mmwx 2026-12-01 (month, price 0); the user decides which one the
native server's 到期 gets.
