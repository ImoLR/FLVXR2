# 257 — release fork.132 (plan 256 订阅二期 sub-rules), panel only

Base: tag `3.0.27-fork.131` (99393cda). Branch `maintenance/3.0.27-fork.132`, worktree `/root/projects/flvxx/flvxx-rel132`.
Merged: `feature/3.0.27-sub-rules` @ 68d9521b (fast-forward; code b8a1f8a2). NOT plan 255 line-monitor.
Run dir `/root/flvx-workers/runs/release-fork132`. User approved screens 11-14 TW (「没问题」, :8791 /sub-rules/).

Scope: panel only, no agent OTA. DB: GORM adds nullable TEXT `user_subscription.routing` (NULL = fork.131 output).
Rollback = fork.131 image swap; column stays inert.

## Tasks
- [x] caches cleaned, worktree + ff merge, plan doc
- [x] gates: go-backend build/vet/test (SQLite + PG 18), go-gost/x tests, go-gost build, `npm run build`
- [x] push branch, CI green, tag `3.0.27-fork.132`, Images workflow, release Latest + assets validated
- [x] pre-state: subs (count, no routing), nodes, rules, panel RSS; temp admin sub + 6 UA pulls via CF
- [x] panel backup + rollback dir, prune backups, upgrade /opt/flvx-svc, health + nodes, column check
- [x] post: temp sub bodies byte-identical; real subs routing NULL + count unchanged
- [x] routed temp sub (恢复默认 + 自己写 + node: exit): Clash/sing-box/Surge pulls, mihomo -t, sing-box check, /s/rs/ 200 via CF
- [x] mainland mihomo run on leikwan-sh (rule sets via DIRECT, AI + CN domain groups)
- [x] UI smoke prod admin (外部节点 (N), 分流 tab, H5 390)
- [x] delete temp sub; nodes/rules unchanged, logs clean, RSS
- [x] ledger 256 → 已发布, memory notes, cleanup worktrees + caches, summary, plan complete

## Results
- Gates (11-14 23:07–23:20 TW): go-backend build/vet ok, `go test ./...` SQLite all ok; temp PG 18 repo/handler/contract ok;
  go-gost/x all ok (landing first failed only because TMPDIR made the unix socket path > 108 chars — rerun with short
  TMPDIR ok; no go-gost change in this release); go-gost build ok; `npm run build` ok. Branch CI 43608453764 green.
- Release: tag `3.0.27-fork.132` → 980984c3; CI 43609729014 + Images 43609730129 green; Latest, not prerelease, same 10 assets
  as fork.131, compose pinned, PINNED_VERSION/REPO ok, gost sha256 + `-V` ok.
- Pre (fork.131): 3 real subs (users 60 / 1 / 78), no routing column; 28/28 nodes online, 51 rules all enabled, panel
  backend 45 MiB. Temp admin sub 22 「临时测试132」 (2 landing 直连 nodes) pulled via CF with clash-verge / mihomo /
  sing-box / Surge / Stash / v2rayN (bodies stable across two pulls).
- Backup `/opt/flvx-svc/rollback/pre-fork132-20261114T153333Z` (quick/integrity ok, local image tags, rollback.sh);
  prune → kept pre-fork131 + pre-fork132. Upgrade 23:33:52–23:34:02 TW, healthy, oldpanel mount ro ok.
- Post: 6 UA bodies byte-identical; column `routing TEXT` added, real subs NULL, last_pull unchanged, count unchanged.
- Routed (defaults 8 + 自己写 3 lines → node:l:97): Clash 7271 B / sing-box 9791 B / Surge 3761 B / Stash 6500 B;
  mihomo 1.19.32 `-t` ok, sing-box 1.14.3 `check` ok; 58 `/s/rs/` URLs via CF all 200 (geosite-ai .mrs/.srs/.list/.txt).
- Mainland leikwan-sh: pull via CF 2.8 s, 14/14 rule providers from flvxx.imgamer.top via DIRECT in 7.1 s;
  www.baidu.com → GeoSite(cn) DIRECT 200; chatgpt.com → RuleSet(geosite-ai) 🤖 AI (node itself timed out from SH — not
  in scope, routing correct).
- UI smoke prod admin: 「外部节点 (1)」 (temp external node), 分流 tab loads desktop + H5 390, no horizontal scroll,
  no visual overlap, 0 console errors.
- Temp sub 22 + temp external node deleted (token 404, landing_external_node 0). Nodes 28/28 online, rules/chains
  identical, backend log 0 errors, backend 97.6 MiB right after restart (45 MiB before, long-running).
