# 114 — Landing nodes: online IPs (now/limit) + click to see the IPs

User (2026-10-20, after fork.59): in 监控 →「落地」 the IP chip must show 已在线 / 上限 (e.g. `IP 2/3`, `IP 1/∞`),
and clicking it shows which IPs are online right now. Build it and release directly (no screenshot gate).

Base: `maintenance/3.0.27-fork.59-landing-monitor` 206dffac (= prod fork.59).
Branch: `maintenance/3.0.27-fork.60-landing-online-ips`. Release: `3.0.27-fork.60`.

## Data source
The landing Core's control snapshot (`GET /v1/snapshot`, version 7) already carries per inbound
`online_ip_count` + `online_ips: [{ip, connections}]` (and per proxy user `inbound_online_ips`); the agent only
reads `proxy_users[].current_total` today (`go-gost/x/landing/control.go` collect()).

## Contract (shared with mmwx Custom/Helper, task landing-online-ips-mmwx)
Per landing node, next to `connections`:
- `online_ip_count` int — number of distinct client IPs online now.
- `online_ips` [{`ip` string, `connections` int}] — sorted by connections desc then ip, at most 64 entries.
Both omitted (absent) by old agents / old Helpers → panel treats the node as "unknown", never as 0.
Native: in the agent's per-node `Counter` (landing status upload). Shared: in Custom's FLVX status `nodes[]`
(`go-backend/internal/landingcustom/client.go` `NodeStatus`).

## Tasks
- [x] Agent: read online IP count + list per node from the snapshot, add to Counter (omitempty), tests
- [x] Panel: accept fields from native uploads and Custom status; keep latest per node in memory only (no DB writes), drop on delete/TTL like the rate tracker
- [x] API: `/landing/monitor` node `onlineIps` (count, null = unknown); new `POST /landing/monitor/ips {nodeId}` → list, admin or node owner only
- [x] UI: chip `IP 在线/上限` (∞), clickable → IP list (desktop popover, H5 dialog); unknown → `IP -/3` + hint 「节点程序版本过旧，暂不支持在线 IP」
- [x] Tests + gates, screenshots (local lab) for the record
- [ ] Release fork.60, prod panel upgrade, OTA native landing node(s) (node 21) with SSH backup, prod check with real data

## Evidence
- UI: `87c04e8e`; TypeScript/build pass; changed-file eslint 0 errors (2 pre-existing API blank-line warnings). Logs: `/root/flvx-workers/runs/landing-online-ips/logs/`.
- Agent: `8d947e01`; landing tests/vet pass (9 new snapshot cases). Real node 21 snapshot uses per-user `inbound_online_ips` (present null = zero); fallback confirmed from Core source, same snapshot call, no Core restart. JSON per node: 106 B old, 126 B zero, 213 B two IPs, 2,626 B 64 IPv6 IPs.
- Backend: `adfbbb08`; SQLite/PostgreSQL targeted tests and package vet pass; native upload/Custom status, admin/owner/other/no-monitor/revoked grant, unknown/zero/stale/delete/cap covered. Cache for 200 × 64 full IPv6 IPs: 1,006,576 B (0.96 MiB); worst-case fixture counter JSON 4,342 B (+4,251 B). Dedicated temporary PostgreSQL container removed.
- UI record: 12 screenshots in run `screens/`, 32 browser checks pass, no console/page errors; details do not fetch during 11-second monitor refresh observation.
- Full backend `go test ./...`: same 10 top-level failures as fork.59 (`206dffac`), zero new; failure-comparison.json records exact names. Full vet: five existing unkeyed-field warnings in unrelated `leikwan/testapi`, byte-identical on baseline; changed packages clean (out-of-scope file left unchanged).
- Local acceptance: S1–S8/E1–E3/R1 all pass; two real sources 198.51.100.11/.12 each report one connection, monitor count=2.
- Panel rollback ready: `/opt/flvx-svc/rollback/pre-fork60-20261020T095406Z`, validated online DB backup; prune retained pre-fork60 + pre-fork59, removed pre-fork58 and fork.57 image tags.

## Production rollout (Taiwan time, UTC+8)
- [x] Release `3.0.27-fork.60` (`be77e8c6`), CI `39404313977` + Images `39404316148` green; Latest, same 10 assets, scripts/checksums/dual-arch images validated (`release-validation.json`).
- [x] Panel upgraded 2026-10-20 18:16; healthy, 29/29 online and fresh reports; counts and landing server revisions/hash unchanged, agent desired hash unchanged.
- [x] Only native agent node 21 OTA'd 18:17; SSH backup `/root/flvxx-backup-20261020T101637Z-pre-fork60`, checksum verified, restore script included; two newest node backups retained. Reconnected in 6.1 s, Core PID 29900 unchanged, diagnostics 27/27 before/after; other node versions unchanged.
- [x] Resource windows: before agent 58,748 KiB / 0.336% CPU (35.72 s), after 54,988 KiB / 0.279% (46.63 s); Core 33,800 → 33,536 KiB, 0.084% → 0.064%, same PID.
- [x] Production acceptance: node 21 + shared server 7: P0–P9/PC, 11 scenarios / 74 checks pass; temporary user 37 and all related rows removed, existing server/user/node credential hashes unchanged (`logs/prod-server7.{log,json}`). Native SS2022/VLESS report real IP 104.145.236.29 with count 1 and positive connections. Earlier three runner attempts and successful cleanup retained; script now waits for fresh IP samples/full Helper cycle and restores the original temporary-node limit.
- [ ] Production acceptance: node 21 + shared server 5 (temporary user fully cleaned)
