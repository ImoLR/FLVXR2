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
- [ ] Push branch → CI Build Check green; tag `3.0.27-fork.107` → Build and Push Images green
- [ ] Release validated: Latest, not prerelease, same asset set as fork.106, pins, gost sha256, ghcr amd64+arm64
- [ ] Baselines: panel CPU/RSS, health, nodes online, real-rule client check, runtime state snapshot
- [ ] Backup (rollback dir + validated DB + image tags) → upgrade `/opt/flvx-svc` → version + health → prune backups
- [ ] Post-checks via Cloudflare: temp admin subscription → Surge iOS/Mac, Surfboard, Loon, QX native; clash/sing-box/
      Shadowrocket unchanged; `?t=` overrides; browser HTML hints; headers; unknown token 404; 0 `/s/` log lines; delete, 0 leftovers
- [ ] Existing pages load (rules / landing / tunnel / 订阅); real rules pass traffic; nodes online; state identical
- [ ] Resources after upgrade vs baseline; non-request log lines in 10 min
- [ ] Mark plan complete
