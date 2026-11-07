# 238 — release-1108: fork.126 (plan 237 门槛 1 分钟生效) + 全网 agent OTA (暂停规则重启不复活)

Base: `feature/3.0.27-margin-apply-now` @ 9751cc28 (= fork.125 bfb776f4 + plan 237). fork.126 = 237 head + this doc.
Branch `maintenance/3.0.27-fork.126-release-1108`. Run dir `/root/flvx-workers/runs/release-1108`.

User approval 2026-11-08 ~00:50 TW: ship 237, upgrade every online FLVX agent (fork.125 paused-rule fix rides along).
Skip Breadcloud JP2 (= mmwx-prod). Nobrand 5cm / 1cm: try, skip if offline/unreachable. Stop starting machines at 10:00 TW.

## Tasks
- [ ] branch + plan doc, gates (backend build/test, go-gost/x landing + socket tests) vs base
- [ ] push branch, branch CI green, tag `3.0.27-fork.126`, Images workflow, release Latest + 10 assets validated
- [ ] panel backup + rollback dir (no schema change → image swap), upgrade /opt/flvx-svc, health + baseline compare
- [ ] baseline before OTA (versions, CPU/RSS, conns, rules, landing links, subs, 救命版 read-only, TTFB)
- [ ] OTA canary (one quiet exit + one entry), then busy entries one at a time, then groups ≤ 4
- [ ] paused-rule check on every machine (0 paused ports listening)
- [ ] margin 1-minute proof on one native landing (1 ms change and back via admin API)
- [ ] after-baseline, per-machine table, restart blips
- [ ] ledger 237 → 已发布, prune backups, summary
