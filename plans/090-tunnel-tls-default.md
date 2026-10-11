# 090 — Tunnel transport protocol default TCP → TLS (fork.41)

Status: DONE 2026-10-11 (prod on 3.0.27-fork.41, all 48 tunnels converted to tls).

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

## Release / upgrade (Part A)
- Commit 6be6e8ac, tag 3.0.27-fork.41 on 6be6e8ac; CI Build Check 38117020659 ok,
  Build and Push Images 38117021757 ok; release Latest, not prerelease, same 10 assets as
  fork.40, compose images `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.41`, PINNED_VERSION +
  REPO=ImoLR/FLVXR2 in both scripts, gost-amd64 sha256 matches.
- Prod: rollback dir `/opt/flvx-svc/rollback/pre-fork41-20261011T062508Z` (quick_check ok,
  local tags = fork.40 images), prune removed pre-fork39 + fork.38 images; upgraded
  06:25:23Z; backend healthy, version.json `3.0.27-fork.41-mv3fbntq`, served bundle has the
  `tls` fallbacks; 26/27 nodes online (24 offline), node_metric ≤5 s old, 26 reconnect
  redeploys all `failed=0`.

## Prod conversion (Part B, 2026-10-11 06:26–06:36Z)
- Rollback dir `/opt/flvx-svc/rollback/pre-tunneltls-20261011T062620Z`: gost.db.validated
  (quick_check ok), compose/.env, local tags = fork.41 images, `chain_tunnel-before.json`,
  `tunnel-filter-before.json`, `summary-before.json` (sha256s), `undo.sql`, `convert-log.jsonl`.
  Prune then removed pre-fork40 (kept: pre-tunneltls + pre-fork41).
- Before: chain_type 1 = 65 tls, 2 = 63 tcp, 3 = 53 tcp. Diagnose (fork.40) 37/48 tunnels
  clean, 11 fail only on the offline entry 24. External TLS probe: 0/116 listeners TLS
  (88 TCP reset, 28 unreachable from this host = nodes 12/26).
- Per tunnel (`partB/convert.py`): UPDATE that tunnel's tcp rows → tls, panel
  `POST /tunnel/batch-redeploy {"ids":[id]}`, diagnose, TLS probe; stop on anything
  not caused by node 24. Canary 3 → 5 → 74, then the rest. 48 tunnels / 116 rows,
  redeploy 1.0–5.2 s each (99 s total). Stops: tunnel 62 + 65, first TLS handshake to
  node 15 (hop listener) timed out right after its redeploy; re-probes 3× OK
  (TLS 1.3, ~0.23 s) → probe got 3 retries, resumed.
- Redeploy failures: only the 11 tunnels with entry 24 (`入口节点 … (24) 清理转发链失败:
  节点不在线`), every hop/exit succeeded; reconnect will redeploy 24 from the DB.
- After: chain_type 2 = 63 tls, 3 = 53 tls; exactly the 116 tcp rows changed, nothing
  else in chain_tunnel; `tunnel-filter-before/after.json` byte-identical (sha256 dbe0855e…);
  sha256 of tunnel static cols, forward static cols, forward_port, user unchanged.
  Diagnose 37/48 clean, 11 fail only on entry 24 (= before). TLS probe 88/88 reachable
  listeners on 10 nodes answer TLS 1.3 (12/26 unreachable from here, diagnose OK).
  End-to-end: idle connect to forward 109 (entry 49 → exit 47, tunnel 74) and 111
  (entry 50 → exit 47, tunnel 77) held open = entry TLS dial + relay to target OK.
  node_metric ≤5 s for all 26 online nodes; backend log: only the 11 node-24 errors.
  Real traffic: forward 65 (tunnel 64) kept counting bytes after its conversion; overall
  tunnel traffic was near zero at that hour (also before the conversion).
- Side effect: exit protocol tls → forward services' UDP listener `ttl: 10s` (was gost
  default 5 s), applied by the same redeploy.

## Checklist
- [x] Frontend: tunnel.tsx transport default/fallback `"tcp"` → `"tls"` (7 spots)
- [x] Grep frontend + backend tunnel create/update paths for other TCP chain defaults
- [x] Frontend build (tsc + vite build), main JS < 5 MiB
- [x] go-backend `go test ./...` vs baseline 15
- [x] UI check: new tunnel form shows TLS for hop + exit, create payload sends `tls`
- [x] Commit, push, tag 3.0.27-fork.41, CI + release assets verified
- [x] Prod backup + prune + upgrade /opt/flvx-svc, health + node state
- [x] Part B: backup + before-dump (chain_tunnel, tunnel filter cols) + undo SQL
- [x] Part B: UPDATE chain_type 2/3 non-tls → tls
- [x] Part B: sequential batch-redeploy per tunnel, record results
- [x] Part B: verify DB, filter after-dump identical, diagnose, 2 node spot-checks,
      node_metric advancing, redeploy errors only node 24
- [x] Plan marked complete
