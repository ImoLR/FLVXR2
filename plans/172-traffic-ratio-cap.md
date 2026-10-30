# 172 — Tunnel billing multiplier bounds (traffic-ratio-cap)

Branch `feature/3.0.27-traffic-ratio-cap` from tag `3.0.27-fork.98`. BUILD ONLY — ships in the next batch release
(no tag / release / deploy / OTA from this plan).

## Why
During the fork.98 release test a temp tunnel was created via the API with `flow: 99999`; tens of MB were billed as
3.5 TB and the rules were auto-paused. The UI cannot enter such values, the API accepted anything.

## Billing fields (confirmed)
`billTunnelFlow` (handler/flow_ingest.go): billed = bytes × `traffic_ratio` × `flow`.
- `tunnel.flow` = 流量计算 mode, also the multiplier: UI select 1 (单向) / 2 (双向).
- `tunnel.traffic_ratio` = 流量倍率: UI input min 0.01, max 100.
- WG path (`path_tunnel.flow` / `traffic_ratio`): same UI select, ratio input min 0.01, no max → use the same 100.

## Range (now enforced server-side)
- `flow` ∈ {1, 2}
- `trafficRatio` finite, 0.01 ≤ ratio ≤ 100
- Absent/null field keeps today's default (1 / 1.0). Stored values and billing are untouched.

## Tasks
- [x] Confirm the multiplier fields (`flow` 99999 in release-batch-1031 + `traffic_ratio`) and UI ranges
- [x] Read-only prod check (copy of `/opt/flvx-svc` sqlite): 42 tunnels, flow ∈ {1,2}, ratio all 1.0; 0 WG paths → nothing out of range
- [x] Shared validator `repo.ValidateTunnelFlowMode` / `repo.ValidateTunnelTrafficRatio`
- [x] `/tunnel/create` + `/tunnel/update` (the entry split twin goes through `/tunnel/create`)
- [x] WG path `/path/create` + `/path/update` (0 still means "unset → 1" since the body is a struct)
- [x] Backup import: reject out-of-range values (0 accepted as legacy "unset", billing already treats it as 1) — raw `/backup/import` pre-check on `tunnel` + `path_tunnel` rows (`checkTunnelImportBilling`, no DB access) and typed `importTunnels`
- [x] Unit + contract tests (edge values accepted, 0/negative/too large/NaN rejected; create + update)
- [x] `go test ./...` in go-backend green
- [x] Existing contract tests that posted `flow: 99999` to `/tunnel/create|update` (root of the copied proof-script habit) → `flow: 1`
- [x] Commit + push, CI green — code commit 1b7421ad, CI Build Check 41134484437 success (backend, PostgreSQL contract, agent, frontend)

## Resource efficiency
Pure input validation on already-decoded request fields: no new DB queries, no new goroutines, no extra memory per request.
