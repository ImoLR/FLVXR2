# 181 — release-batch-1101: plan 178 + 179 + 180 as 3.0.27-fork.103 (panel + OTA all nodes)

Approval: user 2026-11-01 TW (session 46344050) — release 178 r4 + 179 + 180 r1/r2 together, no further screen review.
Branch `maintenance/3.0.27-fork.103-batch-1101` = `feature/3.0.27-rule-best` (fork.102 + 179 e28bb0db + 180 r1/r2)
+ merge `feature/3.0.27-tunnel-group-tree` (178 r4). Run dir `/root/flvx-workers/runs/release-batch-1101`.

## Tasks
- [x] Preconditions: rule-best r2 reviewer final.md present, CI green on 7b5a91da; 178 CI green on 1cd17589; prod = fork.102
- [x] Release branch + merge 178 (clean auto-merge, verify forward.tsx / globals.css keep both behaviours); 179 present once
- [x] Gates: go build (backend, go-gost, x), backend tests SQLite + PostgreSQL, agent tests, frontend tsc / build / lint
- [x] Local UI smoke (demo panel): 隧道页 region mode + 规则页 groups / tree line / bestPick / admin auto-off chip+pill (390 + 1440)
- [x] Migration dry-run on a prod DB copy (tunnel.rule_best, forward.rule_best_off_*)
- [x] Client checks before the panel upgrade (real rules table + temp admin rules to public targets)
- [x] Tag 3.0.27-fork.103, CI + Images + release assets verified
- [x] Panel backup (validated DB) + rollback dir; upgrade /opt/flvx-svc; health + version; prune backups
- [x] Client checks after the panel upgrade; long-lived connection across the restart
- [x] Prod checks (rule_best=1 everywhere, rule_best_off_sig '', failTimeout 60 s in chains, no auto-off chip, round line counters)
- [x] OTA all nodes in order: 22 → 47/15/18/21/42 → rest → entries 2/23/32/49 (never 36); client checks after each entry
- [x] 178 post-deploy checks + prod screenshots 390 + 1440
- [x] Resources before/after (backend RSS/CPU, agent RSS on 3 nodes, non-request log lines 10 min)
- [x] Summary (final.md / summary.md) + plan complete

## Results (pre-release)
- Merge of 178 into 180 auto-merged cleanly (forward.tsx / tunnel.tsx / globals.css); 179 = e28bb0db exactly once.
- Gates: backend build ok; go vet = same 5 pre-existing `unkeyed fields` notes as fork.102; backend tests SQLite 20/20 ok,
  PostgreSQL 20/20 ok; go-gost + x build ok; x tests ok (x/landing needs a short TMPDIR for its unix socket — env only);
  tsc 0 errors; vite build ok; eslint 94 errors = fork.102 base 94, 0 new.
- Smoke (prod DB copy, no-network namespace, bestPick/ruleBestOff injected for 3 rules): all checks pass at 390 + 1440, 0 page errors.
- Migration dry-run on the prod DB copy: tunnel.rule_best INTEGER NOT NULL DEFAULT 1 (41/41 = 1), forward.rule_best_off_* added,
  all '' ; quick_check ok.

## Results (release, 2026-11-01 TW)
- Tag `3.0.27-fork.103` → d6cf84b2; CI Build Check 41246983905 green; Build and Push Images 41246991490 green; release Latest,
  10 assets = fork.102 set, pins + sha256 + ghcr amd64/arm64 verified.
- Panel: backup `/opt/flvx-svc/rollback/pre-fork103-20261031T222224Z` (validated DB), upgrade 06:22 TW, healthy in 11 s;
  every agent reconnected with a diff-only redeploy (failed=0); chain hops failTimeout 60 s, forwarder selectors 600 s.
  Backups kept: pre-fork102 + pre-fork103.
- OTA 28 nodes fork.98 → fork.103 06:29–08:49 TW (canary 22 → 47/15/18/21/42 → rest → entries 2/23/32/49); node 36 untouched
  (fork.95), node 31 offline the whole time (skipped, still fork.98). Diagnose unchanged everywhere except node 52 tunnel 80
  (LeiKwan pipe PipePing flaky before the OTA too: ~70 failures / 24 h with the old agent).
- Client checks (10 real rules + 6 temp SS paths) identical before / after the panel / after each entry OTA.
- Rule-level 最优 live: tunnel 4 rules 102/103 → exit 15 (23 ms vs 36 ms); rules 37/69 auto-off at 07:24 (their landings were
  unreachable from both exits before the release).
- Watch item: plan 179 5 ms margin → tunnels 27/30 (0 rules) flap 12↔26 on owner 21, ~9 switches/h.
