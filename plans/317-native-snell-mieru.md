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
- [ ] 1. Investigate: agent / backend / frontend touch points (plan 274 pattern), mmwx render + caps, Core
      Snell UDP fix in the pinned Core.
- [ ] 2. Agent: types (consts, structs, validate, inbound render, defer accept), UDP ports for Mieru
      (publicPorts, inbound_errors, user_rate), capability report in websocket_reporter.
- [ ] 3. Agent unit tests: inbound JSON for Snell v4/v5 + obfs, Mieru, validation, publicPorts / rate UDP.
- [ ] 4. Backend: model columns + ws ingestion + SetNodeLandingCapabilities, nativeSends / reasons / create
      + switch-on gates, desired render (snell / mieru objects), revision signature, view flags.
- [ ] 5. Backend unit/contract tests: caps gating, old-agent refusal, desired JSON, byte-identical links / Clash /
      Surge / subscriptions native vs shared.
- [ ] 6. Frontend: 扩展协议 box + create-node for native Snell / Mieru (add only); `npm run build`.
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

## Draft cutover plan — Dmit T1 (NOT executed)
(filled in at the end)
