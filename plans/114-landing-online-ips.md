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
- [ ] Panel: accept fields from native uploads and Custom status; keep latest per node in memory only (no DB writes), drop on delete/TTL like the rate tracker
- [ ] API: `/landing/monitor` node `onlineIps` (count, null = unknown); new `POST /landing/monitor/ips {nodeId}` → list, admin or node owner only
- [x] UI: chip `IP 在线/上限` (∞), clickable → IP list (desktop popover, H5 dialog); unknown → `IP -/3` + hint 「节点程序版本过旧，暂不支持在线 IP」
- [ ] Tests + gates, screenshots (local lab) for the record
- [ ] Release fork.60, prod panel upgrade, OTA native landing node(s) (node 21) with SSH backup, prod check with real data

## Evidence
- UI: `87c04e8e`; TypeScript/build pass; changed-file eslint 0 errors (2 pre-existing API blank-line warnings). Logs: `/root/flvx-workers/runs/landing-online-ips/logs/`.
- Agent: `8d947e01`; landing tests/vet pass (9 new snapshot cases). Real node 21 snapshot uses per-user `inbound_online_ips` (present null = zero); fallback confirmed from Core source, same snapshot call, no Core restart. JSON per node: 106 B old, 126 B zero, 213 B two IPs, 2,626 B 64 IPv6 IPs.
