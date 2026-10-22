# 128 · fork.69 — release plan 127 (landing abuse defaults + Reality 防盗)

User 2026-10-22 evening approved plan 127: 「防盗那个你就开吧，防盗用那个也开」 — Reality 防盗 stays DEFAULT ON,
「防滥用」 preset keeps 23/135-139/445 on TCP + UDP. No other behaviour change.

Base: `feature/3.0.27-landing-abuse-defaults` head e723973a (on fork.68 eb047757). Branch
`maintenance/3.0.27-fork.69-abuse-defaults`. Run dir `/root/flvx-workers/runs/release-fork69-abuse-defaults`.

## Tasks
- [x] Branch cut, plan 127 doc marked released (stray tool-markup lines removed)
- [x] Push branch, CI Build Check green (39762977455)
- [x] Tag `3.0.27-fork.69` (→ 728ab56d), Build and Push Images 39762989276 green, release Latest, same 10 assets as fork.68
- [x] Prod panel backup `pre-fork69-20261022T103708Z` (DB quick/integrity ok), prune kept pre-fork68 + pre-fork69
- [x] Prod panel upgrade 18:37 TW, healthy, `/version.json` 3.0.27-fork.69-mvjedno9, 7 add-only columns present, 29/29 online
- [ ] Shared servers 2/3/4: new plan accepted, FLVX Core running, FLVX nodes reachable, 25+BT in plan
- [x] Node 21 canary OTA (SSH backup `/root/flvxx-backup-20261022T103815Z-pre-fork69`): `landing_guard: 1`, wss→wss, diagnose 27/27; 「待重启」 confirmed (0 nodes) → Core restarted with the first Reality node
- [ ] Live check on node 21: temp VLESS-Reality node — wrong SNI closed, right SNI → dest cert, client works,
      TCP 25 blocked, 443 ok; temp node deleted
- [ ] Live check on one shared server: temp node, 25 blocked / 443 ok; temp node deleted
- [ ] Resource numbers (panel, node 21 agent + Core, shared Core) before/after
- [ ] Summary + plan marked complete

## fork.70 follow-up: guard let no-SNI / non-TLS probes through (found in the live check)
Live check on node 21 (fork.69): wrong SNI and bare-domain SNI closed, but a no-SNI ClientHello was answered by the
real dest. Cause: production dests are domains (www.lovelive-anime.jp:443 = the serverName); when sniffing finds no
SNI (no-SNI ClientHello, plain bytes) the Core matches the allow rule's `full:` domain against the dokodemo target
itself → direct. The plan-127 E2E used an IP dest, so it never saw this. Fix: allow rule also requires sniffed
`protocol: tls` (local repro with the real Core: no-SNI and plain now blocked, right SNI unchanged).
- [x] Fix `go-gost/x/landing/guard.go` + golden in `guard_test.go` + real-Core test `guard_real_test.go` (domain dest;
      fails without the fix, passes with it); landing 42 pass / 2 opt-in skips (incl. netns E2E), socket/service ok
- [ ] Commit, CI, tag `3.0.27-fork.70`, release check
- [ ] OTA node 21 to fork.70 (agent-only; panel stays fork.69 — backend/frontend unchanged), live check again

