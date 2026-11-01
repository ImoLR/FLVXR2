# 186 — Release plan 185 「订阅」 subscription links as 3.0.27-fork.105 (panel only)

User approved the plan 185 screens + defaults on 2026-11-01 ~20:25 TW (「这个没问题，按你计划的去发布吧」).
Release ONLY plan 185 (plan 184 latency-tag-region/align is not included). Panel only, **no OTA, no agent change**.

- Base: `3.0.27-fork.104` (prod). Source: `feature/3.0.27-sub-link` head f0ce31d5 (CI 41304827774 green).
- Release branch: `maintenance/3.0.27-fork.105-sub-link`; tag `3.0.27-fork.105`.
- DB: 2 additive tables (`user_subscription`, `user_sub_source`) via AutoMigrate; no existing table change.
- Frontend image rebuilt (nginx `location ^~ /s/`).
- Run dir: `/root/flvx-workers/runs/release-sub-link`.

## Checklist
- [x] Pre-checks: no other release running, prod = fork.104, latest tag fork.104, head CI green, plan 186 free
- [x] Release branch + this plan doc
- [ ] Gates on release head: backend build/vet, go test SQLite + PostgreSQL, frontend tsc/build/lint (compare with fork.104)
- [ ] Push branch, tag `3.0.27-fork.105`, CI + Images (backend/frontend amd64+arm64) green, release assets verified
- [ ] Baselines: panel CPU/RSS, health, node online count, real-rule client check, prod state snapshot
- [ ] Panel backup (validated DB) + rollback dir + image tags
- [ ] Upgrade `/opt/flvx-svc` → fork.105, version + health, new tables present, prune backups to 2
- [ ] Post-checks: sub pool, test subscription, clash / sing-box / Shadowrocket / browser UA pulls + headers, 404, 0 `/s/` log lines
- [ ] Cloudflare: `/s/` not cached, correct content-type; browser page shows https URL + QR
- [ ] Prod screenshots 390 + 1440 of 订阅 page (admin, light) with the test subscription
- [ ] Delete test subscription, verify 0 leftovers
- [ ] Existing pages load (rules / landing / tunnel), menu badge, real-rule client check unchanged, node state unchanged
- [ ] Resources after upgrade (CPU/RSS vs baseline, non-request log lines in 10 min)
- [ ] Summary (final.md / summary.md), plan marked complete, commit + push
