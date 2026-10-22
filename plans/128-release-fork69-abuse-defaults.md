# 128 · fork.69 + fork.70 — release plan 127 (landing abuse defaults + Reality 防盗)

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
- [x] Shared servers 2/3/4: server 3 got the plan at 18:37:34 TW (rules `flvxr-1/2` hot-applied, Core PID unchanged, no
      Helper restart); 2/4 have 0 nodes → plan hash unchanged, defaults appear with the first node (temp node on 4: `flvxr-1/2`
      present, Core not restarted); all three FLVX Cores running, user node 10 on server 3 active throughout
- [x] Node 21 canary OTA (SSH backup `/root/flvxx-backup-20261022T103815Z-pre-fork69`): `landing_guard: 1`, wss→wss, diagnose 27/27; 「待重启」 confirmed (0 nodes) → Core restarted with the first Reality node
- [x] Live check on node 21: temp VLESS-Reality node — wrong SNI closed, right SNI → dest cert, client works,
      TCP 25 blocked, 443 ok; temp node deleted (fork.69: no-SNI still relayed → fork.70 fix below; fork.70: all pass)
- [x] Live check on server 3 (provider allows :25): fork.68 baseline reached the :25 fixture, fork.69 only :2525 arrives,
      443 ok; temp node deleted. Extra temp node on server 4 (embedded) for the rules, deleted
- [x] Resource numbers (panel, node 21 agent + Core, shared Core) before/after — see summary
- [x] Summary + plan marked complete

## fork.70 follow-up: guard let no-SNI / non-TLS probes through (found in the live check)
Live check on node 21 (fork.69): wrong SNI and bare-domain SNI closed, but a no-SNI ClientHello was answered by the
real dest. Cause: production dests are domains (www.lovelive-anime.jp:443 = the serverName); when sniffing finds no
SNI (no-SNI ClientHello, plain bytes) the Core matches the allow rule's `full:` domain against the dokodemo target
itself → direct. The plan-127 E2E used an IP dest, so it never saw this. Fix: allow rule also requires sniffed
`protocol: tls` (local repro with the real Core: no-SNI and plain now blocked, right SNI unchanged).
- [x] Fix `go-gost/x/landing/guard.go` + golden in `guard_test.go` + real-Core test `guard_real_test.go` (domain dest;
      fails without the fix, passes with it); landing 42 pass / 2 opt-in skips (incl. netns E2E), socket/service ok
- [x] Commit bb333eec, CI 39766604365/39766592828, tag `3.0.27-fork.70`, Images 39766606421, release check 13/13
- [x] Panel → fork.70 too (panel refuses OTA to a version other than its own; code identical to fork.69), rollback
      `pre-fork70-20261022T110621Z` (kept + pre-fork69); OTA node 21 → fork.70 (SSH backup `…T110643Z-pre-fork70`), wss→wss,
      diagnose 27/27; live check all pass (no-SNI now closed)

