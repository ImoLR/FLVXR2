# 318 — VPS 厂商 belongs to the NODE (required, multi), no guessing

User 11-27 (follow-up of plan 316 「屏蔽本机厂商官网」, fork.176). Build only,
screenshots → user approval → separate release task.

## Decisions (user, final)

- No guessing: `SuggestVendor`, `vendorSuggest`, 「用建议」 and the
  suggestion-only aliases are removed. The stored choice is the only source.
- The vendor belongs to the **node** (the VPS). Node create / edit has a
  **required, multi-select** 「VPS 厂商」 (火山广州 / 火山上海 = Leikwanhost +
  火山引擎). Domains live only in 管理厂商. Last option 「其他（列表里没有）」 +
  name box → red admin ⚠ 「厂商「X」不在管理厂商列表，请先在管理厂商补齐域名，
  再回节点选择」; no auto-link when a vendor with that name is added later.
- The rule follows the node: a node that is (or becomes) a native landing gets
  `内置：屏蔽本机厂商官网（A、B）` = MinimalDomains(union of its vendors), first
  plan rule, same markers / SNI handling as plan 316. A vendor change on the
  node re-syncs its landing.
- mmwx shares keep `landing_server.vendor` and its select.
- Old nodes are backfilled by us on prod (release task).
- New researched vendor list (17): existing 14 keys keep their names (rule
  names stay byte-identical) + mkcloud, datawave, nobrand.
- AWS = console / sign-in / billing only.

## Design

- **Storage:** `node.vendors` (text, comma list of vendor keys in pick order,
  '' = none) + `node.vendor_other` (varchar 64, the 「其他」 name, '' = none),
  `json:"-"`, admin views only. SQLite + PG via AutoMigrate.
- **Migration (every start, idempotent):** a native `landing_server.vendor`
  is copied to its node when the node has neither vendors nor 其他.
- **Rollback mirror:** `landing_server.vendor` of a native landing = first
  node vendor ('' when none) — written on every node vendor change and when
  a native landing row is created (enable / chain-only exit). fork.176 renders
  that single vendor after a rollback.
- **Compile:** native → node vendors (unknown keys skipped, 其他 ignored, so
  a node with Leikwanhost + 其他 still blocks Leikwanhost); shared →
  `landing_server.vendor`. Union ≤ 64 names (agent `maxVendorHosts`), checked
  when a node is saved and when the vendor list is saved.
- **Required:** UI + admin API `node/create` refuse no vendor; `node/update`
  refuses an empty pick when `vendors` is sent (the UI always sends it, so an
  old node without a vendor cannot be saved without picking one); absent =
  keep (old API callers). Internal paths (federation remote nodes, backup
  import) create the node without a vendor → ⚠.
- **APIs:** `node/vendor/options` (admin: vendor list for the dialog);
  `node/vendor/set` (admin: vendor fields only — the backfill, one node at a
  time, no other node field re-saved);
  `landing/egress/vendor/assign` and `server/save` vendor only for shares
  (native → message: edit the node).
- **UI:** node dialog multi-select + 其他 box; node card / list ⚠ (no vendor =
  amber, 其他 = red); landing card ⚠ via `serverAdminAttention`; server 设置
  shows the node's vendors read-only (native) / select (shares); 管理厂商:
  per-landing read-only view (native) + select (shares) + the list editor +
  nodes that need a vendor.
- **Agent:** unchanged (SNI complement regexp is derived from the rendered
  `domain:` names). No OTA.

## Checklist

- [x] Plan doc
- [x] landingegress: new seed list (17), drop SuggestVendor/aliases, CombineVendors + limits + tests
- [x] model + migration + repo (node vendors, mirror, refs, vendor-list save checks)
- [x] Compile from node vendors (native) / server vendor (shared) + tests
- [x] Handlers: node create/update/list/options, assign/server-save share-only, admin views, resync + tests
- [x] Contract tests: required on create, user view has no vendor fields (extend), node list strip
- [x] Frontend: node dialog, node card/list ⚠, landing ⚠, server 设置, 管理厂商, 内置 row
- [x] go vet + go test SQLite + PG, npm run build
- [x] Prod-DB copy: migrate + backfill + render before/after diff, 158 byte-identical, rollback (fork.176 on migrated copy)
- [x] Screenshots gallery (desktop + 390 px, light + dark)
- [ ] Push branch, CI Build Check green, ledger row (等用户看截图), memory, summary

## Evidence (build, 11-27)

- Prod-DB copy (11-27 16:16 TW, read-only backup API copy), `unshare -n`:
  - fork.176 code render = before; plan-318 code opens it (AutoMigrate + node vendor migration: the 8 native
    landings' nodes get bestvm ×2 / boil ×2 / fachost / nexkr / pqs / tcscloud), then the backfill table (33 nodes,
    25 set, 8 already equal) → render = after. Admin view after backfill: 0 nodes need a vendor.
  - Diff after vs before (work/render-diff.txt): only the vendor rule's domains on 4 servers — Dmit T1 (share)
    +dmit.com; Bestvm US / Bestvm JP B +vmsilo.com, +xwan.world; PQS TW (native) +pqs.cloud, +pni.com.tw. Rule
    names, placement, markers, tags, outbounds, warnings unchanged. Boil Hinet 158, NexKR, Boil Hinet 马年,
    Boil HKT 99u, Fachost seednet 黑五, Tcscloud Seednet and the dead share rows: byte-identical.
  - The same 4 servers already change right after the panel swap (new seed list); the backfill itself only touches
    the 25 non-landing nodes (no landing re-sync).
  - Rollback: fork.176 code on the migrated + backfilled copy renders byte-identical to before (mirror
    `landing_server.vendor` = first node vendor).
- Agent: SNI complement regexp is built from the rendered `domain:` names (`go-gost/x/landing/vendor.go`
  vendorSets / sniffExcludeRegexp, max 64 names per rule) → panel-only, no OTA.
- Lab (prod-DB copy, `unshare -n`, mmwx token blanked): 火山广州 (Leikwanhost + 火山引擎) enabled as a landing
  through the normal 启用为落地 API → its plan carries `内置：屏蔽本机厂商官网（Leikwanhost、火山引擎）` with
  leikwanhost.com, volcengine.cn, volcengine.com at once; mirror = leikwan.
- Gallery: http://104.145.236.29:8791/vendor-node-pick/ (68 PNG, desktop / 390 px / dark).
- Tests (round 2, 11-27 17:04–18:00 TW, `-p 1`, TMPDIR off /tmp): `go vet ./...` ok. SQLite: all 28 other
  packages ok, handler ok (276 s), contract 1st run: `TestForwardWarmTagContract` failed once with
  「部分节点不在线」 at tunnel create = known race (the test dials the agent websocket and creates the tunnel
  without waiting for node.status = 1; same failure in the fork.138 release gates, plan 318 only adds
  `vendors` to its node/create). It passed alone ×5 and ×60 (`-cpu 1`), and the full contract re-run was ok
  (232 s). PG 18 (`FLVX_POSTGRES_TEST_DSN`): repo ok (594 s), handler ok (472 s), contract ok (686 s).
  `npm run build` (tsc + vite) ok.
- Live prod node list re-checked read-only 11-27 17:28 TW: 33 nodes = the 33 rows of `backfill.json`, no new
  or renamed node → no node left without a vendor after the backfill.

## Release recipe (follow-up task, after the user approves the screenshots)

1. Rebase / merge onto the prod branch head (fork.176 = 7b5aab50, or later if 317 ships first), next free fork,
   CI + Images green, 10 assets, gost sha256 (fork-release-and-deploy).
2. Prod backup + rollback dir (`/opt/flvx-svc/rollback/pre-forkNNN-*/rollback.sh` → fork.176 image swap), then
   `/root/flvx-workers/prune-backups.sh`. Record every native landing's Core PID first.
3. Swap. AutoMigrate adds node.vendors / node.vendor_other; the startup migration copies the 8 native landing
   vendors to their nodes. The new seed list hot-updates 4 landings: Bestvm US, Bestvm JP B, PQS TW (native,
   hot RoutingService update, Core PID must stay) and Dmit T1 (share, mmwx Custom hot rule). Check Core PIDs.
4. Backfill: `/root/flvx-workers/runs/vendor-node-pick/work/backfill-prod.py` (+ `backfill.json`; dry run) → `--apply` (admin API `node/vendor/set`, one node at a time,
   2 s apart, names re-checked; unlisted new nodes stay empty with ⚠ and go in the summary).
5. Post-upgrade node checks (`briefs/snippets/post-upgrade-node-check.md`) on Bestvm US, Bestvm JP B, PQS TW and
   the Dmit T1 share; live check through the matching landing: dmit.com (Dmit T1), vmsilo.com (Bestvm US / JP B),
   pqs.cloud (PQS TW) refused, google OK, latency / speed unchanged.
6. Rollback = rollback.sh (fork.176 renders the same as today; node.vendors stays in the DB, unused).
