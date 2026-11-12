# 246 — release-1112: fork.129 (plan 244 预热池 + 245 TLS accept concurrent) + 全网 agent OTA

Base: `feature/3.0.27-entry-warm-pool` @ d57eda21 (base fork.128 978fb216; 244 code + r2 UI layout B, 245 = 63185321).
Branch `maintenance/3.0.27-fork.129-warm-pool`. Run dir `/root/flvx-workers/runs/release-warm-pool`.
User approved release 11-12 ~16:30 TW (「按你推荐的去做」, layout B).

Brief: release as `3.0.27-fork.129`; panel upgrade with backup first (auto-migrate adds `tunnel.warm_pool` default 1;
rollback to fork.128 = image swap, extra column harmless); check relay services re-pushed with `readTimeout: 130s`
in place, no error spam. Fleet OTA of every online FLVX agent, canary = Mkcloud 沪日ixp 440 + its middle/exit for
tunnels 25/28, then one host at a time. Skip Breadcloud JP2 (never) + offline hosts (Nobrand ×2). Never 04:40–06:10
TW. Dmit Echo via panel OTA, never touch its flux_agent. Do NOT restart landing Cores (follow-up run
`landing-core-restart`). Leikwanhost 沪台IPLC keeps pool ON (watch LeiKwan side). ~1 h after: per-tunnel tag report.

## Tasks
- [x] branch + plan doc, gates (backend build/test, go-gost/x warmpool + tls listener + chain tests, go-gost build), disk ≥ 5 GB
- [x] push branch, branch CI green, tag `3.0.27-fork.129`, Images workflow, release Latest + assets validated
- [x] panel backup + rollback dir (image swap; DB column additive), upgrade /opt/flvx-svc, health + node state
- [x] relay services re-pushed with `readTimeout: 130s` in place, no error spam, business check pre == post
- [x] OTA canary Mkcloud 沪日ixp 440 + middles/exits of tunnels 25/28 (backup, RSS/fd before/after, pools ≤2 per key)
- [x] OTA the rest one host at a time (SSH backup each)
- [ ] Leikwanhost 沪台IPLC watch (LeiKwan conn-limit / errors)
- [ ] per-host pool counts vs estimate; logs on entry/middle/exit (no per-stream lines)
- [ ] ~1 h: per-tunnel 预热 tag report vs step-1 numbers; native landing list for `landing-core-restart`
- [ ] ledger 244+245 → 已发布, prune backups, cache cleanup, summary, plan complete

## Results
- Gates (11-12 18:10 TW): go-backend build ok, vet only the old RouteRef unkeyed warnings (same as fork.127/128),
  `go test ./...` all ok; go-gost/x warmpool, listener/tls, chain, handler/relay, socket, landing ok; go-gost build ok.
  Disk 9.9 G free.
- Release: branch CI 43197241775 green (4 jobs incl. PG contract); tag `3.0.27-fork.129` (annotated) → af408eee;
  Images 43197602297 success; release Latest, assets = fork.128 set, compose/install pinned, gost-amd64 sha256 + `-V` ok.
- Panel 18:25:23–18:25:34 TW fork.128 → fork.129 (healthy, 救命版 mount ro ok). Rollback point
  `/opt/flvx-svc/rollback/pre-fork129-20261112T102453Z/rollback.sh` (validated SQLite, image tags). Pruned pre-fork127.
  Migration: `tunnel.warm_pool INTEGER NOT NULL DEFAULT 1`, 50/50 tunnels = 1. 28/28 nodes online (Mkcloud 沪日ixp 440
  + Bestvm HK 年付 had shown 离线 since 01:59 TW while their WS was alive — pre-existing offline/online DB write race on
  a simultaneous reconnect; the panel restart cleared it). Redeploy after reconnect: all relay services updated in
  place, failed=0 on every node; Bestvm JP B 18 + Datawave HK 2 26 relay services all `readTimeout: 130s`, agent PIDs
  unchanged (no restart). Business check pre == post (45 rules; same 5 not-all-ok as at fork.128 = target side).
- OTA 18:28–18:58 TW, one host at a time, SSH backup each (`/root/flvxx-backup-<ts>-pre-fork129/restore-agent.sh`,
  list in run dir work/backups.txt): canary Mkcloud 沪日ixp 440 (5.7 s) → Bestvm JP B → Datawave HK 2 → Bestvm HK 年付,
  then 22 more; 26 agents on fork.129, all back in 4.7–12 s (火山广州 312 s: CN download, request timed out at 300 s,
  agent came up on its own). Skipped Nobrand 5cm IPLC 沪日 + Nobrand IPLC 沪港 (fork.126, user 11-07 leave alone).
  Agent RSS after restart 48–62 MB (before 25–131 MB), fd +0…+12; landing Core PIDs (Bestvm US 456239, NexKR 2144496)
  and every flux_agent PID unchanged.
- Canary pools: 440 → Bestvm JP B t28 middle ×2 + t25 exit ×2 + 端口转发 targets ×2 each; Bestvm JP B (t28 middle) →
  Datawave HK 2 ×2. Found: refill dials of 2 dead 端口转发 targets (rules 77/95) logged one error line per attempt →
  fix = plan 247 (feature/3.0.27-warm-dial-quiet d2da306b, build only, next agent release).
