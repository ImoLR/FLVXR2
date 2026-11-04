# 206 — release-1104b: fork.113 = plan 205 (shared 禁止端口扫描 + no recreate) + plan 201 (probe 表格 default)

Branch `maintenance/3.0.27-fork.113-release-1104b` = `feature/3.0.27-landing-scan-shared` (cb86c6cf) + merge 38227e33
(plan 201, frontend-only). Panel only, no OTA, no DB schema change. Run dir `/root/flvx-workers/runs/release-1104b`.

## Checklist
- [x] Build final.md clean (205 + mmwx helper-scan-guard v0.7.19 live)
- [x] Release branch + merge plan 201
- [ ] Gates on release head: go-backend `go test ./...` (sqlite + pg), frontend tsc + build
- [ ] Prod baselines: panel res/health, nodes online, rule groups, landing links, 救命版
- [ ] Push branch, CI green; tag `3.0.27-fork.113`, images + release assets verified
- [ ] Backup (DB) + rollback dir, prune backups (keep 2)
- [ ] Upgrade /opt/flvx-svc → fork.113, health + nodes
- [ ] Post checks = baseline; shared revisions applied, no 422/409
- [ ] 内置 禁止端口扫描: shared servers 「生效中」 / 「暂无节点」
- [ ] Shared scan test on temp admin node: detect → disable → 解除禁用 → delete
- [ ] Recreate block test on temp user: blocked until 解除禁用; cleanup
- [ ] /tz opens in 表格 at 1440 px and 390 px (fresh browser), TCP sort present
- [ ] Final checks, summary, plan marked complete
