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

---

## Round 2 (2026-10-23, after fork.74 security hotfix)

Branch `qa/3.0.27-qa-r2` = `origin/maintenance/3.0.27-fork.74-security-hotfix` + plan 134's 8 commits
(cherry-picked from `origin/feature/3.0.27-landing-rule-layers`; a plain merge conflicted only because 134 sits on the
pre-release 131 commits — same content, one trivial `.gitignore` comment conflict). Run dir
`/root/flvx-workers/runs/qa-sweep-r2/` (final.md, summary.md, evidence/, screens/); findings appended to round 1's
findings.md (section 「第二轮」, ids F11+); gallery section `#r2`. Report only, no product code changes, no release.

### Checklist (round 2)

- [x] Test branch: fork.74 + 134 (cherry-pick, trivial conflict) + round-1 QA scripts
- [x] Lab: paneld/agent/frontend from the branch; neutralised prod DB copy; 8 real agents in per-node netns wired to the panel netns; target / client (mihomo) / fake-LeiKwan netns; isolation proof
- [x] Hotfix verification: WS probe (11 listeners incl. bad/sibling Origin, forged token, per-user scope, no reply broadcast), 304-route authz (no change vs r1, disabled user refused), login rate limit + known-good address + MD5→bcrypt, live disable (REST at once, WS ≤ 60 s), node_metric retention on prod (gap ≤ 60 s), F01–F09
- [x] Request rate per page left open 2 min (admin + user), WS frames/min
- [x] UI click-through: invalid input, empty submit, double-click, throttled double-click, refresh mid-submit, two tabs on one object, Esc in nested popovers
- [x] Write flows in the lab UI: node create (附加 IP), 启用为落地 + server settings, tunnel create (entry-IP picker with extra IP, hop, exit), user grants, user landing nodes (SS2022, VLESS Reality), rule → 我的落地节点, diagnose, node delete refusal, tunnel delete + migrate, OTA dialog
- [x] Full path traffic entry→hop→exit→landing→target with mihomo + byte checks; billing vs bytes; landing monitor vs DB row by row
- [x] Reconnect test: forced WS reconnect of each of 4 path nodes during a 24 s stream (redeploy does not cut streams)
- [x] Expiry pause/resume with traffic; plan 134 caps (UI + API), forced global rule vs user override, lowering caps keeps rules
- [x] Links: SS2022 + VLESS Reality traffic through mihomo; AnyTLS/Snell/Mieru TCP/UDP links + Clash entries parsed by mihomo (21/21)
- [x] 线路机 `chain_lab.py` C1–C8 (C6 fd assertion relaxed to a warning, see F14)
- [x] 利群汇聚 UI against the repo's fake LeiKwan API (HTTPS, lab CA, lab DNS only): overview, create pipe (1 write on double-click)
- [x] Prod read-only: GET + WS; POST read allowlist proven by `readonly_audit.py` and written to findings.md before use; API vs DB compare; browser tour with every other request aborted
- [x] findings (F11–F28, R6–R11), gallery 第二轮, final.md, summary.md
- [x] Cleanup lab (processes, netns, routes, DB copy, tokens)
- [ ] Not done: real OTA, prod node journald (F13 numbers are lab-only), H5 dialog clicks, batch redeploy button, LeiKwan takeover/retarget/delete, WARP/proxy egress, two-entry-node multi-entry

### Findings (round 2)

| id | sev | area | prod? | one line |
|----|-----|------|-------|----------|
| F11 | P2 | nodes | latent | node `status` never reset on panel start: nodes down at restart show 在线 forever; commands say 节点不在线 |
| F12 | P2 | tunnel delete | yes | 「迁移到其他隧道」 ignores the rule owner's tunnel grant; rule lands on an ungranted tunnel, owner can't edit it |
| F13 | P2 | agent logs | likely | agent logger default info: hop/exit log 4 JSON lines (~690 B) per connection incl. dst; entry prints 2 `[FailFilter]` lines per connection |
| F15 | P2 | 利群汇聚 | yes | pipes whose LeiKwan forward was deleted vanish from the overview without a warning; tunnel stays |
| F14 | P3 | resource | yes | Reality Vision stream = 4 fds (SS2022 2) + guard dest conn ~5 s per new conn; chain_lab C6 old assertion fails |
| F16 | P3 | 规则页 | yes | admin opening 规则 page rewrites global group-order/collapsed config (read-modify-write) |
| F17 | P3 | resource | yes | 5 s polling on 主页/规则 (admin 3 calls per 5 s on 规则); admin WS ~1.25 MB/min with 29 nodes |
| F18–F28 | P3 | UI / auth | yes | favicon config 404 + triple config/list; node dialog error out of view; misleading name/port messages; stale 诊断中 row; delete refusal w/o names; two-tab lost update; over-quota hint; login timing; node/info nil panic; 134 editor default 封锁 + silent cancel |
| R6–R11 | risk | — | — | DNS failure during reconnect redeploy skips a rule; prod nodes 45/12/26 reconnect often; url.PathEscape in AnyTLS/Mieru userinfo; expiry pause timing; group-order sanitizer; chart tick |
