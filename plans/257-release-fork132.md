# 257 — release fork.132 (plan 256 订阅二期 sub-rules), panel only

Base: tag `3.0.27-fork.131` (99393cda). Branch `maintenance/3.0.27-fork.132`, worktree `/root/projects/flvxx/flvxx-rel132`.
Merged: `feature/3.0.27-sub-rules` @ 68d9521b (fast-forward; code b8a1f8a2). NOT plan 255 line-monitor.
Run dir `/root/flvx-workers/runs/release-fork132`. User approved screens 11-14 TW (「没问题」, :8791 /sub-rules/).

Scope: panel only, no agent OTA. DB: GORM adds nullable TEXT `user_subscription.routing` (NULL = fork.131 output).
Rollback = fork.131 image swap; column stays inert.

## Tasks
- [x] caches cleaned, worktree + ff merge, plan doc
- [x] gates: go-backend build/vet/test (SQLite + PG 18), go-gost/x tests, go-gost build, `npm run build`
- [ ] push branch, CI green, tag `3.0.27-fork.132`, Images workflow, release Latest + assets validated
- [ ] pre-state: subs (count, no routing), nodes, rules, panel RSS; temp admin sub + 6 UA pulls via CF
- [ ] panel backup + rollback dir, prune backups, upgrade /opt/flvx-svc, health + nodes, column check
- [ ] post: temp sub bodies byte-identical; real subs routing NULL + count unchanged
- [ ] routed temp sub (恢复默认 + 自己写 + node: exit): Clash/sing-box/Surge pulls, mihomo -t, sing-box check, /s/rs/ 200 via CF
- [ ] mainland mihomo run on leikwan-sh (rule sets via DIRECT, AI + CN domain groups)
- [ ] UI smoke prod admin (外部节点 (N), 分流 tab, H5 390)
- [ ] delete temp sub; nodes/rules unchanged, logs clean, RSS
- [ ] ledger 256 → 已发布, memory notes, cleanup worktrees + caches, summary, plan complete

## Results
- Gates (11-14 23:07–23:20 TW): go-backend build/vet ok, `go test ./...` SQLite all ok; temp PG 18 repo/handler/contract ok;
  go-gost/x all ok (landing first failed only because TMPDIR made the unix socket path > 108 chars — rerun with short
  TMPDIR ok; no go-gost change in this release); go-gost build ok; `npm run build` ok. Branch CI 43608453764 green.
