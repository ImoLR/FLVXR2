# 090 — Tunnel transport protocol default TCP → TLS (fork.41)

## Problem
- The backend already treats an empty chain/hop/exit protocol as `tls`
  (`repository_control.go` ChainNodeRecord fallback, `model.ChainTunnel.Protocol`
  default `'tls'`, every `defaultString(..., "tls")` in `mutations.go` tunnel
  create/update/runtime builders, federation runtime apply).
- The tunnel form in `vite-frontend/src/pages/tunnel.tsx` pre-fills and falls back to
  `"tcp"` for the chain hop and exit node transport protocol, so every tunnel created
  in the UI is stored and deployed as TCP.
- Prod (2026-10-11): all 63 chain_type 2 rows and 53 chain_type 3 rows are `tcp`.

## Scope
- Only the tunnel transport protocol (chain_type 2 hop listener + the dialer the
  previous node uses towards it; chain_type 3 exit listener + the dialers towards it).
- chain_type 1 (entry) rows: protocol is not used for dialing (entry dials with the
  next group's protocol); already `tls` in prod, left as is.
- NOT touched: entry protocol filter (`tunnel.http/tls/socks/block_other`,
  meta `protocolFilter.*`), forward rules, users, install scripts, other stacks.

## Code findings
- Dial protocol: `buildTunnelChainConfig` → `dialer.type = target.Protocol` (targets =
  next hop group or exit group); listener: `buildTunnelChainServiceConfig` →
  `listener.type = chainNode.Protocol` for chain_type 2/3.
- Side effect of exit protocol `tls`: `isTunnelSelectedTLSProtocol` (first chain_type 3
  row) → forward services' UDP listener metadata `ttl: 10s` (gost default 5 s).
  Type 1 tunnels already get this (no exit row → "" → tls). Applied by the forward
  part of the panel redeploy.
- Other `"tcp"` literals checked and NOT tunnel transport: federation forward
  runtime (`federation.go` Role "forward"), remote type-1 entry `targetProto`
  (`mutations.go` ~1266), node-deploy transport, service monitors, health checks.

## Verification (Part A)
- tunnel.tsx: 10 literals on 7 code spots changed (`syncChainGroupNodes` hop default,
  hop card display fallback, 「再加一跳」/「添加一跳」 new hop, exit node pick default,
  exit protocol select display, exit strategy change placeholder). Select options
  (TCP/MTCP/TLS/MTLS/WSS/MWSS) unchanged. No other frontend tunnel-create path
  (`createTunnel` has one caller); backend create/update/runtime already default `tls`.
- Build: `tsc && vite build` OK, main JS 2,746,443 B (< 5 MiB), `dist/version.json` emitted.
- go test ./...: 15 failures, identical set to fork.40 (no new failures).
- Playwright (local paneld 16365 + static server 18190, faked node list, create/update
  intercepted): new build 16/16; unmodified fork.40 build 9/16 (the 7 TLS-default checks
  fail with `tcp`). Edit mode keeps stored `tcp`/`mtcp`; filter values unchanged.
  Script + results: /root/flvx-workers/runs/tunnel-tls-default/e2e/.

## Checklist
- [x] Frontend: tunnel.tsx transport default/fallback `"tcp"` → `"tls"` (7 spots)
- [x] Grep frontend + backend tunnel create/update paths for other TCP chain defaults
- [x] Frontend build (tsc + vite build), main JS < 5 MiB
- [x] go-backend `go test ./...` vs baseline 15
- [x] UI check: new tunnel form shows TLS for hop + exit, create payload sends `tls`
- [ ] Commit, push, tag 3.0.27-fork.41, CI + release assets verified
- [ ] Prod backup + prune + upgrade /opt/flvx-svc, health + node state
- [ ] Part B: backup + before-dump (chain_tunnel, tunnel filter cols) + undo SQL
- [ ] Part B: UPDATE chain_type 2/3 non-tls → tls
- [ ] Part B: sequential batch-redeploy per tunnel, record results
- [ ] Part B: verify DB, filter after-dump identical, diagnose, 2 node spot-checks,
      node_metric advancing, redeploy errors only node 24
- [ ] Plan marked complete
