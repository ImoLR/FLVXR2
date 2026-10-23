# 134 — landing-rule-layers: 全局·强制 / 节点规则 (user) / 服务器 / 全局·默认

Base: `feature/3.0.27-landing-node-limits` (plan 131 966386da, on plan 132 = fork.72 content, on fork.71). Branch
`feature/3.0.27-landing-rule-layers`. Build + tests + screenshots only — no release, no tag, no prod change, no OTA
(waits for the user's screenshot approval; brief /root/flvx-workers/briefs/landing-rule-layers.md).

## Goal (user 2026-10-22: 「可以就按你建议的这么做吧 … 逻辑你自己补齐一下」)
Landing routing gets layers like mmwx's global vs node split: a global group applied automatically to every landing
(native + mmwx-shared, incl. newly enabled), and node rules each user adds on their own landing nodes.

Evaluation order per node (first match wins, every rule scoped by inbound tag):
1. 系统内置 (locked, unchanged): safety, plan 127 anti-abuse defaults, source exemptions
2. 全局·强制 (admin) — every landing node incl. line-chain copies
3. 节点规则 (node owner; admin may edit) — own inbound tag only, not on line-chain nodes
4. 服务器规则 (admin) — the server's route profile, or the admin node override (now replaces only this layer)
5. 全局·默认 (admin) — every non-chain node
6. 默认出站 — node owner's choice > admin override profile default > server profile default (server has a profile)
   > 全局默认出站; line-chain nodes keep their own upstream

## Tasks
- [x] Plan doc + baseline backend suite on the unmodified base (15 FAIL, same list as plan 131:
      /root/flvx-workers/runs/landing-rule-layers/base.fails)
- [x] Models: `landing_egress_setting` global columns (强制/默认 rules text, 全局默认出站); `landing_outbound.user_allowed`;
      `landing_node.route_rules` / `route_rule_count` / `route_default`; `landing_user_setting` node-rule caps
      (value + custom flag ×2); global caps in vite_config (5 / 10); SQLite + PostgreSQL, no jsonb/serial
- [x] Compiler: layered `Compile` (L2 forced, L3 node, L4 server, L5 default, L6 default outbound precedence);
      byte-identical output when global is empty and no node rules (all existing goldens unchanged);
      line-chain tags get only L1+L2 (agent marker `builtin: "forced"`, landing_guard ≥ 2); global layers fail-open
      (`builtin: "global"`); WARP failure in a global layer → 直连 + warning; IPv4/IPv6-forced global part skipped on a
      server without that family + warning; node rules over the server budget skipped per node + warning
- [x] Agent (go-gost): GuardVersion 2 — accepts `builtin` forced/global (any outbound), forced rendered before chain
      rules, builtin rules never withhold nodes; tests
- [x] Handler/repo: global get/save (validate every server, debounce one apply per server), per-server sync status
      (已同步 / 同步中 / 失败 / 不支持（原因）/ 待重启 / warnings); node-rule get/save (owner or admin; caps + rate limit in
      one tx with the user row locked; non-published outbound refused; private-range still blocked by layer 1);
      owner transfer moves rules (refuse over target caps); node delete drops rules with the row; WARP ensure + upstream
      resolution + loop edges include global and node layers; user caps in landing user get/save (跟随全局/自定义)
- [x] Native routing-only change stays hot (RoutingService `adrules`), never a Core restart for node rules; servers
      whose Core still needs the one-time restart keep node rules queued (「下次重启生效」) and global layers wait like
      the abuse defaults
- [x] Frontend: 出站与路由 tab 「全局规则」 section (强制/默认 lists, 全局默认出站, sync status, 用户规则 caps, 匹配顺序
      legend); outbound 「允许用户使用」 checkbox; 路由规则 dialog (user node card + admin node details); user edit 「落地」
      two cap fields
- [x] Golden tests (layer order, line-chain, node scope, default precedence, empty global identical, IPv4-forced on
      IPv6-only, WARP fallback) + contract tests SQLite + PostgreSQL (caps, 20 parallel saves, authz, unpublished
      outbound, transfer, delete, global save → one apply per server)
- [x] Acceptance runner (local lab, real Core): global 强制 blocks a domain on two servers incl. a newly enabled one;
      user A 封锁 rule vs user B on the same Core; B's live connection survives A's edit; private block still wins
- [x] Resource numbers: Core RSS + apply time 0 vs (10 users × 10 node rules + 20 global incl. geosite/geoip); panel
      RSS/CPU before/after (Core 34.2 → 39.1 MiB, panel 36.6 → 36.6 MiB, user edit → applied 0.21 s, hot, same Core pid)
- [x] Runner repair: full local matrix was broken since plan 128 (iterative openssl REALITY dest stalled by the guard
      leg, S4–S8 cascade) and plan 127 (E1–E3 expected an empty default plan) — fixed, all 17 scenarios pass
- [x] Backend suite after (same 15 failures as base, 0 new; landing contracts SQLite + PostgreSQL pass), frontend
      tsc / lint (changed files 30 → 29 warnings, new files 0) / build, go-gost build + x/landing, x/socket, x/service tests
- [x] Screenshots desktop + H5, light + dark (63 shots, 63/63 checks, 0 page errors); gallery on mmwx-preview
      `/<token>/landing-rule-layers/`; H5 fixes: sync-state chip no longer stretched, legend highlight purple not blue
- [x] Push branch, CI green (39812740988, 39816125242), summary + final.md
      (/root/flvx-workers/runs/landing-rule-layers/). NOT released — waits for the user's screenshot approval; rebase on
      fork.73 before release; panel first, agent (GuardVersion 2) with a later OTA

## Release (fork.75, user approved 2026-10-23 ~13:30 TW 「这个规则这一边就赶紧发布吧」)
Plan 134 as built → `3.0.27-fork.75`, panel first, NO agent OTA (agent GuardVersion 2 ships with a later OTA).
Branch `maintenance/3.0.27-fork.75-landing-rule-layers` = `feature/3.0.27-landing-rule-layers` (ae0d53ff) rebased onto
`maintenance/3.0.27-fork.74-security-hotfix` (f0561ce4); the 131 commits (already in fork.73/74) dropped. Path-latency
cadence (F10) unchanged (user decision). Run dir `/root/flvx-workers/runs/release-fork75-landing-rule-layers`.

- [x] Rebase onto fork.74 (only conflict `.gitignore` comment), check overlap with fork.74 security code (only shared
      file handler.go: new config keys + debounce state; new admin routes behind `ensureAdminAccess`, node-rule save behind
      `limitMutation` + owner check; no ws/auth/login/metric code touched by 134)
- [x] `go build ./...`, backend suite vs fork.74 base (same 15 pre-existing failures), landing/ws/auth + contract
      tests SQLite + PostgreSQL, agent build/tests, frontend tsc + build + lint (no new errors vs fork.74), golden +
      empty-global byte-identical tests, local acceptance if RAM allows
      → backend `go test ./...` failure list identical to fork.74's 15; contract suite with PostgreSQL: 13 known
      failures only, every 134 contract `/postgres` subtest passes; go-gost/x `./landing/...` ok (needs a short
      TMPDIR: unix socket path limit); frontend tsc 0 errors, build ok, eslint 0 errors in the 11 files 134 touches;
      local acceptance 17/17 scenarios PASS (L1 17/17; L2 geosite merge check skipped, lab had no geo data)
- [x] Pre-upgrade prod capture (read-only, fork.74): compiled plan/config per landing server, sync status, counts,
      CPU/RSS, latency p95 → plus an offline check on a copy of the prod DB (mmwx token blanked, deleted after):
      fork.74 and fork.75 code compile byte-identical payloads for servers 1–4; fork.75 reproduces every stored
      egress_hash / shared_desired_hash and revision (no revision bump on upgrade)
- [x] Push branch, CI green
- [x] Tag `3.0.27-fork.75`, Images green, release Latest with the fork.74 asset set
- [x] Prod backup `pre-fork75-*` (validated SQLite, compose/.env, local image tags, rollback.sh) + prune to 2
- [x] Upgrade prod panel to fork.75, health + version.json
- [x] Verify: migration once, new tables/columns, global empty / default direct / caps 5/10, compiled plans identical
      before/after for every landing server, sync status no 失败, nodes/agents/forwards/landing counts unchanged, new UI
      strings in served bundle, CPU/RSS/DB/log lines after 10 min, latency p95

### Release results (2026-10-23, 台湾时间)
- Branch CI 39908566823 (c74b3c3c) + 39909828768 (688508e7) green; tag `3.0.27-fork.75` → 688508e7; Images
  39909837894 green; release Latest, not prerelease, 10 assets = fork.74 set, compose pinned
  `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.75`, install scripts PINNED_VERSION/REPO OK, gost sha256 OK, ghcr
  amd64+arm64.
- Rollback point `/opt/flvx-svc/rollback/pre-fork75-20261023T062917Z` (→ fork.74; SQLite quick/integrity ok, local
  image tags `local/flvxx-*:pre-fork75-20261023T062917Z`); kept + `pre-fork74-20261022T202252Z` (→ fork.73, restores
  MD5 hashes); pre-fork73 + fork.72 images pruned.
- Prod upgraded 14:29:25 → healthy 14:29:32 (7 s), version.json `3.0.27-fork.75-mvkkp5ez`; 29/29 online before and
  after, agents unchanged (21 = fork.70, 28 × fork.67, no OTA), forwards 34 (31 running), landing nodes 2 (row hash
  identical), landing servers 4.
- Migration: AutoMigrate only — landing_node +3 cols (route_rules '[]', route_rule_count 0, route_default ''),
  landing_user_setting +4 (user 26 row: rule caps follow global), landing_outbound +user_allowed, landing_egress_setting
  +4 global_* cols; no new table, no backfill, landing_egress_setting still empty → `global/get` = forced [], default [],
  defaultAction direct, caps 5/10, nodesWithRules 0.
- Zero behaviour change: after two sync cycles on fork.75 (shared pushes 14:32, node 21 desired.json rewritten 14:31)
  every landing_server fingerprint is unchanged — server 1 (native, node 21) egress_hash '' rev 253; servers 2/4
  (mmwx 7/10) shared_desired_hash 4f53cda1… rev 230/55; server 3 (mmwx 5, 2 nodes, egress applied) ec03a314… rev 178;
  pushed = applied revisions unchanged, no sync errors; node 21 desired/config/applied/api.json sha256 identical, Core
  PID 890894 unchanged. 全局规则 sync status: all 4 servers 已同步, no notes (the old-agent 线路机 note needs global
  强制 rules + line-chain nodes, prod has neither).
- Served bundle `index-D4-D7RvT.js` / `egress-Su6Rkvm2.js` contain 全局规则 / 路由规则 / 允许用户使用 / 节点规则 /
  全局·强制 / 全局·默认 / 匹配顺序.
- Live smoke on node 21 (temporary objects only): temp user 50 + SS2022 node 96 (port 30000); user saved one node
  rule 「封锁 example.com」 → applied, example.com blocked, example.org fine, a held echo connection through node 21
  survived, Core PID unchanged; node + user deleted, 0 leftover rows; server 1 desired back to `nodes: []` (content
  identical, revision 253 → 273).
- Resources (before fork.74 at 10 h uptime → fork.75 20 min): backend CPU 4.15 → 3.65 / 4.42 %, RSS 37.7 → 43.5 MiB
  (fork.74 fresh start was 44.3), DB 54.27 → 54.38 MB, logs 146 lines/h → 26 lines in a quiet 10 min (≈156/h,
  node-reconnect redeploys + stale-token summaries). p95 ms: landing/server/list 5.1 → 5.5–14 (noisy host),
  landing/node/list 3.8 → 3.5, node/list 8.5 → 8.0–10.4; admin-only egress/overview p50 3.0 → 5.7 (two extra small
  queries for template usage; called once per page open, never polled).
