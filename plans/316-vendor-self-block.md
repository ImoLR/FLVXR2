# 316 — 屏蔽本机厂商官网 (every landing blocks its own VPS vendor's sites)

User 11-26 (NodeSeek 1077705: a 拼车 rider attacked DMIT's account/ticket
system from a DMIT VM, DMIT closed the whole account). Each landing refuses
user traffic to **its own** vendor's website / client area / billing / panel
hosts. Release = next free fork (fork.176), after fork.175 (plan 315).

## Design

- **Vendor list** (admin editable, 全局规则 → 内置 → 「屏蔽本机厂商官网」 →
  「管理厂商」): `landing_egress_setting.vendors` JSON (`''` = built-in seed
  list), entries `{key, name, domains[], remark}`; domains are bare hostnames,
  compiled as `domain:<host>` (host + subdomains). Only the vendor's own
  site/client/billing/panel hosts, never shared infra (amazonaws.com,
  cloudfront.net, aliyuncs.com, alicdn.com, volces.com, cloudflare.com …).
- **Per server pick**: `landing_server.vendor` (key, `''` = 未设置). The
  server 设置 dialog has a 「VPS 厂商」 select with a name-based suggestion;
  the stored choice is explicit. Admin ⚠ 「未设置厂商」 via
  `serverAdminAttention` while the switch is on.
- **Global switch**: `landing_egress_setting.builtin_vendor` (NULL = on), in
  the 内置 list like the 防滥用 rows.
- **Compiled rule**: `内置：屏蔽本机厂商官网（<厂商>）` = `domain:` list →
  block, over every node of the server (+ chain copies when the agent knows
  builtin markers), emitted as the **first plan rule** (before 防滥用, 全局·强制
  such as 放行谷歌/苹果, node rules / 我的规则) → users cannot override it.
  Same readiness as the 防滥用 defaults. Marker: `vendor` for agents with
  landing_guard ≥ 3, `abuse` (block-only, before chains, never withholds) for
  landing_guard 1–2, none for the mmwx Custom (strict JSON).
- **SNI / Host matching (agent, landing_guard 3)**: inbound sniffing keeps
  `routeOnly` + domain exclusion, but `domainsExcluded` becomes
  `regexp:<complement of the vendor names>` instead of `regexp:.*`: only a
  sniffed TLS SNI / HTTP Host that IS a vendor name may override the routing
  domain, and the vendor block rule (rendered right after flvx-safety) then
  blocks it. Every other sniffed name is still excluded, so the plan-? private
  target protection (sniffed public name must not replace a private original
  domain) is unchanged. Transitions keep the invariant "inbound override set ⊆
  vendor block set in the applied routing" (routing first with the union, then
  inbounds, then routing trimmed on the next tick).
- Older agents / mmwx shares: requested-domain match only (hot rule update, no
  restart). SNI coverage on mmwx shares would need an mmwx Custom change
  (follow-up, not in this plan).

## Checklist

- [x] Plan doc
- [x] Backend: model columns, repo (vendor list, switch, server vendor), seed list
- [x] landingegress: vendor rule compile (first rule, markers, chain tags, limits) + tests
- [x] Handler: 内置 admin view / node view row, vendor save API, builtin switch, server vendor in update + list view (suggestion), resync on change
- [x] Agent: `vendor` builtin validate/render first, sniff complement regex, safe transitions, GuardVersion 3 + tests
- [x] Lab Core test (netns): SNI vendor blocked, other SNI passes, private-target protection unchanged
- [x] Frontend: 内置 row (admin + node + H5 compact), 管理厂商 dialog, server 设置 select, ⚠ 未设置厂商
- [x] Screenshots desktop + 390 px (gallery)
- [x] go vet + go test (SQLite + PG handler/repo/contract), go-gost tests, npm run build
- [x] Prod-DB copy render before/after: diff = only the new rule, only on servers with a vendor
- [ ] Wait for fork.175, rebase, release fork.176 (tag, CI, assets)
- [ ] Prod backup + upgrade, prune backups
- [ ] Assign vendors on prod; OTA landing agents (idle now / busy in 02–06 TW); node checks
- [ ] Live check per vendor (vendor domain refused, normal site OK)
- [ ] Ledger, memory, summary

## Evidence (build)

- Vendors seeded (web search + DNS + local vendor configs, no crawling): DMIT dmit.io + dmithost.net · Bestvm bestvm.cloud ·
  Boil boil.network · Fachost fachost.cloud · PQS pqs.pw · Tcscloud tcscloud.net · NexKR nexkr.sh (not client.nexkr.cloud =
  customers' hosts) · Leikwanhost leikwanhost.com · vmsilo vmsilo.com · Lightlayer lightlayer.net · AWS aws.amazon.com +
  signin.aws · 阿里云 aliyun.com + alibabacloud.com · 百度智能云 cloud.baidu.com + bce.baidu.com · 火山引擎 volcengine.com.
  Not seeded (domain not confirmed): Datawave, Mkcloud, Nobrand — admin can add them.
- Lab netns E2E with the pinned Core v1.4.83 (`TestVendorBlockNetnsE2E`, SS2022 node): plan-316 marker → IP + SNI
  www.dmit.io / dmit.io / act.dmithost.net refused, HTTP Host www.dmit.io refused, SNI www.example.com / notdmit.io pass,
  requested domain www.dmit.io refused even with a later 「直连 dmit」 user rule, private target refused with any SNI and never
  reached; older "abuse" marker → requested domain refused, IP + SNI passes (as designed); removal = 2 passes, then SNI
  passes again. A live TCP conn survives the inbound re-add (sniffing change), re-add pass ≈ 0.17 s.
- Prod-DB copy (11-27 05:23 TW) render before (fork.175 head 2458af9f) vs after: only servers with a vendor and nodes gain ONE
  rule in first place (DMIT T1 shared no marker; native v2 agents marker abuse; simulated v3 marker vendor); Boil Hinet 158
  unassigned → identical; empty servers identical; warnings / outbounds / restart flags unchanged (work/render-diff.txt).
- Gallery: http://104.145.236.29:8791/vendor-self-block/ (27 PNG, desktop / 390 px / dark).
- Tests: go-backend `go vet ./...` ok; `go test ./...` SQLite: one failure, TestLandingUserServerViewContract (pins the admin
  server-view keys) → expected keys updated (vendor fields admin-only, never in the user view), rerun ok; PG
  (`FLVX_POSTGRES_TEST_DSN`, `-p 1`): handler 426 s ok, repo 500 s ok, contract 617 s ok. go-gost/x `landing` + `socket`
  ok (TestRemoveCoreRefusals needs a short TMPDIR: unix socket path limit, environment only). `npm run build` ok.
- Rollback rehearsal: fork.175 code (2458af9f) opens the migrated prod-DB copy (vendor columns + values) and renders every
  plan byte-identical to before (work/render-rollback175.json).
