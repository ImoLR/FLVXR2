# 190 — Release batch 1102 (plans 184 + 187 + 188 + 189) as 3.0.27-fork.106 (panel only)

User 2026-11-02 (coordinator 6e28c3e5): 「截图出来之后就跟之前的那些一起全部发布吧，不用等我确认了」.
User 11-02: probe switch (`login_monitor_link`, 公开探针页) must be **OFF** after the release — nobody, admin included, can
open it (「先保持关闭，只有管理员可以使用，暂时先不要开放」). Never switched on on prod.
Panel only, **no OTA, no agent change**.

- Base: `3.0.27-fork.105` (prod), branch `maintenance/3.0.27-fork.105-sub-link` 3aea6ee7.
- Merged: `feature/3.0.27-latency-tag-region` 39a4826d (184 r1+r2+r3, variant B) + `feature/3.0.27-probe-page` ca117a33
  (187 + 188 + 189). Both merges clean.
- Release branch: `maintenance/3.0.27-fork.106-batch-1102`; tag `3.0.27-fork.106`.
- DB: additive column `tunnel.region_override` (184); probe config rows only written on save (187/189).
- Frontend image rebuilt (nginx `location = /api/v1/probe/ws`, `location ^~ /api/v1/probe/`).
- Run dir: `/root/flvx-workers/runs/release-batch-1102`.

## Checklist
- [x] Pre-checks: no other release running, prod = fork.105, latest tag fork.105, both heads CI green, 189 final.md clean, plan 190 free
- [x] Release branch (184 + probe-page merged onto fork.105) + this plan doc
- [x] Gates on release head: backend build/vet, go test SQLite + PostgreSQL, frontend tsc/build/lint (compare with fork.105)
  - build ok; vet = same 5 leikwan/testapi findings as fork.105; go test 22/22 packages SQLite and 22/22 PostgreSQL
    (contract package on PG 340 s); tsc 0; vite build ok; eslint 94 errors, same files/counts as fork.105
- [ ] Push branch, tag `3.0.27-fork.106`, CI + Images (backend/frontend amd64+arm64) green, release assets verified, nginx probe locations in image
- [ ] Baselines: panel CPU/RSS, health, nodes online / forwards / connections / traffic counters, tunnel list dump, real-rule client check, `login_monitor_link` value
- [ ] Panel backup (validated DB) + rollback dir + image tags
- [ ] Upgrade `/opt/flvx-svc` → fork.106, version + health, prune backups to 2
- [ ] Probe switch OFF (turn off if it was on); `/tz` 「探针未开启」, snapshot/series 404, WS refused (anon + admin, via CF)
- [ ] 184 data step (5 tunnels via POST /tunnel/region) + verify list diff, user view, no node push
- [ ] Post-checks: 监控 page (no history fetch without detail, detail charts, 👁), rules / landing / tunnel / 订阅 load, probe settings card loads
- [ ] Real-rule client check after upgrade vs before; node state / counters vs before
- [ ] Prod screenshots 390 + 1440 (监控 list, rules page ms tag, probe settings card)
- [ ] Resources after upgrade (CPU/RSS vs baseline, non-request log lines in 10 min)
- [ ] Summary (final.md / summary.md), plan marked complete, commit + push
