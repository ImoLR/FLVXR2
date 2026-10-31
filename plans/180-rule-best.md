# 180 — rule-best: rule list shows the current 最优 pick + 「规则最优」 (exit→target in the score)

Branch `feature/3.0.27-rule-best` from tag `3.0.27-fork.102` + cherry-pick of plan 179 (5 ms margin).
BUILD ONLY (no tag / release / prod writes / OTA). Approval: user 2026-10-31/11-01 (session fbed47e3).

## Design (short)

- **Tunnel switch** `tunnel.rule_best` (int, default 1, add-only, SQLite + PostgreSQL). ON + tunnel has ≥ 1 最优
  layer ⇒ rule-best; OFF ⇒ exactly the plan 165/179 tunnel 最优 (the chain config is byte-identical to fork.102).
- **Destinations** = the targets of the tunnel's gost rules (`forward.remote_addr` split + `processServerAddress`,
  exactly the `forwarder.nodes[].addr` the entry dials). Skipped (stay on the tunnel order): nftables / wg_path
  rules, paused rules, plan-108 chain rules (target = exit-local node, exit fixed by design).
- **Probes**: in the same 60 s round, one TCP probe per distinct (exit node, target host, port) — the same
  `probePathPair` / `TcpPing` (2 handshakes, RST close) as the edges; host passed as written (the agent resolves it
  like 诊断). A target unreachable from every exit is probed only every 5 min (back-off) and uses the tunnel decision.
- **Score** per (tunnel, owner, destination): edge(owner→cand) + best downstream to an exit + exit→destination.
  Same switch rules as plan 165/179 (≥ 5 ms in 2 rounds, failover at once), shared storm-guard caps.
- **Materialisation**: the owner's chain hop gets `metadata.destOrder = {"host:port": [addr…]}` only for the
  destinations whose decided first differs from the tunnel's first (same ⇒ no entry). Every chain builder goes
  through `tunnelChainConfigFor`, so create/edit/redeploy/reconnect/最优 pushes all carry it.
- **Agent** (option B): hop `destOrder` — when the dialed destination (`SelectOptions.Host`, else `Addr`, normalised
  host:port) matches, the eligible nodes are put in that order before the fail filter + fifo; otherwise the normal
  order. Failover unchanged (maxFails 1 / failTimeout 600 s, dead node skipped for that destination too). UpdateChains
  keeps node objects when only node order and/or destOrder change (in place). New handshake capability
  `destOrder=1`; the panel only sends destOrder to such agents (others: tunnel order).
- **Display**: the rule list items get `bestPick` (built from memory, no DB, no extra polling: rides the existing 15 s
  rule-list poll). Users: own rules only, node names only (no ids / IPs).

## Tasks

- [x] 1. Agent: hop `destOrder` parse + select + in-place update + capability `destOrder=1`; unit tests (select,
      failover, UDP, config with/without destOrder, in-place swap)
- [x] 2. Panel: `tunnel.rule_best` column (default 1) + create/update/list/get + form field; ws capability
      `destOrder`
- [x] 3. Panel: rule-best destinations + exit→target probes in the 60 s round (dedupe, 5 min back-off)
- [x] 4. Panel: rule-best decisions (score incl. exit→target, margin/2 rounds/failover, cleanup) + materialisation in
      `tunnelChainConfigFor` + push (one UpdateChains per changed owner, shared storm guard) + logs/counters
- [x] 5. Panel: switch OFF removes per-destination orders on that tunnel (push) ; tunnel edit / rule edit cleanup
- [x] 6. Panel: `bestPick` on the rule list (rule-level / tunnel decision, multi-entry / multi-target, states)
- [x] 7. Frontend: rule list pick line + tap popover (desktop + phone); tunnel form switch 「规则最优」
- [x] 8. Unit tests panel (cost, margin, failover, back-off, dedupe, cleanup, OFF byte-compare vs fork.102 for all
      prod-shaped tunnels, capability gating, multi-target, entry-bound rules)
- [x] 9. Full `go test ./...` SQLite + PostgreSQL; go-gost / go-gost/x touched packages; frontend tsc/build/lint
- [x] 10. netns proof (tc netem): per-target exits on one tunnel, switch after delay change, dead exit failover,
      long-lived conn survives pushes, old agent stays on tunnel order, OFF = fork.102
- [x] 11. Resource numbers (probes/round prod-like + 200 rules, round time, ss, panel CPU/RSS, agent select cost)
- [x] 12. Screens 390 + 1440 light + dark → gallery /root/mmwx-preview/rule-best/
- [x] 13. `runs/rule-best/release-notes.md` (OTA order, canary, capability check, pre/post client checks, prod
      proof with temp objects, rollback, kill switch)
- [ ] 14. Push branch, CI green, summary/final.md

## Results (2026-11-01 TW)

- Tests: backend `go test ./...` SQLite 20/20 packages ok, PostgreSQL (`FLVX_POSTGRES_TEST_DSN`) 20/20 ok; go-gost/x
  hop, socket, config, handler/forward ok; go-gost root ok; frontend tsc 0 errors, build ok, eslint errors 20 = 20 on
  fork.102 (none in new code).
- 228 chain configs of the 31 prod-shaped tunnels byte-identical to fork.102 with the switch off (unit test); lab:
  OFF tunnel agent configs identical between the new panel and the fork.102 panel; fork.102 boots on the migrated DB.
- netns lab (`runs/rule-best/lab`): same tunnel, rule A → 东京出口, rule B → 新加坡出口 (TCP + UDP); 2-target rule
  alternates per target; switch after a delay change in 2 rounds; dead preferred exit: new connections fail over at
  once; long-lived connections survived 5 push rounds and 2 panel restarts; fork.102 agent on a 中转 = tunnel order.
- Resources: prod copy 126 → 140 probes per round (+14); 200 seeded rules +208 (cap 300); decision 0.1 ms (prod) /
  0.5 ms (200 rules) per round; lab panel CPU per 5 min 2.94 s = fork.102 2.94 s, RSS 36.8 vs 36.4 MiB; TIME_WAIT 0.
- Screens: http://104.145.236.29:8791/rule-best/
