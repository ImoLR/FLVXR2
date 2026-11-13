# 252 — chain-only cleanup: remove the idle chain-only landing server + Core (P2-6)

Base: tag `3.0.27-fork.130` (7464438a). Branch `feature/3.0.27-chain-only-cleanup`, worktree
`/root/projects/flvxx/flvxx-chain-cleanup`. Run dir `/root/flvx-workers/runs/chain-only-cleanup`.
BUILD ONLY (user 11-13 ~16:00 TW: build now, release together with the next FLVX batch — ledger row 252).

## Problem
A 「我自己的节点」 rule on a 线路机 tunnel whose exit has no landing server auto-creates a chain-only native
`landing_server` (`chain_only = 1`, `ensureChainHostTx`) and the exit agent installs the landing Core
(`flvx-landing-core.service` + `<agent dir>/landing`). When the last chain rule there is gone, the row and the Core stay
forever (idle Core, ~30–60 MB RSS + 58 MB on disk); no API removes them (plan 225 P2-6). Plan 251 removed the Bestvm JP B
residue by hand.

## Design (decisions + reasoning)
1. **Reconciler, not per-path hooks.** Every way the last chain copy disappears ends in the same DB state (0
   `forward_chain_node` rows for the server): rule delete / batch / force delete, switch to another target
   (`DeleteForwardChain`), exit moved off the tunnel (`CommitChainRetarget`), failed create (`FailForwardChain`), tunnel
   delete, user delete (cascades `deleteChainsTx`), orphan sweep. So one sweep keyed on that state covers all of them,
   including paths added later. Paused rules keep their copies (state active, only not rendered while paused) → never
   idle. Expired users / disabled rules are paused, not deleted → never idle.
   - Sweep every 10 min (first 3 min after start) + a **kick** from `syncChainServers` (every chain change ends there):
     a sweep 30 s later records "idle since", the cleanup runs once the server has been idle ≥ **10 min grace** (a
     delete + immediate recreate never flaps). Idle-since is in memory: a panel restart only restarts the grace.
   - Cost: one indexed query per sweep (`landing_server` chain-only rows with NOT EXISTS on `forward_chain_node`, index
     `idx_forward_chain_node_port` leads with `landing_server_id`); the full guard check and any node command only for
     idle candidates (none on prod today). No probes, no per-node polling.
2. **Never touch a used server.** Removable only if ALL hold, checked before and again inside the delete transaction
   (row lock `FOR UPDATE` on PG; SQLite = single connection): `source = native`, `chain_only = 1`, `status = active`
   (an admin-disabled one blocks chain rules on purpose → kept), `route_profile_id = 0` (admin egress config → kept),
   node ≠ 36, and zero rows in `forward_chain_node` (any chain state incl. creating / paused / pending retarget copies),
   `landing_node`, `landing_server_user`, `landing_server_user_group`, `landing_warp_account`, `landing_scan_event`,
   `user_server_quota` / `user_traffic_pool_member` (kind landing, this id). Shared servers are never chain-only.
   Prod `Bestvm US` (server 9, 1 active copy of sunflorian 「ATTVPS」) stays exactly as is — test shaped like it.
3. **Agent command `RemoveLandingCore {revision}`** (new; `landing_chain` 2 → **3** advertises it, so no DB column):
   refuses unless the agent's desired state is exactly that revision with **zero nodes** and no `flvxl-` inbound left
   applied; refuses if the unit file does not run this agent's own Core path. Then: `systemctl disable --now`
   flvx-landing-core.service, remove `/etc/systemd/system/flvx-landing-core.service` + the set-property drop-ins
   `/etc/systemd/system.control/flvx-landing-core.service.d/`, `daemon-reload`, `reset-failed`; delete only the Core's
   own files in the landing dir (binary, config/api/desired/applied/apply*/scan json, geo data, `run/`, their
   `.download` / atomic temp files); the dir itself only if empty. **Kept:** `entry_rates.json` (plan 168 entry 速率 tree
   lives in the same dir and is independent of the Core), any unknown file, the `flvx-landing` system user (reused on
   reinstall), the FLVX agent, 救命版 `flux_agent`, everything else. In-memory landing state is reset so the next
   `ApplyLandingDesired` installs from scratch exactly like the first time. Bounded: lock wait ≤ 15 s, whole command
   ≤ 60 s (panel waits 90 s).
4. **Version gate.** Exit agent `landing_chain < 3` (fork.130 and older, Nobrand ×2 fork.126): no command is sent; one
   log line per server and reason per panel process; the server stays as today; the next sweep after the OTA cleans
   it. Offline exit: waits silently (one line). A failed attempt backs off 30 min. No UI change (no admin hint needed).
5. **Counters.** A chain-only Core only ever carries chain copies (no landing nodes — guarded twice). Chain copies are
   billed at the rule's entry; exit chain counters are dropped by the panel (`filterChainCounters`, plan 108), so the
   Core holds no billed traffic. Inbounds were already removed by the normal desired push (agent samples before
   removal). The command is accepted only when nothing is left in the Core's desired/applied state → nothing to lose.
6. **Order + races.** Under `landingSyncMu` (every desired push takes it): re-check → push the empty desired state
   (revision R, agent confirms 0 nodes) → `RemoveLandingCore{R}` → delete the row (re-check under lock). A chain create
   racing in between either commits first (re-check keeps the server; its own sync, blocked on the mutex, then
   reinstalls the Core) or after (finds no row → creates a fresh chain-only server, exactly as today).
7. **Logs.** One line per real cleanup and one per new skip reason per server; nothing per sweep.

## Tasks
- [x] read memory + fork.130 notes, map chain-only code paths, prod DB read-only copy (server 9 only, 1 copy, no orphans)
- [x] agent: `Manager.RemoveCore` + `RemoveLandingCore` command + `ChainVersion` 3 + unit tests
- [ ] panel: repo idle query / keep reasons / guarded delete; handler cleaner loop + kick + version gate; sync refactor
- [ ] panel tests: every removal path, keep cases (paused, Bestvm-US-shaped, grants, disabled, route profile…),
      old agent, offline, failure backoff, recreate; SQLite + PG 18
- [ ] gates: go-backend build/vet/`go test ./...` (SQLite + PG), go-gost/x tests, go-gost build
- [ ] lab E2E (netns, local agents + pinned Core): create → delete → Core/files/row gone → recreate; old fork.130 agent
- [ ] push branch, CI green (no tag, no release, no prod change)
- [ ] ledger row 252 (待发布, ready), summary, cleanup temp files / lab units, plan complete

## Results
