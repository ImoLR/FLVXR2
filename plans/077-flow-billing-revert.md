# 077 - Restore the original tunnel billing formula (3.0.27-fork.29)

## Problem
Plan 053 (fork.8) changed tunnel billing to 双向 = (upload+download) x ratio and
单向 = max(upload, download) x ratio. The user decided (2026-10-08) that this was a mistake:
billing must go back to the original panel formula.

## Decisions
- Billing per flow report (original `scaleFlowByTunnel` from fork.7):
  `in = int64(upload x ratio) x flow`, `out = int64(download x ratio) x flow`, i.e.
  单向 (flow=1) = (upload+download) x ratio, 双向 (flow=2) = (upload+download) x ratio x 2.
  Any other positive flow value is used as the multiplier (as in fork.7); flow <= 0 and
  ratio <= 0 are treated as 1. No tunnel (deleted forward without a resolvable tunnel) =
  raw bytes x 1, as in fork.7.
- Only the formula changes. All other plan 053 fixes stay (DB owner resolution, atomic
  batches, non-`ok` on failures, no double-added forward limit delta, agent/nftables counting,
  user reset linkage). User quota, forward limit, user_tunnel and forward counters all use the
  billed values, as before.
- Historical counters are not touched; the formula applies to new traffic only.
- Production (2026-10-08): 53 tunnels, all flow=2 ratio=1, so billed traffic goes back to
  2x the raw bytes.

## Tasks
- [x] Release gate: fork.28 is origin-tagged, published Latest, and running in production;
  rebase onto final fork.28 head `2b46d3dc` and push with force-with-lease (no conflicts).
- [x] `billTunnelFlow`: original formula + unit tests; ingestion/regression tests updated
- [x] UI labels (tunnel form, WG path form, dashboard badge tooltip) and usage docs
- [x] Traffic limit fields labelled 双向 (user request 2026-10-08): user form
  「流量限制(GB，双向)」 + formula description, user list column / mobile card
  「流量限制(双向)」 with tooltip, rule form 「流量控制（双向）」 + formula description
- [ ] go-backend `go test ./...` baseline comparison, frontend build, CI green
- [ ] Release `3.0.27-fork.29` on top of the released fork.28, prod backup + upgrade,
  verify billed/raw ratio 2.000 on prod (forward/user growth vs raw `tunnel_metric`)
