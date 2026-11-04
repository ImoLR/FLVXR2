# 203 — release batch 1104 (fork.111): plan 200 + plan 202

Base `3.0.27-fork.110`. Branch `maintenance/3.0.27-fork.111-batch-1104`.
Content: plan 200 外部订阅源上限 2 (panel only) + plan 202 落地「禁止端口扫描」 (panel + agent; Core pin v1.4.83).
Rollout: panel → OTA Breadcloud JP (canary, admin-confirmed Core restart) → OTA NexKR. No other agent upgraded.
Run dir: `/root/flvx-workers/runs/release-batch-1104`.

## Checklist
- [x] Read inputs (200/202 final + release notes, Core v1.4.83 report, recipe); both clean → ship both
- [x] Free dev-box disk (Go build cache entries > 1 day: 9.1 → 3.7 GB; 1.4 → 6.8 GB free)
- [x] Release branch: fork.110 + 200 (ff) + 202 (merge, no conflicts)
- [x] Gates on release head: backend build ok / vet only the known leikwan/testapi unkeyed-field notes; go test SQLite 25/25 + PG 25/25 packages, 0 failures; go-gost build/vet/test ok; go-gost/x vet ok, test 19 pkgs ok + landing 62 pass (24 real-Core skips; run by the 202 worker) — first landing run failed only on the >108-char unix-socket TMPDIR path, re-run with a short TMPDIR passed; frontend tsc 0, build ok, eslint 94 errors / 3793 warnings = identical to fork.110 per file
- [x] Baselines (10:05–10:20 TW, files in run dir): panel CPU/RSS/health/online, client check per entry/rule group, landing links (JP, NexKR, shared ×3), 救命版
- [ ] Push branch + tag `3.0.27-fork.111`; CI + images + release assets verified
- [ ] Panel backup (DB) + rollback dir → upgrade `/opt/flvx-svc`; prune backups (keep 2)
- [ ] Business check after panel = baseline
- [ ] OTA Breadcloud JP (SSH backup first) + Core restart confirm; scan_guard.v1; temp test node scan trigger → block → 解除禁用 → delete; resources; logs 15 min
- [ ] OTA NexKR the same way
- [ ] Final business check + 救命版 + resources + logs 10 min
- [ ] Summary (final.md / summary.md), plan marked complete
