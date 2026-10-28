# 160 — release-batch-1028: fork.94 = plans 151 + 154 + 155 + 156 (panel only) + node 39 notice + tunnel split data step

User 2026-10-28: batch releases (1–2 per day). This batch ships, panel only (no agent OTA, `git diff fork.93 -- go-gost` empty):
- 151 backend-test-cleanup — backup import now refuses legacy 1.x per-type backups (was a silent "导入成功：{}"); test fixes.
- 154 node-admin-notice — admin-only 「节点提示」 (`node.admin_notice`, add-only) with a ⚠ badge on node cards.
- 155 tunnel-split-plain-names — plan-152 「入口显示名」/`{入口}` hints hidden (renderer + column kept), admin
  `tunnel/split-entry` / `merge-entry` endpoints; original multi-entry tunnel logic untouched.
- 156 tap-only-tooltips — chart tooltips + dropdown chips open on click/tap only (no hover / finger-swipe).

Base: `origin/feature/3.0.27-tap-only-tooltips` (65abbdd5) = fork.93 branch head 734abae3 + 151/154/155/156; no rebase
needed (no newer maintenance branch). Branch `maintenance/3.0.27-fork.94-batch-1028`, worktree
`/root/projects/flvxx/flvxx-release-batch-1028`. Run dir `/root/flvx-workers/runs/release-batch-1028`.

## Checklist
- [x] Read finals of 151/154/155/156 (all finished, tests green, CI green) — go/no-go: GO, all four ship
- [x] Gates on the release branch: `go build ./...` + vet OK, full `go test ./...` SQLite 19/19 + PostgreSQL 19/19 packages,
      0 failures; frontend tsc 0 errors, build OK (main JS 3.0 MB), eslint 96 errors = fork.93 base 96 (no new); branch CI green
- [x] Screenshot gate: galleries of 154 / 155 / 156 reviewed — no defect to fix. Note for the user: the 12 vmsilo twins inherit
      the tunnel-page folder 「Mkcloud 沪日ixp 440」 (admin-only label; user decides whether to move them to a new folder)
- [x] Fresh prod DB copy: new binary starts (migration add-only), 155 split dry-run + apply/undo rehearsal, 154 notice rehearsal
      (10-28 ~17:55 TW copy, release binary, netns panel + 30 fake agents: split 37/37 checks, node 23 0 pushes, users
      byte-identical; undo 26/26 identical; notice → 0 pushes, tunnel/forward tables identical)
- [x] Push branch, annotated tag `3.0.27-fork.94` (→ dfdec478), CI 40670079220 + Images 40670081592 green, release assets
      validated (Latest, not pre-release, same 10 assets as fork.93, compose pins, PINNED_VERSION/REPO, gost sha256, ghcr amd64+arm64)
- [x] Prod: rollback dir `/opt/flvx-svc/rollback/pre-fork94-20261028T102203Z` (validated DB backup quick/integrity ok, image
      tags, rollback.sh with --undo-split / --restore-db); pruned → kept pre-fork94 + pre-fork93 (pre-fork92 + fork.91 images removed)
- [x] Prod: upgraded `/opt/flvx-svc` to fork.94 at 18:22 TW, healthy, version.json `3.0.27-fork.94-mvryi59t`; 30/30 online;
      reconnect redeploys 29 nodes all `updated=0 failed=0`; forward_port 38 rows + per-node runtime objects (248, orphan-sweep
      dry-run) identical; node 23 nft ruleset hash identical; every user's picker / rule list / package identical (real JWTs)
- [x] Node 39 notice via admin API (18:23 TW, 219 chars): node/update result tunnelsUpdated 0 / rulesUpdated 0 / failures [];
      DB row changed only in admin_notice/updated_time; on node 39 gost.json/config.json mtimes, agent PID, journal unchanged;
      0 non-request backend log lines; per-node runtime objects identical
- [x] Plan 155 data step `--apply` 18:25 TW (undo file first: `pre-fork94-20261028T102203Z/data-step-155-20261028T102520Z/`):
      12 twins 92–103 (entry 51), originals 25–33/39/42/43 entry 23 only; VERIFY OK, 0 `{入口}` names, 0 multi-entry tunnels;
      forward_port 38 rows identical; node 23 runtime objects + nft ruleset hash identical (9/13 rule ports listening = the 9 gost
      rules, 3 nft rules in nft, rule 98 disabled — same as before); hop nodes only gained the expected new objects (12 +7, 14 +2,
      15 +1, 17 +1, 18 +13, 21 +7, 26 +7, 30 +1, 39 +1, 45 +2), node 51 11 chains swapped 25…→92…, 0 orphans / 0 missing
- [x] Real client (xray SS-2022 → entry → chain → landing 105 PQS TW → cloudflare trace): temp admin rules on tunnels 25/28/39
      entry 23 via CN host leikwan-sh: 9/9 before upgrade, 9/9 before split, 9/9 after split; temp rule on twin 92
      (vmsilo → Akari JP) listened on 51 only (forward_port [[51, port]], 23 closed / 51 open), 3/3 from this host's IPv6; all
      temp rules deleted, leftover []; panel 诊断 on rules 78/91/151 OK before/after; existing rules kept passing traffic
- [x] Users: 4 enabled users of these tunnels (4/7/8/11): names byte-identical, rule lists / packages identical, still 12
      originals and 0 twins (they were only ever granted entry 23 via group 沪日440; group 沪日IEPL has no grant). Only diff:
      tunnel 39 (port-forward type) exitRegions JP+CN → CN (its region comes from its entries). Users 10/12 disabled (401, as before).
      Admin: 沪日440 pill = 12 × `mk沪日ixp440 -> …`, 沪日IEPL pill = 12 × `vmsilo沪日首发 -> …`, tunnel page 24 plain names
- [x] Resources: node 23 agent unchanged (PID, RSS 145→144 MB, 9 TCP + 9 UDP listeners, CPU ~2 %); hop nodes RSS ±1 MB,
      listeners +1…+7 (idle), fds +1…+5, CPU 0.1–0.45 %, journal 0–3 lines / 10 min after the step; node 51 (no SSH) panel
      metrics mem 20.5 % → 20.5 %, CPU 0.65 → 0.77 %; backend 27–29 MiB, 3–5 % CPU, DB 57.3 MB, 0 log lines in the 10 min after;
      30/30 nodes reporting metrics, 0 orphans / 0 missing on all 30 nodes
- [x] Read-only prod screenshots (admin, writes aborted in the browser): node 39 ⚠ closed/open (desktop) + tapped (390),
      tunnel list (mk / vmsilo), rule picker 沪日IEPL / 沪日440 (desktop + 390), 监控 390 连接数: swipe scrolled 138 px with no
      tooltip, tap showed 「18:00 TCP 连接 : 99 UDP 连接 : 11」 → gallery :8791 /release-batch-1028/
- [x] final.md + summary.md; `docs(plan): mark fork.94 rollout complete`

## Rollback (order matters)
1. Panel only: `/opt/flvx-svc/rollback/pre-fork94-20261028T102203Z/rollback.sh` → fork.93 images; the split stays (24 plain
   one-entry tunnels work on fork.93), `node.admin_notice` stays unused.
2. Split too: `rollback.sh --undo-split` (runs the fork.94 merge-entry undo FIRST, then swaps images) — or data only, panel stays
   fork.94: `python3 …/data-step-155-20261028T102520Z/split_shared_tunnels.py --undo …/undo-20261028T102523Z.json`.
3. Notice: clear 「节点提示」 on node 39 in the node edit dialog.
4. Last resort `rollback.sh --restore-db` (DB back to 18:22 TW, loses later changes). Older point: `pre-fork93-20261024T233311Z`.

## Open (not fixed here)
- Tunnel 39 (port-forward type) now groups under 中国大陆 instead of 多地区 in users' pickers (exit region of a port-forward
  tunnel = its entry nodes' regions; 51 = JP was removed). Names / rules unchanged.
- The 12 vmsilo twins inherited the tunnel-page folder 「Mkcloud 沪日ixp 440」 (admin-only label); nobody is granted 沪日IEPL yet.
- Rule 124 「JP-SSH」 (nft, node 23 → 160.21.75.136:22) refuses connections from outside — the target refuses; pre-existing, untouched.
- Path-latency prober now also probes the twins' hop ports (≈66 → 85 short TCP connects per minute, mostly nodes 18/21).
- node/list still omits interfaceName (154 finding); 156 leaves the white focus ring on tapped charts; CI still runs no full go test.
