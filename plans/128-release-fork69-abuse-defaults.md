# 128 · fork.69 — release plan 127 (landing abuse defaults + Reality 防盗)

User 2026-10-22 evening approved plan 127: 「防盗那个你就开吧，防盗用那个也开」 — Reality 防盗 stays DEFAULT ON,
「防滥用」 preset keeps 23/135-139/445 on TCP + UDP. No other behaviour change.

Base: `feature/3.0.27-landing-abuse-defaults` head e723973a (on fork.68 eb047757). Branch
`maintenance/3.0.27-fork.69-abuse-defaults`. Run dir `/root/flvx-workers/runs/release-fork69-abuse-defaults`.

## Tasks
- [x] Branch cut, plan 127 doc marked released (stray tool-markup lines removed)
- [ ] Push branch, CI Build Check green
- [ ] Tag `3.0.27-fork.69`, Build and Push Images green, release Latest with the same asset set as fork.68
- [ ] Prod panel backup `pre-fork69-*` (validated DB, .env, compose, image tags), prune to 2 backups
- [ ] Prod panel upgrade, health/version, add-only columns present, 29/29 nodes online
- [ ] Shared servers 2/3/4: new plan accepted, FLVX Core running, FLVX nodes reachable, 25+BT in plan
- [ ] Node 21 canary OTA (SSH backup first): `landing_guard: 1`, landing Core healthy, 「待重启」 handled
- [ ] Live check on node 21: temp VLESS-Reality node — wrong SNI closed, right SNI → dest cert, client works,
      TCP 25 blocked, 443 ok; temp node deleted
- [ ] Live check on one shared server: temp node, 25 blocked / 443 ok; temp node deleted
- [ ] Resource numbers (panel, node 21 agent + Core, shared Core) before/after
- [ ] Summary + plan marked complete
