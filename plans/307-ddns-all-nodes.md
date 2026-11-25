# 307 — DDNS main domain + 用户专属域名 on every node except IXP; editing a kept forward moves it to the new design

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
- [ ] go test ./... SQLite + PG vs base; tsc + build (tsc + build ok)
- [x] Screenshot → gallery `:8791/user-subdomain/` section 「307」

### Release
- [ ] Next free fork tag (ls-remote + ledger), branch `maintenance/3.0.27-fork.<N>-ddns-all`
- [ ] CI + Images green, Latest, 10 assets, gost sha256
- [ ] Prod-DB copy rehearsal (migrates; old image starts), subs byte-identical
- [ ] Baseline (5 busiest forwards + RTT), backup + prune, panel swap, light check, rollback script

### Data op (after the release)
- [ ] Per node: name free via CF API only, ownership check (mmwx names untouched)
- [ ] DDNS on with keepAddress + static/panel/agent source, toggle on with keep=true
- [ ] Verify: node rows unchanged, 0 new names, forwards / subs byte-identical, nodes online,
      no tunnel redeploy, CF count under the guard

### Docs
- [ ] Ledger row, memories (fork-release-and-deploy, entry-domains, node-ddns), summary
