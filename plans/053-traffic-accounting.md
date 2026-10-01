# 053 - Traffic accounting fixes (3.0.27-fork.8)

## Problem
Users asked whether per-rule (forward) traffic and the user-list totals are accurate.
They are not:

1. **Billing mode is applied twice.** `scaleFlowByTunnel` multiplies both directions by
   `tunnel.flow`, so 双向 (flow=2) counts (up+down)x2 and 单向 (flow=1) counts up+down.
   Every production tunnel is flow=2, so every forward/user/user_tunnel/quota number grows
   at twice the real traffic.
2. **nftables forwards count almost nothing.** The per-forward counter (and the `limit`
   speed limit) sit in the `prerouting` NAT chain, which only sees the first packet of each
   conntrack connection.
3. **Agent loses bytes when it resets service stats.** `observeStats` reads the counters,
   then `ResetTraffic` stores `current - reported` (Load then Store); bytes added in between
   are lost.
4. **Panel silently drops traffic.** `/flow/upload` answers `ok` even when the body cannot be
   decrypted/parsed or the DB write fails, so the agent discards those bytes.
5. **Stale ids in service names.** The user id / user_tunnel id embedded in the service name
   are trusted; after a tunnel move or user_tunnel re-creation (group revoke/re-grant) on an
   unsynced node, user_tunnel stats and tunnel quota/expiry enforcement silently miss.

Related problems found while verifying:
- `enforceForwardTrafficLimit` adds the just-written delta a second time.
- `pauseForward` (forward traffic limit) deletes `{fid}_{uid}_0_*` services, which never
  exist for normal users, so a forward over its traffic limit keeps running; nftables
  forwards are not handled at all.
- nftables deletion: `DeleteRule` looks up the port after removing the map entry and then
  deletes the first rule with that protocol (any forward); `DeleteRuleWithPort` deletes
  kernel rules by port even when the port belongs to another forward on this node, and
  leaves the old rule behind when a forward's port changed.

## Decisions
- 双向 (flow=2): in = upload x ratio, out = download x ratio (no x2).
- 单向 (flow=1): only the larger direction is billed, max(upload, download) x ratio, per
  report; it is recorded in that direction's column (upload -> in_flow, download -> out_flow,
  tie -> in_flow) and the other column gets 0. Any other flow value is treated as 双向.
- Historical counters are not touched; the fix applies to new traffic only.
- Peer-share (federation) flow stays raw upload+download (share quota, no tunnel billing);
  tunnel metrics stay raw bytes (monitoring).
- Old agents keep the 50/50 nftables split; with 单向 tunnels that bills half of the
  nftables total until the node agent is upgraded (no production tunnel uses 单向).

## Tasks
- [x] Billing: `billTunnelFlow` with the new 单向/双向 semantics + unit tests
- [x] Flow ingestion: resolve forward owner/tunnel/user_tunnel from the DB (fallback to the
  parsed ids only when the forward no longer exists and the name is not a federation
  runtime of another panel; deleted users are not billed), apply a whole upload batch in one
  transaction, enforcement after commit, tunnel metrics after commit
- [x] `/flow/upload`: non-`ok` for decrypt/parse/DB failures (unknown secret still `ok`);
  de-duplicate identical encrypted bodies per node (resend after timeout); answer before the
  (serialized) enforcement runs so slow node commands cannot push the agent past its 5s
  timeout and make it resend committed bytes
- [x] Forward traffic limit: no double-added delta; pause via the standard pause path
- [x] Agent stats: atomic subtract of the reported bytes + race test; hand the last period's
  bytes over when a service stops (Serve cancels the observer after its connections ended)
- [x] Agent traffic manager: resend the same pending body until acknowledged (bounded)
- [ ] nftables: count in filter chains (forward/input/output -> `accounting`) by conntrack
  original dst port + direction (upload/download), speed limit as a bytes policer there,
  generation-tagged rules, final counters harvested on delete, safe deletion
- [ ] Reporter: per-direction deltas keyed by rule generation
- [ ] Frontend labels for 单向/双向
- [ ] Backend/agent unit + contract tests, netns nftables integration tests, frontend build;
  compare failures with the fork.7 baseline
- [ ] Local end-to-end check (panel on :16365 + agent in network namespaces) for gost and
  nftables forwards against known transfer sizes
- [ ] Release `3.0.27-fork.8` and verify CI + assets
- [ ] Upgrade the production panel with a validated rollback point; verify `/flow/upload`
  ingestion at the real rate (no x2)
- [ ] Agent rollout per precedent (single-node canary) and document how to upgrade the rest
