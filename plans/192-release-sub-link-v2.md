# 192 — Release plan 191 sub-link-v2 (Surge / Surfboard / Loon / Quantumult X) as 3.0.27-fork.107 (panel only)

User 2026-11-02 (coordinator 6f979d30): 「可以吧，然后按你推荐现在就可以发布吧」 — release plan 191 alone, now.
All open decisions keep the build defaults (all Surge incl. Mac skip Hysteria2-obfs; Surfboard keeps Snell + TLS obfs;
editor format unchanged). The two old issues (Clash render cost; landing 复制链接 Snell Surge line with TLS obfs) are NOT
fixed here. Panel only: **no OTA, no agent change, no DB change**. Prod settings unchanged (公开探针页 OFF, 订阅 switch as is).

- Base: `3.0.27-fork.106` (prod) → feature branch `feature/3.0.27-sub-link-v2` cd439f3a (10 commits, CI green).
- Release branch: `maintenance/3.0.27-fork.107-sub-link-v2`; tag `3.0.27-fork.107`.
- Run dir: `/root/flvx-workers/runs/release-sub-link-v2`.

## Checklist
- [x] Pre-checks: no other release running, prod = fork.106, latest tag fork.106, head CI green, plan 192 free
- [x] Release branch + this plan doc
- [x] Gates on release head: backend build/vet, go test SQLite + PostgreSQL, frontend tsc/build/lint (vs fork.106)
  - build ok; vet = same 5 leikwan/testapi findings; go test 22/22 packages ok SQLite and 22/22 PostgreSQL, 0 FAIL
  - frontend: `npm ci` refuses the lockfile with this host's npm (lockfile unchanged since fork.106) → `npm install
    --no-package-lock`; tsc ok, vite build ok, eslint 3887 problems (94 errors) = identical to fork.106, same file list
- [x] Push branch → CI Build Check green; tag `3.0.27-fork.107` → Build and Push Images green
  - tag → db582010; CI Build Check 41448032272 ✓, Build and Push Images 41448034208 ✓
- [x] Release validated: Latest, not prerelease, same asset set as fork.106, pins, gost sha256, ghcr amd64+arm64
  - 10 assets = fork.106 set; compose v6 differs from prod compose only in the two image tags
- [x] Baselines: panel CPU/RSS, health, nodes online, real-rule client check, runtime state snapshot
  - CPU 15.3 s/5 min, RSS 43.4 MiB, 29/29 online, 12 real rules reachable (258 gone/paused, 37 hinet 2/4 known), 251 runtime objects
- [x] Backup (rollback dir + validated DB + image tags) → upgrade `/opt/flvx-svc` → version + health → prune backups
  - rollback `/opt/flvx-svc/rollback/pre-fork107-20261102T130749Z` (quick/integrity ok); upgraded 21:08 TW, healthy,
    frontend build 3.0.27-fork.107-mvz9ffnn; kept pre-fork106 + pre-fork107 (pre-fork105 + fork.104 images pruned)
- [x] Post-checks via Cloudflare: temp admin subscription → Surge iOS/Mac, Surfboard, Loon, QX native; clash/sing-box/
      Shadowrocket unchanged; `?t=` overrides; browser HTML hints; headers; unknown token 404; 0 `/s/` log lines; delete, 0 leftovers
  - temp sub = admin landings 10 + 97 (SS2022); managed line `https://flvxx.imgamer.top/s/<tok>`; all `?t=` overrides OK
  - fork.106 vs fork.107 images on the same DB copy (--network none): clash/mihomo/sing-box/Shadowrocket/v2rayN/
    `?t=singbox`/`?t=base64` bodies byte-identical
  - 0 `/s/` and 0 token lines in logs; temp sub deleted, 0 rows left, old token 404
- [x] Existing pages load (rules / landing / tunnel / 订阅); real rules pass traffic; nodes online; state identical
  - 1440 + 390 pages load, 0 page errors; 29/29 online; forward_port 41 + 251 runtime objects identical, 0 orphans;
    same 12 rules reachable (113 intermittent ~1.2 s connect = SYN retransmit on node 48 path, 220 ms on re-test)
- [x] Resources after upgrade vs baseline; non-request log lines in 10 min
  - RSS 43.4 → 38–40 MiB; CPU 15.3 → 15.7 / 18.5 s per 5 min; quiet window 17 non-request lines (pre 10; same WS
    reconnect / diff-redeploy lines, all failed=0); DB size unchanged
- [x] Mark plan complete — production runs 3.0.27-fork.107 (panel only, no OTA, no DB change)
