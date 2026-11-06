# 230 — relay egress IPs: NAT nodes' egress addresses count as relays on landings (node105-drops)

Run dir: /root/flvx-workers/runs/node105-drops (diagnosis of jiuwei's PQS TW SS node 105 「🇨🇳 HiNet」 drops).

## Problem
Landings (shared 妙妙屋 Core via Custom/Helper, and native FLVX Cores) recognise a relayed connection by its
source IP: the relay sources are the IP literals of `LandingHostInventory().Nodes` (each node's address fields,
resolved names, DDNS addresses). A NAT node dials landings from addresses that are none of those fields.
Leikwanhost 沪台IPLC (node 48, address = DDNS name with only an AAAA record) leaves through two Taiwan IPv4s per
flow (103.246.219.39 Sky Digital / 114.35.6.24 Hinet). PQS TW has no AAAA, so tunnel 75 「拼车hinet3 利群沪台IPLC」
reaches it over IPv4 and the Core sees those two IPs as **jiuwei's own client IPs**:

- PQS TW Core group `flvx-user-8`: `inbound_online_ips` = the two node 48 egress IPs, `max_inbound_online_ips` 3,
  `rejected_user_online_ip_limit` 73 since 11-04 16:16 TW.
- Panel live usage (user/list): jiuwei 5/5 总 IP (2 real at the 沪台 entry + 1 elsewhere + 2 phantom egress) →
  the 沪台 entry's group budget for jiuwei is 2 → a 3rd real device / rotated IPv6 address is refused silently
  (agent logs refusals at debug level only). Relayed connections are also counted twice (entry + landing).

## Fix
- `node.reported_ip_history` (TEXT JSON `[{"ip","at"}]`): every public egress address the agent reported in the
  last 14 days, ≤ 4 per family (newest kept), canonical order; a known address is rewritten at most every 6 h
  (writes bounded; agents report on WS connect, gated 1 min / 30 min already).
- `LandingHostInventory().Nodes` also carries each node's `EgressHistoryIPs` (current reported_ip_v4/v6 + unexpired
  history). `Native` unchanged (dual-install detection = the machines' own addresses only).
- Effect: Custom's relay sources (flvx_node_hosts) and native `relay_sources` include node 48's two egress IPs →
  Helper counts them as relay IPs (slots kept via `max(relayIPs, reserve)`), reports them as relay connections, not
  as online IPs → jiuwei's 总 IP usage drops to his real devices; entry budget at 沪台 rises accordingly.
- Not touched: egress rules / 禁止大陆直连 (relay sources never feed them since plan 219), node addresses, tunnels,
  NodeGuardHosts, mmwx code (no Custom/Helper change needed).

## Risks
- A NAT egress IP shared with strangers (provider NAT pool) is treated as a relay for user-limit accounting only.
- Custom shows the 「同机安装」 chip if a shared server's own address equals an FLVX node's egress IP (none today).
- Reserve may be lenient by a few slots for a node whose IPv6 rotates (history keeps ≤ 4 per family): refusing relayed traffic is worse.
- Inventory grows by ≤ 8 IPs per node; hosts push is hash-deduplicated (one push when an address first appears).

## Checklist
- [x] Diagnose (node 48 / PQS TW logs, Core control snapshot, panel live usage, code path FLVX → Custom → Helper)
- [x] Model column + history merge in FillNodePublicIPs
- [x] Inventory adds egress IPs to Nodes (not Native)
- [x] Relay IP reserve counts each relay node's egress addresses (larger family, ≥ 1) instead of 1 per node
- [x] Unit tests (merge / expiry / bound / inventory / FillNodePublicIPs / relay slots) + `go test ./...` (25 packages ok incl. contract, 0 FAIL)
- [ ] Commit + push branch `feature/3.0.27-relay-egress-ips`, ledger row 230 ready (build-only, next batch)
- [ ] Release check (next batch worker): Custom `flvx_node_hosts` contains 103.246.219.39 + 114.35.6.24 (hosts
      push once); PQS TW Core group `flvx-user-8` `max_inbound_online_ips` = budget + 2 (was 3) while the Core
      still lists both IPs; panel user/list jiuwei `activeClientIps` = real devices only (was 5/5 with 2 phantom)
      and `activeConnections` no longer counts relayed connections twice; 7 landing links + 沪台 rules + 救命版
      unchanged; native landings get one user-limits revision (relay_sources) then stay stable ≥ 30 min.
