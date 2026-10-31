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
- [ ] 2. Gates: go build, backend tests SQLite + PostgreSQL, frontend tsc / build / lint (no new errors vs fork.101)
- [ ] 3. Pre-release prod snapshot (nodes, forward_port, per-node runtime objects, landing egress hashes, backend RSS/CPU)
- [ ] 4. Push, CI green, tag `3.0.27-fork.102`, Images green (yaml.v3 resolves in Docker build), release validated
- [ ] 5. Panel backup + rollback dir, upgrade `/opt/flvx-svc`, health + version.json, prune backups (keep 2)
- [ ] 6. After: nodes online as before, forward_port + runtime objects identical, landing egress hashes identical, no Core re-apply
- [ ] 7. DB: table + column exist, all `chain_external_id` = 0; non-admin `landing/external/list` → empty, cap 20
- [ ] 8. Prod read-only UI checks 390 + 1440 (admin + non-admin), sidebar shows fork.102
- [ ] 9. Resources: backend RSS/CPU before/after, 0 new non-request log lines in 10 min
- [ ] 10. Plan complete commit + summary

## Notes
