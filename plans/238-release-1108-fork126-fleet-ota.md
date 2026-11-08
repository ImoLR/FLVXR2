# 238 — release-1108: fork.126 (plan 237 门槛 1 分钟生效) + 全网 agent OTA (暂停规则重启不复活)

Base: `feature/3.0.27-margin-apply-now` @ 9751cc28 (= fork.125 bfb776f4 + plan 237). fork.126 = 237 head + this doc.
Branch `maintenance/3.0.27-fork.126-release-1108`. Run dir `/root/flvx-workers/runs/release-1108`.

User approval 2026-11-08 ~00:50 TW: ship 237, upgrade every online FLVX agent (fork.125 paused-rule fix rides along).
Skip Breadcloud JP2 (= mmwx-prod). Nobrand 5cm / 1cm: try, skip if offline/unreachable. Stop starting machines at 10:00 TW.

## Tasks
- [x] branch + plan doc, gates (backend build/test, go-gost/x landing + socket tests) vs base
- [x] push branch, branch CI green, tag `3.0.27-fork.126`, Images workflow, release Latest + 10 assets validated
- [x] panel backup + rollback dir (no schema change → image swap), upgrade /opt/flvx-svc, health + baseline compare
- [x] baseline before OTA (versions, CPU/RSS, conns, rules, landing links, subs, 救命版 read-only, TTFB)
- [x] OTA canary (one quiet exit + one entry), then busy entries one at a time, then groups ≤ 4
- [x] paused-rule check on every machine (0 paused ports listening)
- [x] margin 1-minute proof on one native landing (1 ms change and back via admin API)
- [x] after-baseline, per-machine table, restart blips
- [x] ledger 237 → 已发布, prune backups, summary

## Results (2026-11-08 TW)
- Tag `3.0.27-fork.126` → a5966795 (= 237 head 9751cc28 + this doc). Branch CI 42421808582 green, Images 42422121510 green,
  release Latest, 10 assets = fork.125 set, compose/install pinned to fork.126, gost-amd64 sha256 + `-V` ok.
- Gates (code identical to 237): backend build + vet ok, backend SQLite `go test ./...` rc=0, go-gost root ok,
  go-gost/x `go test ./...` rc=0 (the known flake TestSocketCapSharedByProbeAndPin passed this time). PG covered by CI + 237.
- Rollback rehearsal on a DB copy (no network): fork.126 → fork.125 both healthy, schema hash unchanged, integrity ok.
- Panel 06:59 TW → fork.126 (rollback `/opt/flvx-svc/rollback/pre-fork126-20261107T225923Z/rollback.sh`); 31/31 back.
- OTA 07:01–07:48 TW: all 30 FLVX agents → fork.126 (incl. Mkcloud 沪日ixp 440 / 沪港ixp 新春版 from fork.125,
  Nobrand 5cm from fork.103 without SSH backup, Dmit Echo without SSH). Skipped: Breadcloud JP2 (= mmwx-prod).
  Back online in 2.4–17 s (火山广州 38 s: slow download). Running-rule blips 0.7–1.8 s on entries, 0–0.7 s on 最优 pairs.
- Paused: temp paused admin rules on Leikwanhost 沪台IPLC + Mkcloud 深港ixp 1 and the 2 real paused rules on the
  Mkcloud entries: 0 open samples across each restart; resume/pause still instant. Fleet: 0 paused ports listening.
- Margin proof (目标 IP 20 → 21 → 20 via config/switch-margins/save): the 4 native landing agents had the new value in
  8–10 s and the old one back in 49–51 s; 0 pin moves, 0 extra switches. Settings back to 5/5/20.
- Business after = before (15 rules, 7 landing links, subs, 救命版 48/49). Temp rules/sub deleted, 0 leftovers.
