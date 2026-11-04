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
- [x] Push branch + tag `3.0.27-fork.111` (→ f1b808d7, 10:29 TW); CI 41761043940 + Images 41761046395 green; release Latest, 10 assets = fork.110 set, pins + sha256 + ghcr amd64/arm64 ok
- [x] Panel backup `pre-fork111-20261104T024420Z` (DB quick/integrity ok) → upgraded 10:44 TW, healthy, 救命版 ro mount re-applied; pruned (kept pre-fork110 + pre-fork111)
- [x] Business check after panel = baseline (12 real rules / 8 entries, 6 temp rules, 5 landing links, 29/29, 253 runtime objects, 救命版 unchanged; old agents get no scan_guard)
- [x] OTA Breadcloud JP 10:48 TW (SSH backup first, back in 6 s) + Core restart confirm 10:49 (0/222 port probes failed) → v1.4.83 running, scan_guard.v1, 生效中; rules through JP + 大妈日本落地 pass
- [x] Scan test on temp node: loopback targets never counted (agent flvx-safety blocks 127/8 + private before the dialer) → vertical on JP's own IP: blocked at port 30, panel disabled in 4 s, event row; **解除禁用 did not take effect (revision reused) → plan 204 fix → fork.112**
- [x] fork.112 (plan 204) panel 11:41 TW; JP scan test re-run: 解除禁用 → traffic back in 1 s
- [x] OTA NexKR 11:50 TW → fork.112 (SSH backup first, back in 6 s), Core restart confirm 11:51 (2/123 probes failed ≈ 0.5 s) → v1.4.83, 生效中; 自己用kr passes; scan test passed (block → 解除禁用 1 s → delete)
- [x] Final business check = baseline (10 real rules / 8 entries — 123/124 SSH targets dropped from probing; 6 temp; 5 landing links), 救命版 unchanged, panel CPU 11.05→13.47 s/5 min (quiet; past range 11–14), RSS 59→50 MiB, 2 non-request lines/10 min; JP/NexKR agent+Core ≈ baseline, journals quiet
- [x] Summary (run dir final.md / summary.md), plan marked complete

**DONE 2026-11-04 12:15 TW** — prod panel fork.112; agents: Breadcloud JP fork.111, NexKR fork.112 (same agent code), others unchanged; both landing Cores v1.4.83.
