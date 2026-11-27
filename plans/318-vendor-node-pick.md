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
- [ ] Frontend: node dialog, node card/list ⚠, landing ⚠, server 设置, 管理厂商, 内置 row
- [ ] go vet + go test SQLite + PG, npm run build
- [x] Prod-DB copy: migrate + backfill + render before/after diff, 158 byte-identical, rollback (fork.176 on migrated copy)
- [ ] Screenshots gallery (desktop + 390 px, light + dark)
- [ ] Push branch, CI Build Check green, ledger row (等用户看截图), memory, summary
