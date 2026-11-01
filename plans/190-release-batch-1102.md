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
- [x] Push branch, tag `3.0.27-fork.106`, CI + Images (backend/frontend amd64+arm64) green, release assets verified, nginx probe locations in image
  - tag → 926bd86d; CI 41350711847, Images 41350723141; Latest, 10 assets = fork.105 set, gost sha256 ok, ghcr amd64+arm64;
    frontend image nginx.conf has `location = /api/v1/probe/ws` + `location ^~ /api/v1/probe/`, build 3.0.27-fork.106-mvybuz6t
- [x] Baselines: panel CPU/RSS, health, nodes online / forwards / connections / traffic counters, tunnel list dump, real-rule client check, `login_monitor_link` value
  - 29/29 online, 40 forwards (37 active), 249 runtime objects / 0 orphans; CPU 12.56 s/5 min, RSS 43.6 MiB;
    13 real rules on all 8 active entry nodes + 6 temp rules (4/25/64/28/39/92) 6/6 OK; `login_monitor_link` = false
- [x] Panel backup (validated DB) + rollback dir + image tags
  - `/opt/flvx-svc/rollback/pre-fork106-20261101T212745Z` (quick_check + integrity_check ok), local image tags pre-fork106
- [x] Upgrade `/opt/flvx-svc` → fork.106, version + health, prune backups to 2
  - 11-02 05:27:58 TW healthy, frontend build 3.0.27-fork.106-mvybuz6t; kept pre-fork105 + pre-fork106 (pre-fork104 + fork.103 images pruned)
- [x] Probe switch OFF (turn off if it was on); `/tz` 「探针未开启」, snapshot/series 404, WS refused (anon + admin, via CF)
  - was `false` before, still `false` (never switched on); CF anon + admin: snapshot/series 404 「探针未开启」, host 404, WS 404, /tz page 「探针未开启」 at 390 + 1440; off page re-checks once per 30 s
- [x] 184 data step (5 tunnels via POST /tunnel/region) + verify list diff, user view, no node push
  - 4/61→TW, 57/105→HK, 39→JP; exactly these 5 changed; tunnel/chain/forward/forward_port checksums unchanged; user 4 view has no autoExitRegions/regionOverride/IPs; no log line / push from the step; 「手动」 pills on the tunnel page
- [x] Post-checks: 监控 page (no history fetch without detail, detail charts, 👁), rules / landing / tunnel / 订阅 load, probe settings card loads
  - prodcheck 22/22: 0 history requests in 63 s (fork.105: 5), 👁 opens detail with chart, rules ms tags (green 26 ms 含落地, nested), no `x–y ms`, pages load, probe card loads (real data, not saved), no page errors
- [x] Real-rule client check after upgrade vs before; node state / counters vs before
  - 13 real rules on all 8 active entries: same signatures + diagnose; temp 6/6 at 10–13 Mbit/s (before 7–12); 29/29 online, 249 runtime objects identical, 0 orphans; restart = chain-only UpdateChains on nodes 2/32/21 (plan-165 最优 reset) + landing revision jitter (plan 158), both pre-existing
- [x] Prod screenshots 390 + 1440 (监控 list, rules page ms tag, probe settings card) — run dir `shots/`
- [x] Resources after upgrade (CPU/RSS vs baseline, non-request log lines in 10 min)
  - CPU 12.56 → 11.04 s/5 min, RSS 43.6 → 50.0 MiB, non-request lines 3 → 7 (one-off nft DNS resync of rule 98, as on fork.105), DB size unchanged
- [x] Summary (final.md / summary.md), plan marked complete, commit + push

**Status: COMPLETE** — prod runs 3.0.27-fork.106 since 2026-11-02 05:27 TW (panel only, no OTA). Probe switch OFF.
Rollback → fork.105: `/opt/flvx-svc/rollback/pre-fork106-20261101T212745Z/rollback.sh` (no --restore-db).
