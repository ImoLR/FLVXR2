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
- [ ] push branch, branch CI green, tag `3.0.27-fork.129`, Images workflow, release Latest + assets validated
- [ ] panel backup + rollback dir (image swap; DB column additive), upgrade /opt/flvx-svc, health + node state
- [ ] relay services re-pushed with `readTimeout: 130s` in place, no error spam, business check pre == post
- [ ] OTA canary Mkcloud 沪日ixp 440 + middles/exits of tunnels 25/28 (backup, RSS/fd before/after, pools ≤2 per key)
- [ ] OTA the rest one host at a time (SSH backup each)
- [ ] Leikwanhost 沪台IPLC watch (LeiKwan conn-limit / errors)
- [ ] per-host pool counts vs estimate; logs on entry/middle/exit (no per-stream lines)
- [ ] ~1 h: per-tunnel 预热 tag report vs step-1 numbers; native landing list for `landing-core-restart`
- [ ] ledger 244+245 → 已发布, prune backups, cache cleanup, summary, plan complete

## Results
- Gates (11-12 18:10 TW): go-backend build ok, vet only the old RouteRef unkeyed warnings (same as fork.127/128),
  `go test ./...` all ok; go-gost/x warmpool, listener/tls, chain, handler/relay, socket, landing ok; go-gost build ok.
  Disk 9.9 G free.
