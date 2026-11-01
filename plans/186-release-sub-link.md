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
- [x] Gates on release head: backend build/vet, go test SQLite + PostgreSQL, frontend tsc/build/lint (compare with fork.104)
  - build ok; vet = same 5 leikwan/testapi findings as fork.104; tsc 0; vite build ok; eslint 94 errors = fork.104
  - `TestExternalNodesBothDialects` failed on fork.104 too (hard-coded 2026-10-31 expiry) → test-only fix f7108278;
    after it: 20/20 packages on SQLite and PostgreSQL
- [x] Push branch, tag `3.0.27-fork.105`, CI + Images (backend/frontend amd64+arm64) green, release assets verified
  - tag → 01e4efdc; CI 41310238085, Images 41310239366; Latest, 10 assets, gost sha256 ok, ghcr amd64+arm64
- [x] Baselines: panel CPU/RSS, health, node online count, real-rule client check, prod state snapshot
- [x] Panel backup (validated DB) + rollback dir + image tags
- [x] Upgrade `/opt/flvx-svc` → fork.105, version + health, new tables present, prune backups to 2
  - 11-01 21:35 TW; rollback `/opt/flvx-svc/rollback/pre-fork105-20261101T133019Z/rollback.sh` (no --restore-db); kept pre-fork104 + pre-fork105
- [x] Post-checks: sub pool, test subscription, clash / sing-box / Shadowrocket / browser UA pulls + headers, 404, 0 `/s/` log lines
- [x] Cloudflare: `/s/` not cached, correct content-type; browser page shows https URL + QR
- [x] Prod screenshots 390 + 1440 of 订阅 page (admin, light) with the test subscription
- [x] Delete test subscription, verify 0 leftovers
- [x] Existing pages load (rules / landing / tunnel), menu badge, real-rule client check unchanged, node state unchanged
- [x] Resources after upgrade (CPU/RSS vs baseline, non-request log lines in 10 min)
  - CPU 15.67 → 12.57 s/5 min, RSS 43.4 → 47.9 MiB, quiet non-request lines 7/10 min (one nft sync at +10 min), then 0
- [x] Summary (final.md / summary.md), plan marked complete, commit + push

**Status: COMPLETE** — prod runs 3.0.27-fork.105 since 2026-11-01 21:35 TW (panel only, no OTA).
