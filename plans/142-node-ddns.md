# 142 · Node DDNS (Cloudflare) on FLVX nodes — RELEASED as 3.0.27-fork.82 (2026-10-24 TW)

Requested 2026-10-24 ~02:40 TW (user voice message): node 48 「Leikwanhost 沪台IPLC」 lost its CT
entry when the SLAAC prefix rotated (`240e:b8f:a30:1904::/64` → `240e:b8f:72d:5c04::/64`); node
addresses are manual. The user wants a DDNS option on node create / edit (like miaomiaowuX's server
form) so entries become domains that follow the node's real IPs. DNS = Cloudflare.

Base: `origin/maintenance/3.0.27-fork.80-native-landing-ports` @ 34f66765 (= fork.81 + plan doc).
Branch `feature/3.0.27-node-ddns`. **No tag, no release, no prod change, no OTA** — the UI waits
for the user's screenshot approval. Pure FLVX feature: no runtime link to miaomiaowuX.

## Design decisions

1. **DNS 服务商** (admin only): table `dns_provider` (name, type `cloudflare`, credentials text JSON
   `{"api_token":…}`, token status / expires_on / checked_at / error, cached zone names). The token
   is never returned (masked 「已设置」); edit = replace, empty = keep. 「测试」 = CF
   `GET /user/tokens/verify` + `GET /zones`. Validity: no `expires_on` → 「永久有效」, else 「剩余 N
   天」, warn chip < 14 days; re-checked lazily at most once a day (when the list is opened) + on
   测试. Delete refused while nodes use it. Routes under `/api/v1/node/ddns/…` (admin by the existing
   `/api/v1/node/` rule). Not in backup export (secret).
2. **Node DDNS config**: node columns `ddns_enabled`, `ddns_provider_id`; table `node_ddns_record`
   (node_id, domain, type A/AAAA, role `main`/`extra`, source, custom prefix, takeover, state:
   zone/record id, current IP, TTL, applied, status, last error, last sync/check, fail count, next
   retry). Unique (domain, type). Max 8 records, ≤ 1 main per family. Saved through node
   create/update as an optional `ddns` object; **absent `ddns` = config unchanged** (old clients
   and API scripts keep working).
3. **Sources**: AAAA `auto` (first global IPv6, default-route NIC first) / `ct` 240e / `cu` 2408 /
   `cm` 2409 / `prefix` (CIDR or hex prefix); A `panel` (IPv4 the panel sees on the agent WS, works
   with old agents) / `iface` (first public IPv4 on a NIC).
4. **Ownership**: while DDNS is on, a `main` AAAA record owns `server_ip_v6`, a `main` A record owns
   `server_ip_v4` (and `server_ip` when it equals the owned value); the node save keeps the DB value
   for owned fields (UI shows them read-only with the domain). The field is switched **to the domain
   once, after the first successful CF sync** (repo method touching only address fields, then the
   existing `syncNodeUpdate` path). `extra` records replace the matching extra IP (or are appended).
   Later IP changes only touch CF → tunnels / rules / user copies never change again.
5. **Bind IPs stay literal**: forward_port `in_ip` is a listen address; node-edit address rewrites
   never write a hostname into it (keeps the literal). When DDNS moves an owned IP old→new, bind IPs
   equal to the old IP are rewritten to the new IP and those rules redeployed.
6. **Agent** (`go-gost/x/nodeaddr`): global unicast IPv6 (skip link-local, ULA, doc, temporary,
   deprecated, tentative, dadfailed; skip container/virtual NICs) + public IPv4 on NICs. Event driven
   (netlink `AddrSubscribe`, 2 s debounce) + refresh on the existing 10-min RA tick (no new timer);
   piggybacked on the `metric` message as `addrs` only when the set changed (and once per session).
   Capability `addrReport=1` in the WS handshake. Old panel ignores the unknown field.
7. **Updater (panel)**: per-node serialized sync on: agent address change, node online, save,
   「立即同步」, retry/drift timer. One timer goroutine (no short poller). CF upsert proxied=false,
   TTL 120 s, comment 「FLVX DDNS」. Existing record pointing elsewhere / proxied / duplicated →
   refused unless 「接管已有记录」 (checked on save). Never deletes records; protected names
   (flvx/flvxx/flvxht/flvxxht, mmwx*, apex, wildcard, panel host) refused. Failures: backoff 30 s →
   10 min cap; log ≤ 1 line / record / 10 min. Drift check: one GET per record per 60 min.
8. **UI (add-only)**: settings card 「DNS 服务商」; node form section 「DDNS」 after 附加 IP; node
   card/list badge 「DDNS 已同步 / 待同步 / 失败 / 需升级节点」; extra IP list / entry picker /
   rule display accept domains; listen-IP options keep literal IPs only.

## Checklist

- [x] Baseline `go test ./...` on the unmodified base (34f66765): 15 FAIL lines (backup ×5, service monitor ×2, federation dual panel ×3, legacy migration ×2, nodelay, renewal cycle, address-in-use) — same set as earlier rounds
- [x] Backend: models + migration (SQLite + PG), provider repo + CF client + provider API
- [x] Backend: node DDNS config parse/validate/save (create + update), list payload, delete cascade
- [x] Backend: extra IPs accept domains; bind IPs stay literal
- [x] Backend: updater (selection, CF upsert, apply-once, bind rewrite, backoff, drift, 立即同步)
- [x] Backend: WS capability + `addrs` ingest + panel-seen IPv4
- [x] Agent: nodeaddr collector + subscribe + metric piggyback + handshake capability
- [x] Unit tests (selection/filter/change/CF fake/backoff) + agent test + SQLite/PG contract tests
- [x] Real CF round trip on a throwaway record (create → update → read → delete → gone)
- [x] netns E2E (agent in netns, fake CF, prefix change → one update, no double redeploy) + resource numbers
- [x] Frontend: API, settings card, node form section, badges, domain-aware extras / picker
- [x] `npm run build` + full `go test ./...` vs baseline (15 FAIL lines, identical)
- [x] Screenshots desktop + H5, light + dark; gallery published
- [x] Branch pushed, CI green (run 40025762299 incl. PG contract); summary + final.md

## Results (2026-10-24 TW)

- Base `origin/maintenance/3.0.27-fork.80-native-landing-ports` @ 34f66765 (fork.81 + plan doc).
- Backend: `internal/ddns` (Cloudflare client, address selection, protected names, backoff),
  `dns_provider` + `node_ddns_record` tables (+ `node.ddns_enabled`, `node.ddns_provider_id`),
  provider API under `/api/v1/node/ddns/…`, node create/update `ddns` object, updater
  (`ddns_sync.go`), WS `addrReport=1` + metric `addrs`, extras accept domains, listen IPs stay
  literal, legacy report-ip never overwrites DDNS nodes. Lab-only env `FLVX_DDNS_CF_API`.
- Agent: `go-gost/x/nodeaddr` + `socket/node_addrs.go` (netlink event driven, RA tick fallback,
  sent only on change / once per session; 102 bytes for node 48's two IPv6).
- UI: 网站配置 「DNS 服务商」 card, node form 「DDNS」 section, node badges, owned-field hints,
  delete note, domain labels in the entry picker; IPv4 row no longer repeats the IPv6 domain.
- Tests: `go test ./...` = 15 FAIL lines, identical to the baseline; new DDNS unit / handler /
  contract tests pass (SQLite + PostgreSQL 18; contract + repo packages with PG: only the
  baseline failures). go-gost/x socket / nodeaddr / nftables pass (base: same). `npm run build`
  ok, `tsc` clean, eslint errors unchanged on edited files.
- Real Cloudflare round trip (`-tags cfreal`): throwaway `flvx-ddns-test-1792787762.imgamer.top`
  created (AAAA, TTL 120, proxied=false, comment FLVX DDNS) → updated with 1 API call → read back →
  deleted → verified gone; zone record count unchanged (33).
- netns E2E (`scripts/ddns-e2e/e2e.py`): first sync 1.0 s after save; CT prefix rotation → exactly
  one PUT 2.8–2.9 s later; tunnel entry rewritten once per record at the switch, 0 times after;
  lifetime refresh + 300 s idle → 0 API calls. Agent 120 s: base fork.81 0.292 % CPU / 50.6 MB RSS /
  8 threads / 8 fds; branch 0.225 % / 48.5 MB / 8 / 9 (+1 fd = netlink subscription).
  1 h idle simulation (handler test, fake clock): 2 calls for 2 records (one GET drift check each).
- Screenshots: 64 shots (desktop 1440 + H5 390, light + dark), 110/110 checks, 0 page errors;
  gallery `/root/flvx-workers/runs/node-ddns/screens/index.html` on mmwx-preview `…/node-ddns/`.

## Release (fork.82, 2026-10-24 TW)

Approval: user 2026-10-24 ~04:40 TW waived the screenshot gate for this queue; the release worker
reviewed the gallery (DNS 服务商 card, node form DDNS section desktop/H5 light/dark, badge menu):
no layout defect, nothing re-shot. Branch `maintenance/3.0.27-fork.82-node-ddns` = feature head
4ff90130 + ride-along 「强制 IPv4/IPv6」 label fix (5e2e5be0 + c8aca366 from
`fix/3.0.27-force-ipv4-label`, user-requested for the next panel release).

- [x] Gallery self-review (no defect, no re-shoot)
- [x] Release branch + ride-along cherry-pick (no conflicts)
- [x] `go build ./...` + backend tests SQLite + PostgreSQL vs baseline; go-gost build/tests; frontend tsc/build/lint
  - SQLite `go test ./...`: 15 FAIL lines, identical to the fork.81 baseline list; PG 18 (contract + repo + handler): only baseline names
  - go-gost + x build ok; `x/nodeaddr`, `x/socket`, `x/landing` pass (netns E2E skipped, needs real core)
  - frontend: tsc 0, `vite build` ok (index 2.98 MB), eslint errors on modified files 31 = base 31
- [x] Prod-DB identity check on a read-only copy (fork.81 vs fork.82: node/tunnel/forward data unchanged, migration columns only, DDNS off everywhere); copy deleted
  - both panels in an empty netns; node/tunnel/forward/user/tunnel-user/egress-global lists identical; DB rows identical except `node.updated_time` (both versions set it at startup)
  - landing node/server lists differ only by the intended label 直连（仅 IPv4）→ 强制 IPv4; new tables `dns_provider`, `node_ddns_record`, node columns `ddns_enabled`/`ddns_provider_id`, 0 nodes with DDNS
- [x] Push, CI green, tag `3.0.27-fork.82`, release Latest with the same asset set as fork.81
  - branch CI 40032058919 green; tag → 94381193, Images/Release 40032286008 green; Latest, 10 assets = fork.81 set, compose/install pinned, gost sha256 ok, ghcr amd64+arm64
- [x] Prod panel: validated DB backup + rollback dir, upgrade `/opt/flvx-svc`, prune backups (keep 2)
  - rollback `pre-fork82-20261023T222555Z` (quick/integrity ok) + `pre-fork81-20261023T195325Z`; pre-fork80 + ghcr fork.79 images pruned; healthy 06:26:10 TW (9 s), 30/30 online, reconnect redeploy 0 updated / 0 failed
- [x] Node 48: SSH backup, OTA to fork.82 (needs `addrReport=1` agent)
  - backup `/root/flvxx-backup-20261023T221445Z-pre-fork82/restore-agent.sh` (pre-fork67 backup removed, 2 kept); panel OTA 3.1 s, reconnected with `addrReport=1`; no other node OTA'd
- [x] DNS 服务商 「Cloudflare imgamer.top」 + node 48 records (ct 240e main, cu 2408 extra, TTL 120); records resolve to node 48's current addresses; tunnels 75/78/79 still pass traffic
  - provider id 1 token active / 永久有效 / zone imgamer.top; CF zone 33 → 35 records (only the two new AAAA, proxied=false, TTL 120, comment FLVX DDNS)
  - `leikwan-hutai-ct` → 240e:b8f:72d:5c04:ca:8aff:fec0:2813, `leikwan-hutai-cu` → 2408:820c:750b:dc64:ca:8aff:fec0:2813 (1.1.1.1 + 8.8.8.8); node 48 serverIp/serverIpV6 → ct domain, extra → cu domain, no other node field changed; tunnels 75 (ct,cu) / 78 / 79 (ct) in_ip = domains; forward_port in_ip untouched
  - tunnel diagnose 75/78/79 ok, forward diagnose 110/113/126/156 ok, TCP via both domains to all 4 rule ports open
- [x] Prod checks (version, health, online count, agent versions, migration once, bundle, real-JWT admin/non-admin, 10-min resources/logs)
  - version.json `3.0.27-fork.82-mvlj4s74` (local + public); bundle has DNS 服务商 / 接管已有记录 / DDNS 已同步 / 需升级节点 / 强制 IPv4
  - 30/30 online; agents 29 × fork.79 + node 48 fork.82; extra backend restart: no migration/DDNS line repeated, records stay synced, 30/30 back
  - real JWT: admin provider list / node list 200; user 3 → 403 on provider list/test/delete, node/list, ddns/sync; anon → 401; CF token never in any response; user 23 sees `leikwan-hutai-ct.imgamer.top:45233` (read-only)
  - 10 min after restart: backend 2.3–4.3 % CPU / 25.6 MiB (fork.81 3.7 % / 35.9 MiB), DB 56.6 MB unchanged, 12 log lines/10 min (access lines only; fork.81 quiet window 1); node 48 agent ≈0.43 % CPU, 64 MB RSS, journal 28 lines/10 min (before OTA 29–30, all SetQuotaGroups)

Rollback: `bash /opt/flvx-svc/rollback/pre-fork82-20261023T222555Z/rollback.sh` (→ fork.81, keeps DB; node 48 stays on the domains, DNS just stops following IP changes; `--restore-db` = DB as of 06:25 TW incl. literal IPs). Node 48 agent: `sh /root/flvxx-backup-20261023T221445Z-pre-fork82/restore-agent.sh` (→ fork.79). CF records are never deleted by FLVX; remove by hand in Cloudflare only if DDNS is abandoned.
