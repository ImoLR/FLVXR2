# 165 — 隧道每一跳「最优」= 按入口→出口整段延迟自动选路 (best-path-select)

User 2026-10-29 (TW night), session a49886d1: 「可以，就按你推荐的做吧，主要是注意效率的问题」 + later the same night
「把隧道的默认改成用这个最优」. BUILD ONLY (branch + tests + CI + screens); the batch release worker (fork.96) ships it.
Base: `origin/feature/3.0.27-line-compliance-ack` (163 + 164 on fork.95). Branch `feature/3.0.27-best-path-select`.

## Findings (fork.95 code, before this plan)
- `best` was only UI + unused scoring code. The chain builder passed `strategy: "best"` straight to the agent, whose selector
  parser maps unknown strategies to **round-robin** — so a `best` layer actually behaved as 轮询, not 主备.
- Agent `UpdateChains` = `Unregister` → `ParseChain` → `Register`. During that window `chainWrapper.Route` returns nil and the
  router falls back to `DefaultRoute` = **direct dial to the rule target (tunnel bypass)**. The panel's redeploy path
  (`DeleteChains` → `AddChains`) has the same window. Chain objects are not `io.Closer`, so a replace also leaves the old
  chain's mux sessions (mtls/mwss/mtcp) open — one idle TCP connection per replace.
- nft-mode rules on a chained tunnel use `resolveChainNextHop` (flat DB rows), not the gost chain → unaffected, stay static.
- WireGuard paths do not use `chain_tunnel` strategies → unaffected.

## Design
- Decision engine (panel, memory only) runs at the end of every 60 s path-latency round, from that round's per-edge results
  (0 extra probes). Cost backwards from the exits: cost(exit)=0; cost(n)=min over OK edges (latency + cost(next)).
  For each owner (node of the layer before a `best` layer with ≥ 2 nodes) rank candidates by edge + cost; failed/unreachable last.
- Switch the first place only if a challenger is ≥ 15 ms faster than the current first in 2 consecutive rounds (switch to that
  round's best). Current first unreachable (edge failed after the round's retry, or downstream unreachable) → switch at once to
  the best reachable. New order = ranking (best first, rest by score, unreachable last). Only first-place changes push.
- Runtime selector for `best` = `fifo` (maxFails=1/failTimeout=600 s unchanged) → dead first node still skipped immediately.
- Every chain push for an owner (switch, reconnect diff, tunnel edit/create, entry split/merge) builds targets in the current
  in-memory order; panel restart starts from DB order. No DB writes.
- Agent (capability `chainSwap=1` in the WS handshake): `UpdateChains` reorders the existing hop's nodes in place when only the
  node order differs (same node objects: no gap, no new connections, mux sessions and fail marks kept); any other change →
  parse first, then one atomic registry `Store` (no window). Owners without the capability (old agent) → static DB order,
  never reordered (no gap risk). For capable agents the panel's redeploy path also uses `UpdateChains` instead of
  `DeleteChains` + `AddChains`.
- Static (never reordered): federation remote owners (`IsRemote`), LeiKwan pipe entries (single target), nft-mode rules, WG paths.
  Remote nodes as candidates of a local owner are fine (only our own agent's chain order changes).
- Storm guard: one push per (tunnel, owner) per round, ≤ 10 pushes per owner node per round, ≤ 40 per round globally; failovers
  first; deferred switches stay confirmed and go next round. Counters in the hourly path-latency summary line.
- Logs: one line per actual switch; hourly summary gets `best_owners/switches/deferred/push_fail`.
- Default = 最优: new tunnels, new hop groups and the exit group default to `best` (UI + `tunnelCreate` with empty strategy).
  Existing rows untouched.
- Display (admin only): per `best` layer a tinted chip 「最优：<node> · NN ms」 (tap → per-owner list), 「最优：等待探测」 until
  the first decision, old agent → 「最优：节点需升级」. Hint under the strategy select.

## Checklist
- [x] Investigate fork.95 code paths (path-latency round, chain builders, agent chain update, capability handshake, nft/WG/federation)
- [x] Agent: atomic `UpdateChains` (in-place reorder / atomic replace) + `chainSwap=1` capability + tests
- [x] Panel: capability parse (`ChainSwapCapable`), best→fifo runtime mapping, ordered targets in every chain builder, UpdateChains on capable redeploys
- [x] Panel: decision engine (cost, 15 ms × 2 rounds, failover, storm guard, cleanup) + switch push + logs/summary
- [x] Panel: default `best` on tunnel create paths
- [x] Display state per best layer (admin) + frontend chips/hint + form defaults
- [x] Unit tests (ranking example 10/100 vs 30/20, threshold, 2 rounds, failover, push-only-on-change, reconnect/resync order, cleanup) + benchmark (200×3×3)
- [x] Reconnect diff: chain-only `UpdateChains` when only the chain differs on a chainSwap agent (panel restart after a switch never touches services)
- [x] `go test ./...` SQLite + PostgreSQL, agent tests, frontend tsc/lint/build
- [x] Real proof: local panel + agents in netns with `tc netem` (switch, long-lived conn survives, dead node)
- [x] Screens 390 + 1440 light/dark → `/root/mmwx-preview/best-path-select/`
- [x] Release notes for the batch worker (`runs/best-path-select/release-notes.md`)
- [x] Commit, push, CI green (run 40960010981: backend, PostgreSQL contract, frontend, agent all success)
- [x] Summary (`runs/best-path-select/summary.md` + `final.md`)

## Results
- Tests: backend `go test ./...` SQLite 19/19 packages ok, PostgreSQL (`FLVX_POSTGRES_TEST_DSN`, throwaway postgres:18)
  19/19 ok; agent `go test ./socket ./hop ./registry ./chain ./config/... ./selector/...` ok (incl. `-race` on the swap tests);
  frontend tsc 0 errors, eslint on changed files: tunnel.tsx 4 errors = same 4 as the base (jsx-a11y, untouched lines),
  +20 prettier warnings only on changed lines inside the already mis-indented form block; vite build ok.
- Agent gap evidence: old `Unregister`→`Register` with 4 concurrent dialers × 600 updates: 259,144 of 266,000 lookups found
  no chain (router → direct dial); new `updateChain`: 0 misses (`TestUpdateChainNeverLeavesAWindowWithoutRoute`).
- Lab (netns, real panel + 5 agents, tc netem, 60 s rounds): hop layer switched after exactly 2 rounds
  (`H1 111ms -> H2 50ms`), new connections moved to H2, a long-lived connection stayed on H1 (413/413 echoes over all
  switches); 10 ms advantage → 0 switches in 200 s; exit layer switched (`X1 80ms -> X2 25ms`), new connections exit X2, an
  open one stayed on X1; dead node → failover at the next round without the 2-round wait (`H2 unreachable -> H1 56ms`);
  panel restart → only H2 (which had switched) got one `UpdateChains`, no service delete/add, others unchanged.
- Resources: decision step 1.7–1.8 ms CPU and 1.3 MB transient allocations per 60 s round for 200 tunnels × 3 最优 layers
  × 3 nodes (1,400 owners); state ≈ 246 B per owner, dropped when the tunnel/layer/owner goes; 0 probes and 0 connections
  added (reorder = same node objects); logs: one line per switch + counters in the existing hourly line; no DB writes.
- Screens (28, 28/28 checks, 0 page errors): `/root/mmwx-preview/best-path-select/` (:8791).
