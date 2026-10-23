# 139 — native landing ports + 出站 label + shared delete stuck + 双向 hint + H5 pull-to-refresh (fork.80)

Base: `origin/maintenance/3.0.27-fork.79-reconnect-stability` (bc65d006, fork.79 = prod).
Branch: `maintenance/3.0.27-fork.80-native-landing-ports`. Panel only, no agent change, no OTA.

## Checklist
- [x] A1 range-spec helpers (subtract / add / format) + unit tests
- [x] A2 enable native landing carves landing range out of node rule range (conflict refusal)
- [x] A3 save native landing server: carve new range, give back old ports (rule below), conflict refusal
- [x] A4 disable (status → disabled) gives the range back; re-enable carves again
- [x] A5 node edit rejects rule range overlapping the active native landing range (message names the range)
- [x] A6 `landingFreePort` error explains the concrete cause
- [x] A7 item 5: OS-level bind conflict investigation + outcome
- [x] B  出站 label follows the layered default (shared helper with CompileLayers) + tests
- [x] C  shared landing delete stuck (node 103): cause + bounded finalize + totals ignore removed nodes + tests
- [x] D  双向 quota hint text
- [x] E  H5 pull-to-refresh: landing page hook + global fallback/cap + hook awaits callback; Playwright check
- [x] F  backend tests (full + contract SQLite/PG), frontend tsc/build/lint
- [ ] G  commit, push, CI, tag fork.80, release assets
- [ ] H  prod backup + upgrade + verify (nodes, counts, labels, node 103 finalized, node 105 untouched)
- [ ] I  summary + plan complete

## Notes

### A — native landing range vs node rule range
- Cause: `landingFreePort` marks the node's whole rule range (`node.port`) as used; enable/save never carved the
  landing range out, so `10000-65535` swallowed `30000-39999` (node 55). Node 21 only worked because it was split by hand.
- New `repository_landing_ports.go`: span helpers (`parsePortSpans`/`subtractPortSpan`/`formatPortSpans`) and
  `applyNativeLandingRange` (inside the enable/save transaction).
  - Enable (new server, or chain-only exit made public) and save (range move/shrink, disable, re-enable) carve the
    active landing range out of the rule spec, keeping the `a-b,c-d` style. Unchanged ports keep the admin's spelling.
  - Give-back rule (nothing new stored): ports leaving the landing range return to the rule spec only if they lie in
    the hull [min, max] of the current rule spec and no landing node / chain node / Reality guard holds them.
    Middle carve (the normal case) comes back whole; an edge carve (rule 10000-65535, landing 60000-65535 →
    10000-59999) is not re-added automatically — the panel cannot tell it from a landing range that never was part
    of the rule range; the admin can widen the rule range by hand (now allowed since the landing no longer reserves it).
  - Disable = `status=disabled` via `/landing/server/save` (the row and its landing nodes stay; there is no native
    server delete except node delete). Disable gives the range back minus ports still held by its landing nodes, so
    forwards can never get a port a (disabled) landing node still owns; re-enable carves again.
  - Conflict refusal: forwards (`forward_port`), tunnel listeners (`chain_tunnel`), deployed inbounds and runtime
    resources on the node inside the new range that would be newly reserved or carved → Chinese error with up to 5
    ports + total; nothing moves. Chain-only exits are left alone (they never reserved a public range).
- Node edit (`ValidateNodeLandingPortRange`): a rule range overlapping the active native landing range is refused:
  「端口 30001-39999 已留给落地，请从规则端口段里排除（例如改成 10000-30000,40000-65535）」. The node page shows backend
  `msg` via toast (verified in `pages/node.tsx`). Enable toast now says which ports left the rule range.
- `landingFreePort` errors now say why: whole range inside node 「X」's rule spec / partly rule spec + N taken /
  all taken by listeners / shared machine busy.
- Prod data is NOT rewritten at startup/migration: node 21 `10000-29999,31000-65535` vs 30000-30999 and node 55
  `10000-30000,40000-65535` vs 30001-39999 are already disjoint (read-only check before release).

### A7 — item 5: port busy at the OS level (another program)
- Agent (`go-gost/x/landing/control.go`): reconcile adds inbounds with the Core CLI `api adi`; on a bind error the CLI
  fails and the agent returns `landing gRPC adi failed` (stderr, port and reason dropped) and **stops the reconcile at
  the first failing inbound** — so one busy port also keeps later new inbounds of the same server from being added,
  retried every 10 s tick. The agent status carries `state=error` + that text.
- Panel: `syncLandingServer` only logged `landing desired sync pending` and the create call still returned OK; the
  node shows active although nothing listens. No panel-only fix is possible that knows *which* port failed or *why*
  (bind vs other Core errors), so no auto-reassign in this release. Panel change: the existing log line now carries
  the agent's short reason (same number of lines).
- Follow-up proposal (needs an agent release): agent probes `net.Listen` tcp+udp on the port before `adi`, skips a
  failing inbound and continues with the rest, reports per-node `{id, error:"port_in_use"}`; panel then marks a
  never-used node (no counters, no forwards) and moves it to the next free port + redeploys, otherwise shows
  「端口被其他程序占用」 on the node.

### B — 出站 label ignores 全局默认出站
- Cause: `landingegress.Label` returned 「直连」 for profile 0 and the views never looked at the global layer.
- `compile.go`: CompileLayers' global layer, node layer and default precedence moved into `globalLayer`/`nodeLayer`/
  `nodeDefault`; new `CompileInput.EffectiveDefault`/`EffectiveLabel` use the same functions (node default > profile
  default > 全局默认出站, IP-family fallback to 直连, layers only when the server applies them). Enabled global
  forced/default rules, node rules or profile rules → 「按规则分流」; built-in SMTP/BT switches do not count.
- Handler: catalog builds a per-server label input with the same `landingEgressSupport`/`landingEgressInput` as the
  real compile; global settings + native node facts are read once per request (no N+1). Server/node `egressLabel`,
  admin egress view label/profileName, node `effectiveRouteProfileName` (「默认 · 直连（仅 IPv4）」) and the node rules
  dialog's 「跟随上级」 all use it. Frontend default option text 「默认（直连）」 → 「默认（跟随全局默认出站）」.
- fork.81 follow-up (found on prod after fork.80): prod has 4 enabled 全局·强制 **block** rules (发信端口, Telnet/SMB,
  大陆 IP, 大陆网站), so fork.80 showed 「按规则分流」 everywhere. Block-only rules only filter — allowed traffic still
  leaves through the default — so only rules that route to an outbound count as 分流 now (global, node and profile
  rules alike; a profile 「默认代理 + 屏蔽 BT」 now reads 「代理」 instead of 「按规则分流」).

### C — shared landing delete stuck (node 103)
- Read-only Custom status of share 16 (10-23 19:0xZ): node 103 `state=removed, connections=29`, up/down bytes frozen
  (909 122 / 1 001 491, same for hours), **no online IPs**; node 105 active with 2 online IPs. Other removed nodes
  report 0. → the 29 is a stale per-node connection count the mmwx Custom/Helper keeps reporting for a removed inbound,
  not live sockets (idle conns would have timed out; no bytes move). mmwx-side follow-up needed (reset/omit removed
  nodes' connections).
- Panel: `sharedRemovedSettled` — removed + applied ≥ pushed (unchanged rule) and connections 0 **or** 3 min since the
  deletion request (`updated_time`); the poll right before finalizing books the final counters. Never-applied and
  forward-referenced nodes behave as before. Same helper for the expired-node lifecycle path. `LandingLiveConnections`
  makes removed nodes count 0 in server totals, monitor totals and node views.

### D — 双向 hint
- Texts only: user quota header 「限额 (GB，计费流量)」 + hint line; node 流量上限 label/description; server defaults
  labels + one hint line.

### E — H5 pull-to-refresh
- Cause: the global spinner only stopped on `flvx:pulltorefresh:done`, which only hook users send; 落地 had no hook.
- Fix: `usePullToRefresh` counts itself into the event detail synchronously and sends `done` only after its (async)
  callback finished or failed; the global spinner waits for all claimers, stops after 300 ms when nobody claims and
  always after 8 s. 落地 page now uses the hook (servers/nodes tabs reload `load`, the 出站与路由 tab remounts).
- H5 pages with a handler: dashboard, monitor, forward, tunnel, landing (new), node, user, group, limit,
  panel-sharing, tz. Without (short 0.3 s spin, no reload): leikwan, tls, profile, config, shop, myhome,
  admin/plans, admin/orders, admin/payment, change-password.
- Playwright (headless Chromium, 390×844 touch via CDP, lab panel :16781/:16782, API delayed 1.2 s after the pull):
  admin 落地 / user 落地 / admin 节点: spinner visible ~1.3 s, list requests fired, spinner gone; /profile: 290 ms.

### F — tests
- Backend full suite (`go test ./...`, SQLite + PostgreSQL via FLVX_POSTGRES_TEST_DSN): same 15 pre-existing failures
  as fork.79 (backup export/import, federation dual panel, legacy migrations, renewal, service monitor, address-in-use
  contract); one new failure `TestLandingTargetListQueryCount` = the expected per-request query count (+1 global
  settings read, +1 native node facts read, constant for 1 and 6 nodes) → expectation updated; rerun of repo/handler/
  landingegress packages: only the baseline failures.
- New tests: `TestLandingRuleSpecMath` (16 cases), `TestNativeLandingRangeCarveAndGiveBack` (sqlite+pg),
  `TestNativeLandingEnableRefusesUsedPorts`, `TestLandingNoFreePortMessage`, `TestEffectiveLabelPrecedence` (19 cases),
  `TestEffectiveDefaultMatchesPlan`, `TestLandingLabelsFollowGlobalDefault` (handler, admin+user, IPv6-only fallback),
  `TestSharedLandingFinalizeStaleConnections` (node 103 exact state, sqlite+pg).
- Frontend: tsc 0 errors, `npm run build` OK, eslint on changed files 0 errors (remaining warnings on untouched lines).
- Lab API smoke (node 10000-65535): enable → `10000-29999,40000-65535`, first node port 30000 path, node edit refused
  with the range message, shrink/disable/enable/move give-back as designed, conflict refusal lists the port.
