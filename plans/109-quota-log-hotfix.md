# 109 — quota-log-hotfix (3.0.27-fork.56)

Branch `maintenance/3.0.27-fork.56-quota-log-hotfix` from prod fork.55 head `c16969d6`.
Trigger: node 48 「Leikwanhost 沪台IPLC」 root fs 100 % (2026-10-19), /var/log 8.6G of
`⚠️ nft quota reconcile failed: conn.Receive: netlink receive: no such file or directory`.

## Root cause (verified on node 48)

- `WebSocketReporter.connect()` calls `initNftablesManager()` on **every** WebSocket (re)connect.
  Each call builds a new `nftables.Manager`; its constructor deletes the `flvx_quota` table,
  clears all DNAT rules and resets `flvx_acct` (clean slate; the panel re-syncs the node's
  forwards after a reconnect). The replaced manager is just dropped: its 1 s quota poll
  goroutine (`startQuotaPollLocked`) is never stopped.
- Node 48 (agent PID 292771 since 10-09, fork.37) logged `nftables manager initialized
  successfully` 117+ times (53 in syslog.2, 63 in syslog.1, 1 in syslog). Forward 126 had an
  nft IP cap (`user-23`, max 2 IPs), so every reconnect while it was nft-mode leaked one poller
  that kept reconciling quota objects the next manager had deleted → ENOENT every second, per
  leaked poller. A failed batch prints 3 lines (`errors.Join` of the 3 netlink acks). Since
  forward 126 moved to gost mode (10-12 20:56) no manager owns `flvx_quota` any more, so all
  leaked pollers fail: ~150 lines/s, 19 % CPU (each poller dumps the whole conntrack table
  every second), journald suppressed ~81k msgs/h, rsyslog wrote it all to syslog + daemon.log.
- Not a kernel/feature problem: conntrack and nft work on 48 (kernel 5.10); the current
  manager's kernel state is consistent (no nft forwards on 48 now).
- The leaked pollers also wrote into same-named sets of a newer manager while the forward was
  still nft-mode, so the IP cap could be corrupted by stale pollers before 10-12.

## Fix

1. `socket/nftables_init_linux.go`: close the previous manager (stop its quota poll and wait for
   it) before creating the new one. Kernel semantics on reconnect stay the same (clean slate +
   panel re-sync).
2. `nftables`: `Manager.Close()`; quota poll with exponential backoff (1 s → 5 min) on
   failure; logs via the shared `logsafe.RoutineLogger` (first failure, ≤1 summary per 10 min,
   one recovery line); on "object missing" (ENOENT) the manager rebuilds all its nft objects
   from its in-memory rules (once per backoff step); if that fails the IP cap is reported as
   NOT enforced in the log (no report field exists for it; no new UI in this hotfix).
3. Audit of periodic loops; same treatment through the shared helper (see list below).
4. Atomic writes (tmp in same dir → fsync → rename) for agent-written config files via one
   shared helper; on failure the old file stays intact and the failure is logged throttled.
5. Tests: logger/backoff with fake clock, quota poll backoff with failing reconcile, atomic
   write keeps old file, netns integration: replaced manager stops polling, self-heal after
   `nft flush ruleset`.

## Checklist

- [x] Root cause verified on node 48 (logs, nft ruleset, code)
- [x] Shared helpers: `logsafe.RoutineLogger` (+ newline collapse), `logsafe.Backoff`, `fsutil.WriteFileAtomic`
- [x] nftables: Close(), poll backoff + throttled logs, self-heal rebuild, unavailable notice
- [x] socket: close old manager on reconnect
- [x] Loop audit fixes (list in final section)
- [x] Atomic config writes (gost.json, config.json, landing, wireguard, baseline, units)
- [x] Unit tests + netns integration tests pass; `go vet`; amd64/arm64 agent builds
- [ ] Commit, push, tag `3.0.27-fork.56`, CI + images green, release Latest verified
- [ ] Prod panel backup + upgrade to fork.56, prune backups
- [ ] Node 48 OTA first: spam stops, gost.json rewritten, forwards healthy, before/after numbers
- [ ] Remove temporary rsyslog filter on 48, /var/log flat for 10 min
- [ ] OTA all other online nodes one at a time (skip 14), compare diagnose
- [ ] Disk sweep on SSH nodes (48 21 30 31 34 35 36 42 45 47 52 53)
- [ ] Plan marked complete, summary written

## Loop audit (agent, go-gost/x)

| Loop | Failure logging before | Change |
|------|------------------------|--------|
| nftables quota poll (1 s) | every failure, 3 lines (joined netlink errors), leaked per reconnect | stopped on reconnect (`Close`); backoff 1 s → 5 min; shared `RoutineLogger` (first / 10-min summary / recovery); errors collapsed to one line; self-heal |
| nftables traffic collection (metric tick) | silent | detects missing accounting chains → self-heal (rate-limited); no new polling |
| gost service accept retry (≤5 s per listener, e.g. EMFILE) | one warning per retry per listener | shared `RoutineLogger` (first / 10-min summary / recovery); retry timing unchanged |
| WS reconnect | already deduped (`log_dedupe.go`) + backoff 2 s → 2 min | unchanged (bounded) |
| Heartbeat / metrics / probes / traffic + config report | already `RoutineLogger` (plan 099) | unchanged |
| landing reconcile (10 s) | silent (status field) | unchanged; note: hashes the Core binary every 10 s and retries downloads every 10 s on failure (follow-up, landing branch owns this code) |
| egress / IPv6 RA (10 min), panelconn self-heal, leikwan detect, stats collectors | silent or ≤1/h | unchanged |

Atomic writes (`x/atomicfile`: tmp in same dir → fsync → rename → dir fsync; existing mode/owner kept; on error the old file stays): gost.json (`saveConfig`), config.json (node_id ×2, protocol/panel IP family settings), landing state files + landing unit, WireGuard conf + forward plan, traffic baseline, xray/sing-box unit, agent unit logging hardening.

Not available: there is no agent → panel status field for "IP cap not enforced" (only the RA-specific `ipv6_ra_status`); the agent logs it clearly instead (no new UI in this hotfix).

## Tests

- `go test` nftables, socket, service, landing, logsafe, atomicfile, panelconn: pass.
- `nftables/netns_integration.sh` (throwaway namespaces): 34 pass, 1 skip (pre-existing `TestIPv6RANetNS`), incl. new `TestNftSelfHealAfterRulesetFlush`, `TestReplacedManagerStopsItsQuotaPoll`.
- `go vet` touched packages + root module; `CGO_ENABLED=0` linux amd64 + arm64 agent builds.
