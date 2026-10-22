# 133 — QA sweep (report only, no fixes, no release)

Branch `qa/3.0.27-qa-sweep` (= fork.71 + 132 + 131 + 134; prod = fork.73 = fork.71 + 132 + 131).
Run dir `/root/flvx-workers/runs/qa-sweep/` (findings.md, final.md, summary.md, evidence/, screens/).
Gallery: `http://104.145.236.29:8791/<token>/qa-sweep/index.html` (→ run screens dir).

Rules: prod strictly read-only (GET only, sqlite `mode=ro`); writes only in the local lab (neutralised copy
of the prod DB in a netns without internet). No product code changes. Lab/test scripts live under
`scripts/qa-sweep/`.

**Status: STOPPED EARLY on a prod P0 (as the brief requires).** P0-1: the unauthenticated public WebSocket
`/system-info?type=2` (and every logged-in non-admin session) receives all node metrics incl. per-rule
`forward_metrics` (user_id/port/speed) and every agent command reply (TcpPing ip:port of hops/exits every
minute, landing online client IPs, client-IP lists) — confirmed on prod fork.73 with a read-only listener.

## Checklist

- [x] Read previous final reports (130, 132, 131, 134) + memory
- [x] Static: enumerate router routes (259 in Register + 45 map-registered), classify admin-only vs user
- [x] Lab: build paneld + frontend from branch, neutralised copy of prod DB, prove no outbound calls (netns + nft drop counters + DNS sink)
- [x] Authz sweep: all 304 routes as anon / non-admin / disabled user → every admin route 403
- [x] Disabled/expired user's old JWT: reads still allowed (F03), writes refused by per-handler checks
- [x] Real traffic in lab: `scripts/landing-acceptance` full matrix on the branch, 19/19 scenario groups pass
- [x] UI tour pass 1: 89 page views (admin, user w/ tunnels, landing-only user, no-grant, disabled+expired, over-quota; desktop/H5; light/dark)
- [x] Prod read-only: WebSocket GET listener (redacted) → **P0-1**; DB `mode=ro` checks (metrics size, flow buckets)
- [x] Findings + gallery index + final.md + summary.md
- [x] Cleanup lab processes, netns, temp DB copies; record lab RAM
- [ ] NOT DONE (stopped at P0): dialog click-through / invalid input / double submit / two tabs / throttled network
- [ ] NOT DONE: 2-min polling counts per page (admin + user)
- [ ] NOT DONE: node/tunnel/rule write flows in the UI, diagnose/redeploy/OTA UI, multi-entry IP, LeiKwan pipes
- [ ] NOT DONE: chain_lab (线路机) run; multi-namespace gost tunnel entry→exit→landing traffic
- [ ] NOT DONE: 监控 / 落地 numbers vs DB item-by-item; links/QR parse with real clients for every protocol

## Findings

Full detail: `/root/flvx-workers/runs/qa-sweep/findings.md`.

| id | sev | area | who | prod? | one line |
|----|-----|------|-----|-------|----------|
| P0-1 | P0 | ws / monitor | anon + all users | yes | public WebSocket streams per-rule metrics, hop/exit IP:port (TcpPing replies), landing client IPs, all agent replies; not gated by `login_monitor_link` |
| F01 | P2 | 主页 | expired user | yes | expired account shows 「-2 天后到期」 |
| F02 | P2 | config | anon | yes | `/config/list` returns the whole vite_config to anonymous callers |
| F03 | P2 | auth | disabled user | yes | disabled user's JWT stays valid 90 days; read APIs (incl. landing links) keep working |
| F04 | P3 | resource | admin | yes | GitHub releases API called uncached on every admin page load |
| F05 | P3 | 主页 | all | yes | dashboard sends quota/history with userId 0 first on every load |
| F06 | P3 | 规则 | user | yes | user rule page calls admin-only speed-limit/list + federation/share/list (403 each load) |
| F07 | P3 | 主页 | over-limit user | yes | 12/10 rules shows 「100.0%」 with no over-limit hint |
| F08 | P3 | resource | — | yes | node_metric 1 row/s/node = ~355 MB of the 403 MB prod DB (disk 96%) |
| F09 | P3 | 主页 | all | yes | 「Powered by FLVX」 pill covers the 24h chart time axis |
| F10 | P3 | resource | — | yes | path-latency job pings 113 pairs every minute regardless of use |
| R1–R5 | risk | ws / auth | — | yes | slow WS consumer stalls node read loop; CheckOrigin true; users get others' forward_metrics; disabled users can still edit; login unthrottled + unsalted MD5 |
