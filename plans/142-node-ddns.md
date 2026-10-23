# 142 · Node DDNS (Cloudflare) on FLVX nodes — build + screenshots, no release

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
- [ ] `npm run build` + full `go test ./...` vs baseline
- [x] Screenshots desktop + H5, light + dark; gallery published
- [ ] Branch pushed, CI green; summary + final.md

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
