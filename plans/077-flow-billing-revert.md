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
- [x] go-backend `go test ./...` baseline comparison, frontend build, CI green
- [x] Release `3.0.27-fork.29` and verify published assets.
- [x] Create validated production backup and prune to the newest two backups.
- [ ] Upgrade production backend/frontend to fork.29.
- [ ] Verify production health, APIs, served labels, and billed/raw ratio 2.000
  (forward/user/user_tunnel growth vs raw `tunnel_metric` over at least 180 seconds).

## Release validation (2026-10-08)
- Rebased code: `60caa2ea`, based on final fork.28 head `2b46d3dc`.
- Backend full suite: `GOMAXPROCS=2 go test -p 1 ./...`; exactly the same 15
  failing tests/subtests as `/root/flvx-workers/runs/monitor-render/baseline.json`;
  no new or missing failures. Billing/ingestion/regression targeted run: 19 tests passed.
- Local frontend `npm run build`: passed (MemAvailable 1,847,780 KiB, no competing
  heavy process at start); main JS 2,755,019 bytes, PWA precache 2,891.51 KiB.
- CI Build Check on `7262c616`: passed, run [37775718199](https://github.com/ImoLR/FLVXR2/actions/runs/37775718199).
- Evidence: `/root/flvx-workers/runs/fork29-flow-billing/`.

## Release published
- Annotated tag `3.0.27-fork.29` targets `0393ccf5`; tag-head CI
  [37776147343](https://github.com/ImoLR/FLVXR2/actions/runs/37776147343) passed.
- Build and Push Images [37776377406](https://github.com/ImoLR/FLVXR2/actions/runs/37776377406): all required jobs passed.
- [Release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.29) published
  2026-10-08 12:40:08 UTC, Latest, non-prerelease; same 10 assets as fork.28.
- v4/v6 compose images both pin `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.29`.
  Both install scripts pin fork.29 and `REPO=ImoLR/FLVXR2`; AMD64/ARM64 GOST SHA256 verified.

## Production rollback point
- `/opt/flvx-svc/rollback/pre-fork29-20261008T124111Z/`: compose, .env,
  Python sqlite3 online backup `gost.db.validated`, `validation.json` and
  `ROLLBACK-METADATA.md`; quick_check=ok, all table counts recorded
  (user=10, node=28, tunnel=53, forward=25, user_tunnel=153).
- Current fork.28 images retained as `local/flvxx-{backend,frontend}:pre-fork29-20261008T124111Z`.
- Required prune retained pre-fork29 and pre-fork28; removed pre-fork27 backup
  and its local tags plus unused fork.26 images. No scratch production DB copies created.
- Normal rollback: restore saved compose/.env, retag the saved local images to fork.28,
  then `docker compose up -d --pull never backend frontend`; leave live database intact.
