# 307 — RELEASED 11-25 TW as fork.171 (20:06) + fork.172 (20:46); data op done on 23 nodes — DDNS main domain + 用户专属域名 on every node except IXP; editing a kept forward moves it to the new design

Task `ddns-all-nodes` (coordinator 1218a2c5, 11-25 evening TW, Claude xhigh). Prod = fork.170 (plans 303 + 305).

## User decisions (11-25 evening)

1. Every node except the 3 IXP (Mkcloud 深港ixp 1 / 深港ixp 2 / 沪日ixp 440) gets a DDNS main
   domain and 用户专属域名 ON, so users are never handed a raw IP.
2. Existing forwards / landings are not touched (toggle with 「已有转发保持原地址」 ON), but a kept
   forward that is **edited** (forward/update from the edit dialog, by the user or an admin), or
   deleted and recreated, uses the new design (owner's name, 「正在就绪」 until ready). Not an edit:
   pause/resume, redeploy, batch status ops, traffic reset, expiry/limit jobs, tunnel or node edits.
   Same for a kept native landing node edited in its edit dialog.
3. Multi-record nodes: one name per DDNS record per user (305, live).

## Findings (fork.170 code + prod 11-25)

- The DDNS sync **always switches the node address to the domain** on the first sync
  (`ddnsApply` → `ApplyDDNSDomain` → `syncNodeUpdate` = tunnel redeploys) → hard rule says no.
  → new per-node option 「保持节点地址」 (`node.ddns_keep_address`, API `ddns.keepAddress`).
- Record sources are agent / panel based only. `panel` = IPv4 the panel sees on the agent
  connection; for Nobrand 5cm IPLC 沪日 / Nobrand IPLC 沪港 that is the overseas egress
  (87.86.22.15 / 143.203.126.34), not the 上海 entry users dial (120.253.251.132 / .146);
  offline nodes (火山广州 / 火山上海) never get a panel IP → their records would never exist.
  → new source 「固定 IP」 (`static`, the IP in the prefix field), A and AAAA.
- User names map hosts = DDNS domains + their current IPs. With the node address kept, nodes whose
  address is a domain we don't own (Fachost seednet 黑五 `facseednet`, Dmit Nathan `dmitnathan`,
  Boil HKT 99u `boilhkt99u`, NexKR vendor name) would keep handing that shared domain out.
  → in keep-address mode a hostname in the node's v4 / v6 address field also maps to the main
  record of that family (IP literals only through the record's IP — never a guess).
- Users get entries on the target nodes today only through: Datawave HK 2 + AWS HK (admin
  forwards), Dmit Nathan (9 tunnels, admin + usb2), native landings on Bestvm US / Bestvm JP B /
  Fachost / NexKR / Tcscloud / 马年 / 99u (+ user forwards on Dmit Echo targeting their own
  landings on Bestvm US / JP B: kept via the landing keep set → remoteAddr unchanged).
- node/update: payload without `panelIpFamily` keeps the stored value (avoids '' → 'auto' push).

## Checklist

### Code (branch from fork.170 9cfde8b2)
- [x] forward/update (dialog / App / chain dialog) success → drop the forward's keep rows
- [x] landing/node/update with dialog fields (not status-only) → drop the landing's keep rows
- [x] node.ddns_keep_address + `ddns.keepAddress` (nil = unchanged); sync never switches the
      address / rewrites listen IPs; status 「已同步」 without "applied"; address fields stay editable
- [x] source `static` (固定 IP) for A / AAAA: validate literal public IP of the family; no agent needed
- [x] keep-address alias: node address hostname → user's name of the main record of that family
- [x] Frontend (add-only): 「保持节点地址」 checkbox + 「固定 IP」 source + IP input; address inputs
      editable when kept
- [x] Tests: unit (static source, alias), contract SQLite + PG (edit → name; pause/resume / batch /
      status-only → kept; subs byte-identical for untouched kept forwards; keep-address no switch)
- [x] go test ./... SQLite + PG vs base; tsc + build (tsc + build ok)
- [x] Screenshot → gallery `:8791/user-subdomain/` section 「307」

### Release
- [x] Next free fork tag (ls-remote + ledger), branch `maintenance/3.0.27-fork.<N>-ddns-all` → fork.171
- [x] CI + Images green, Latest, 10 assets, gost sha256
- [x] Prod-DB copy rehearsal (migrates; old image starts), subs byte-identical
- [x] Baseline (5 busiest forwards + RTT), backup + prune, panel swap, light check, rollback script

### Data op (after the release)
- [x] Per node: name free via CF API only, ownership check (mmwx names untouched)
- [x] DDNS on with keepAddress + static/panel/agent/reported source, toggle on with keep=true (23/23)
- [x] Verify: node rows unchanged, 0 new names, forwards / subs byte-identical, nodes online,
      no tunnel redeploy, CF count under the guard

### Docs
- [x] Ledger row, memories (fork-release-and-deploy, entry-domains, node-ddns), summary

## Results

### Build (11-25 TW)
- Commit 198cb2ae on `maintenance/3.0.27-fork.171-ddns-all` (from fork.170 9cfde8b2). CI Build Check 45607212268 green.
- `go test ./...` SQLite + PG (throwaway PG 18): every package ok, exit 0 (contract 610 s, repo 490 s,
  handler 458 s). New: `TestStaticSource`, `TestUserSubdomainDDNSAllNodesContract` (sqlite + postgres).
- tsc + `npm run build` ok.
- Lab (isolated netns, fake Cloudflare, synthetic data): 29/29 checks; screens → gallery 「307」.
- Prod-DB copy rehearsal (isolated netns, fake Cloudflare seeded with the real zone):
  fork.170 vs fork.171 = subs 104/104, user views 141/141 identical; fork.171 + the whole data op
  (23 nodes, enable then toggle) = subs 104/104 identical, node rows / tunnel entries unchanged,
  names 6 → 6, views differ only in admin-only 「保持原地址」 tags and an empty publicHost on the
  Bestvm US / Bestvm JP B 「可用落地」 cards of 3 users with no landing there (303 r3 design: no
  name yet → no address; only shown in the AnyTLS SNI hint); fork.170 started on that DB after the
  rollback SQL (DDNS off where ddns_keep_address=1): node addresses unchanged, subs identical.

### Release fork.171 (11-25 20:06 TW)
- Tag 3.0.27-fork.171 → 198cb2ae, Images 45607808886 green, Latest, 10 assets, compose tags, PINNED_VERSION /
  REPO, gost sha256 ok. Swap 20:06:32–20:06:40 TW (panel only, no OTA); DB + node.ddns_keep_address.
- Rollback `/opt/flvx-svc/rollback/pre-fork171-20261125T120631Z/rollback.sh` (turns DDNS off where
  ddns_keep_address=1 before fork.170 starts — fork.170 would switch those addresses). Pruned to 2 backups
  (pre-fork171 + pre-fork170).
- Light check: 30/32 online before = after (火山 ×2 offline before), 30 reconnect redeploys all unchanged /
  failed=0, node addresses + tunnel entries identical, users' views 141/141 identical, 5 busiest forwards diag
  3/3 + RTT unchanged (#90 26.8 → 26.8 ms, #625/#631/#644 ~1 ms, #660 57.9 ms).

### Data op (11-25 20:08–20:14 TW, prod admin API = the node dialog's save)
- DDNS (保持节点地址) on 23 nodes; 21 synced at once. Records (all new, comment `FLVX DDNS`):
  Bestvm TW bestvm-tw → 188.253.6.237 · Lightlayer TW lightlayer-tw → 156.245.238.207 · Datawave HK 2
  datawave-hk2 → 103.97.175.50 · Bestvm US bestvm-us → 155.254.127.160 · Bestvm JP B bestvm-jpb →
  188.253.118.132 · Vmsilo TW vmsilo-tw AAAA (联通 2408) → 2408:8748:a100:59:38d5:ce03:dedb:ff57 · Bestvm HK STD
  bestvm-hk-std → 150.129.43.123 · Datawave TW 1 datawave-tw1 → 78.105.182.55 · Dmit T1 dmit-t1 → 154.3.39.138 ·
  Bestvm sg 年付 bestvm-sg → 185.36.192.124 + bestvm-sg-v6 AAAA → 2407:cdc0:d003:2c06:badb:71b:f533:104d ·
  Bestvm HK 年付 bestvm-hk-annual → 163.53.18.89 · AWS HK aws-hk → 43.199.143.188 · Dmit Nathan dmit-nathan →
  103.117.101.148 · vmsilo 沪港 vmsilo-hugang AAAA (电信 240e) → 240e:96c:7100:1fe:1c8:c8aa:4ecc:b4 · 火山广州
  volc-gz → 101.96.213.164 · Nobrand 5cm IPLC 沪日 nobrand-huri → 120.253.251.132 · Nobrand IPLC 沪港
  nobrand-hugang → 120.253.251.146 · NexKR nexkr (面板看到的出口) → 218.50.130.172 · 百度云广州 baidu-gz →
  106.13.17.23 · 阿里广州 aliyun-gz → 8.134.172.154 · 火山上海 volc-sh → 14.103.34.108 (固定 IP unless noted).
- Every node row: only ddns_enabled / ddns_provider_id / ddns_keep_address (+ user_subdomain_enabled) changed;
  expiry-reminder dismissals reset by node/update were re-dismissed (Bestvm HK 年付, Nobrand IPLC 沪港, NexKR,
  Fachost seednet 黑五). Sync results: 0 tunnels / 0 rules / 0 entry addresses updated on every save.
- Fachost seednet 黑五 + Boil HKT 99u: agents reach the panel over IPv6, no public IPv4 on a NIC, IPv4 dynamic
  (Fachost: 4 IPv4s in 14 days) → 面板看到的出口 / 网卡 sources fail (「没有地址」), 固定 IP would go stale →
  new source 「节点上报的出口 IPv4」 (node.reported_ip_v4) in fork.172. mmwx's facseednet / boilhkt99u and
  dmitnathan / dmithkt / ly untouched.
- 用户专属域名 ON with 已有转发保持原地址 on the 21: 0 names; kept: Datawave HK 2 1 forward, AWS HK 3, Dmit
  Nathan 3, Bestvm US 8 landing nodes, Bestvm JP B 5, NexKR 1. Users' views before/after: 137/141 identical +
  admin-only 「保持原地址」 tags on 7 forwards + empty publicHost on the Bestvm US / Bestvm JP B 「可用落地」
  cards of 3 users with no landing there (same as the rehearsal).

### fork.172 (11-25 20:46 TW) + the last two nodes
- Code 3d40695a: A source 「节点上报的出口 IPv4」 (`reported` = node.reported_ip_v4, the agent's own egress check;
  an agent public-IP report re-syncs such a node). Tests TestReportedSource + TestNodeDDNSReportedSourceKeepAddress;
  go test ./... SQLite + PG 30/30 ok; tsc ok. Branch `maintenance/3.0.27-fork.172-ddns-reported` (= the fork.171
  branch head), tag 3.0.27-fork.172 → 3d40695a, CI 45614120354 + Images 45614123493 green, Latest, 10 assets,
  sha256 ok. Swap 20:46:32–20:46:40 TW, no DB change; rollback `/opt/flvx-svc/rollback/pre-fork172-20261125T124631Z/`
  (→ fork.171). Backups kept: pre-fork172 + pre-fork171.
- Light check: views 141/141, node addresses / tunnels identical, 30 reconnect redeploys unchanged / failed=0,
  5 busiest forwards same as baseline (#660's exit→target jitter 0.06–1 s was already in the fork.170 baseline).
- Fachost seednet 黑五 fachost-seednet → 123.204.25.103, Boil HKT 99u boil-hkt99u → 58.152.255.186 (reported),
  synced, toggled with keep (1 admin landing each kept).

### Final state (11-25 20:48 TW)
- 29 nodes with DDNS + 用户专属域名 (23 new with 保持节点地址 + the 6 from before); IXP ×3 untouched (off).
- Cloudflare imgamer.top 45 → 69 records (+24, all `FLVX DDNS`, unproxied); no existing record touched by the op
  (tcsseednet followed Tcscloud Seednet's own IP change 20:30 TW, its normal DDNS).
- Names 6 → 6 (no user got a new name); keep rows 22 → 43. Node rows (non-DDNS columns), tunnel entries, forwards:
  identical to the pre-op copy. Prod-DB copies pre-op vs post-op rendered by fork.171: subs 104/104 byte-identical.
- Users see no change until they create a forward / landing on these nodes or save the edit dialog of a kept one.

### Undo
- Per node: node edit → DDNS → untick 「用户专属域名」 (names pause) and/or switch DDNS off (records stay in
  Cloudflare; delete them there by hand if wanted). With 保持节点地址 the node address never moved, so nothing else
  to restore.
- Panel: `/opt/flvx-svc/rollback/pre-fork172-*/rollback.sh` (→ fork.171), then `pre-fork171-*/rollback.sh`
  (→ fork.170; turns DDNS off on the 23 保持节点地址 nodes first).
