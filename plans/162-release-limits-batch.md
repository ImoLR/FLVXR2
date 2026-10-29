# Plan 162 — release-limits-batch: ship plans 157 + 158 + 159 + 161 as `3.0.27-fork.95`

Branch `maintenance/3.0.27-fork.95-limits` from `origin/feature/3.0.27-limits-landing-rate` (fc039b1a, on fork.94
dfdec478). Panel upgrade of `/opt/flvx-svc` + OTA of ALL online agents + NAT extra-IP data step + temp-object prod proofs.
Run dir `/root/flvx-workers/runs/release-limits-batch/`. Approval: user 2026-10-29 ~15:30 TW.

## Checklist
- [x] 0. Inputs read: 157/158/159/161 final.md + release-notes.md all say finished (build only); galleries exist.
- [ ] 1. Gates on the release branch: `go build ./...` + vet, backend tests SQLite + PostgreSQL, agent tests (go-gost/x, go-gost), frontend tsc / eslint / build; compare failures with the feature-branch runs.
- [ ] 2. Migration dry-run on a fresh prod DB copy with the new paneld (time it; orphan-cleanup log line vs 159 read-only SQL counts); 158/159 read-only SQL summary tables.
- [ ] 3. Screenshot gate: four galleries viewed; defects fixed on the release branch (re-shoot affected views only).
- [ ] 4. Tag `3.0.27-fork.95`, push; CI Build Check + Build and Push Images green; release validated (Latest, same asset set as fork.94, pins, sha256, ghcr arches).
- [ ] 5. Panel backup (`/opt/flvx-svc/rollback/pre-fork95-<TS>/`, validated DB copy, image tags, rollback.sh) → upgrade → health/version → prune backups (keep 2).
- [ ] 6. 30/30 online; forward_port rows + per-node runtime objects identical before/after; no rate/limit fields set (nothing throttled).
- [ ] 7. OTA all online agents → fork.95: 21 (SSH backup, RAM watch, kernel modules) → 55 → busy entry → the rest one at a time; node 26 only if stable; agent RSS/CPU/fds before/after on 21/55 + 2 entries.
- [ ] 8. NAT data step: nodes whose egress IP (panel-seen agent client IP) is not in address/extra IPs → add as extra IP via admin API (backup + undo file); prove no redeploy storm.
- [ ] 9. Prod proofs with temp objects only: (a) entry-row 速率 with two rules, (b) own-landing row 速率 on 21/55, (c) 入站连接数 on a row, (d) pool with two servers counts once; all temp objects deleted, kernel objects gone.
- [ ] 10. Resources: backend RSS/CPU, DB size, non-request log lines / 10 min; agents as in 7.
- [ ] 11. Read-only prod screenshots (admin): user edit dialog rows (entry / own landing / shared landing / relay 「不限」), 共享流量池.
- [ ] 12. final.md with 「验证证据」 table + summary.md (Chinese); plan marked complete.

## Results
(filled in as the steps finish)
