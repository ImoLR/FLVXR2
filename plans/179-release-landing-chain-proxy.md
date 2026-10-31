# Plan 179 — release-landing-chain-proxy: ship plan 177 as `3.0.27-fork.102` (panel only)

User approval 2026-10-31 TW (session 5f390821) after the gallery http://104.145.236.29:8791/landing-chain-proxy/:
「可以，发布吧」. Open choices keep the worker defaults (8 distinct outbounds per landing server, no GOMEMLIMIT / agent change,
rule-dialog 「我自己的节点」 picker not in this release).

Branch `maintenance/3.0.27-fork.102-landing-chain-proxy` from tag `3.0.27-fork.101` + `--no-ff` merge of
`origin/feature/3.0.27-landing-chain-proxy` (based on fork.100; no file overlap with fork.101 — verified with
`git diff --name-only`). Plan 178 (tunnel-group-tree) is NOT included. Panel only, no agent OTA. DB migration additive
(new table `landing_external_node`, new column `landing_node.chain_external_id` default 0).
Run dir `/root/flvx-workers/runs/release-landing-chain-proxy`.

## Checklist
- [x] 1. Branch + merge (no conflicts, 0 overlapping files with fork.101)
- [x] 2. Gates: go build, backend tests SQLite + PostgreSQL, frontend tsc / build / lint (no new errors vs fork.101)
- [x] 3. Pre-release prod snapshot (nodes, forward_port, per-node runtime objects, landing egress hashes, backend RSS/CPU)
- [x] 4. Push, CI green, tag `3.0.27-fork.102`, Images green (yaml.v3 resolves in Docker build), release validated
- [x] 5. Panel backup + rollback dir, upgrade `/opt/flvx-svc`, health + version.json, prune backups (keep 2)
- [x] 6. After: nodes online as before, forward_port + runtime objects identical, landing egress hashes identical, no Core re-apply
- [x] 7. DB: table + column exist, all `chain_external_id` = 0; non-admin `landing/external/list` → empty, cap 20
- [x] 8. Prod read-only UI checks 390 + 1440 (admin + non-admin), sidebar shows fork.102
- [x] 9. Resources: backend RSS/CPU before/after, 0 new non-request log lines in 10 min
- [x] 10. Plan complete commit + summary

## Notes

### Gates
- `go build ./...` rc 0; `go vet` = the same pre-existing `leikwan/testapi` unkeyed-field notes as fork.101 (5 lines both).
- `go test -p 1 ./...` SQLite 20/20 packages ok, PostgreSQL 20/20 ok, 0 failing tests (fork.101 had 19 packages;
  +1 = new `internal/landingsub`).
- Frontend: `tsc --noEmit` 0 errors; `vite build` ok; `eslint src` 94 errors / 3723 warnings (fork.101: 94 / 3723).
- Branch CI 41207215608 green (backend, agent, frontend, PostgreSQL contract). Images 41209424951 green — backend
  Docker build resolves `gopkg.in/yaml.v3`. validate-release 13/13 (Latest, 10 assets = fork.101 set, pins, sha256,
  ghcr amd64+arm64). Tag `3.0.27-fork.102` → 464ae25c.

### Upgrade (22:34 TW)
- Backup `/opt/flvx-svc/rollback/pre-fork102-20261031T143412Z` (quick_check + integrity_check ok); healthy in 14 s,
  version.json `3.0.27-fork.102-mvwhmk24`. Prune removed pre-fork100 dir/tags + fork.99 images; kept pre-fork101 + pre-fork102.
- State diff pre → post: 30 → 30 online, forward_port 38 = 38 identical, forwards identical, runtime objects 240 = 240
  identical, 0 orphans / missing. Agents unchanged (29× fork.98, node 36 fork.95).
- Landing: `egress_hash` identical on all 6 servers; shared servers 2/3/4/6 rows identical (desired hash, pushed/applied
  revision). Native servers 1/5: Core pid + `config.json` md5 on nodes 21/55 identical (no Core restart, no config change).
  Their `revision` rose 355→357 / 128→131 and `conn_mode_sig` `ul=` changed transiently, then returned to the exact
  pre-upgrade `ul=92cbd0f637f036cb` within ~1 min: `landingRelaySources` resolves relay host names asynchronously and the
  cache is empty right after any backend restart (plan 158 code, unchanged in this release).
- Reconnect redeploy: 2 nodes (18, 21) got `tunnels updated=3/1` — best-path (plan 165) decisions are in memory only, so
  after any restart the DB order is pushed once; no best-path switch needed afterwards. Pre-existing, unchanged here.
- DB: `landing_external_node` exists (0 rows); `landing_node.chain_external_id` exists, indexed, 4/4 rows = 0.
  Non-admin users 8 and 26 `landing/external/list` → `{cap: 20, count: 0, items: []}`.
- UI (read-only, writes intercepted): 35/35 checks, 0 page errors; 出站/card labels identical to the fork.101 capture
  (admin 4 rows, user 8 1 card, 390 + 1440); 「链式代理」 on every row/card; dialog defaults to 直连; 添加外部节点 tabs
  render; no horizontal scroll; sidebar `当前版本 3.0.27-fork.102`, no update dot.
- Optional live chain test skipped: the only admin-owned landing node is 「自己用kr」 (NexKR, may be in real use); server 1
  has no node. Chain E2E was proven in the plan 177 lab.

### Resources (backend)
- Before (fork.101, 3 samples): CPU 5.5–7.1 %, container mem 44.5–45.4 MiB. DB 57.3 MB.
- After 10 min (fork.102): CPU 2.6–8.6 %, process VmRSS 46.8 MiB (anon 25.4 MiB; container figure 76 MiB incl. ~49 MiB
  active file cache from the backup / integrity reads). DB 57.3 MB unchanged. 0 container restarts.
- Logs in the first 10 min: 78 lines; non-request lines only startup (4), node_metric maintenance (1), one Cloudflare
  reconnect of node 45, the pre-existing rule 98 DNS refresh (nft.debug), 30 reconnect redeploy lines — 0 new kinds.
