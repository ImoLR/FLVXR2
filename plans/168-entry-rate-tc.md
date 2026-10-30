# 168 — entry-rate-tc: every entry speed limit → tc HTB + fq_codel (BUILD ONLY)

Brief: `/root/flvx-workers/runs/entry-rate-tc/brief.md` (user 2026-10-30 「把这个全部都换成 TC 吧，统一一下口径……注意效率的问题」).
Branch `feature/3.0.27-entry-rate-tc` from tag `3.0.27-fork.97` (= prod). Build only: no tag, no release, no prod writes, no OTA.
Base reading: plans 159 / 161, `runs/limits-landing-rate/final.md` + `lab/fair/`, `contracts/limits-unify-decisions.md`.

> **Superseded in part by plan 170 (speed-simplify, 2026-10-30 ~23:50 TW):** the user retired 限速规则 / 竞技场 and the
> rule's own speed — only 用户默认速率 + per-user per-server 速率 remain. The tree now only gets user classes
> (`repo.EntryRateRules` no longer reads rule limits), so the user decisions 2 (cross-user 竞技场 vs user 速率) and 3
> (non-arena 限速规则 per rule vs shared) in `runs/entry-rate-tc/final.md` became moot. The rule / arena class code
> stays (tested) but is not reached.

## Facts (code, fork.97)

- A rule has at most ONE own limit on its entry machine: dynamic `forward.speed_limit` (`forward_<id>_speed`) else
  `forward.speed_id` else `user_tunnel.speed_id` (named limiter `<id>`, or `arena_speed_<id>_node_<n>_<hash>` when the
  限速规则 has `arena_mode=1`). Plus the plan-159 user row rate (`flvx_user_<U>_rate`, `UserServerRateMbps`).
- gost: a named limiter's `$` bucket is ONE object per machine, so today a NON-arena 限速规则 is (by accident) shared by
  every rule that references it on the machine, although the admin UI / `docs/speed-limit-scope-design.md` define
  non-arena as 「各自拥有自己的限速能力」 and arena as 「共享同一个限速桶」.
- nftables rules: one inline policer per rule (`min(own, row)`), never shared (arena included).
- Plan-161 shaper (`go-gost/x/landing/user_rate_linux.go`): IFB `flvxlr-up` / `flvxlr-dn`, HTB `7f1a:`, class minor
  `slot<<4` (low nibble 0 → re-injected packets keep priority-0 behaviour), flower prefs 61860 (v4) / 61861 (v6),
  handle `port<<2 | 1 tcp / 2 udp`, skbedit priority + mirred redirect; drain 2 s; 10 s tick / 60 s full verify.
  Runs inside `landing.Manager` (not on node 36).
- Rule ports and native landing ports are disjoint on a node (plan 139 carves landing from the rule range).

## Design

### HTB tree (one per machine, shared with landing)
`root 7f1a: → [user class U (row rate)] → [rule / arena class] → fq_codel`.
- User class U (group): inner class, rate = ceil = row; always has a default leaf `U/` (rate share, ceil = row) that
  carries every rule of U WITHOUT an own limit — flows of those rules share fairly (same as a landing user class).
- Rule own limit (dynamic or non-arena 限速规则): leaf under U (ceil = min(own, row)) or top-level (rate = ceil = own).
  Non-arena 限速规则 = per rule (the documented meaning; today's gost sharing was accidental) — 「需要用户决定」.
- Arena: one leaf per (限速规则, entry node). Members of one user with a row → under U; members whose users have no
  row → top-level. **Conflict**: arena members of ≥ 2 users and at least one of them has a row on this machine → no
  tree can hold both limits; the arena then stays on today's agent path (gost shared limiter; nft per-rule policer at
  the arena speed), the user rows of those rules go to tc. No limit is dropped — 「需要用户决定」 (default above).
- Children of an inner class: rate = min(ceil, parent / n) (sum ≤ parent, so the parent always holds), ceil as above,
  equal quantum → equal share when all are busy, the idle share goes to the busy ones (work-conserving).
- Landing classes unchanged (top-level leaves, slot = user id or highest free). Entry classes get slots from the free
  rest (sticky, from 2048 up), identity = (parent, id): a reparent is a new class, the old one drains.
- Filters: same prefs / handles / mask as landing (one hashed lookup per hook and family, landing + entry together);
  a port claimed by both → landing wins, entry reports an error for that port.

### Agent (go-gost/x/landing + socket)
- Command `SetEntryRates {version 1, classes [{id, parent?, group?, rate_mbps}], ports [{port, tcp, udp, class}]}`
  (full snapshot, runtime revision `entryrate:entry`), persisted in `<landing dir>/entry_rates.json`, applied at once
  (hot: class change / add / drain, filters follow), re-applied from the file at start, 10 s / 60 s ticks reused.
- Handshake `entryRateTc=1` + `entryRateTcErr=<reason>` when a one-time start probe (throw-away IFB + HTB + fq_codel +
  clsact + flower/skbedit/mirred) fails (LXC / OpenVZ / missing module). Not on node 36 (landing manager off there).
- Status: `entry_rate {state, error, classes, ports}` in the 1 s metric message only when it changed (like `addrs`).
- Graceful stop releases everything (shared with landing); kill -9 → next start cleans up first.

### Panel (go-backend)
- `node.entry_rate_tc` (handshake: 0 = old agent, 1 = tc, 2 = kernel cannot → fallback) + `node.entry_rate_error`
  (probe reason / runtime error) — add-only.
- Snapshot per node from the DB (active forwards with an entry port there, user rows, own limits, arenas); pushed only
  to tc nodes, only when its signature changed (memo per agent session); triggers: forward sync / delete / pause,
  row / user default change, 限速规则 edit, user_tunnel speed change, reconnect (inventory revision), 60 s safety pass.
- tc nodes: gost services carry no `limiter` / `userLimiter` for tc-covered limits, nft rules no policer, no
  `flvx_user_*` / rule limiters pushed. Fallback nodes (old agent, kernel cannot, remote): byte-identical to fork.97.
- UI: small yellow hint on the 速率 row (user server limits dialog) and on the rule speed chip where the entry node
  runs the fallback (or reports a tc error). No other UI change.

## Checklist

- [x] Plan doc
- [x] Agent: tree engine (groups, children, drain, reparent), entry snapshot (validate, merge, persist), probe,
      handshake, status report, SetEntryRates command + revision
- [x] Agent tests: unit + kernel netns (tree, hot change, reparent, landing + entry together, restart, 500 ports);
      landing kernel / netns tests unchanged and green
- [x] Panel: columns + capability, snapshot builder, push / memo / reconnect / safety pass, tc-aware gost + nft payloads,
      status, API fields; tests SQLite + PostgreSQL
- [x] UI: yellow fallback hint (row + rule); tsc / build / lint
- [x] Lab: gost limiter vs nft policer vs tc (gost + nft rules), 20/100/500, 2/4 flows, stagger, both directions,
      work-conserving cases, accuracy, CPU; 500-port filter scale
- [x] DNAT check in netns: ingress sees listen port before DNAT, egress after un-NAT (v4 + v6, TCP + UDP)
- [x] E2E: real panel + real agent (row 20 over gost + nft rule, rule 5 under it, second user, live change / removal,
      restart → one set, landing on the same node, old agent / no-IFB fallback)
- [x] Resource numbers (agent RSS / CPU / goroutines, kernel CPU 20 / 100 / 500)
- [x] Full `go test ./...` SQLite + PostgreSQL; go-gost / go-gost/x touched packages incl. netns tests
- [x] Screens of the yellow hint
- [x] Push branch, CI green
- [x] `runs/entry-rate-tc/release-notes.md`, summary.md / final.md

## Results

Lab: `runs/entry-rate-tc/lab/` (netns, offline, synthetic data; real panel + agent A; fork.97 agent for today's paths).

### Fairness / accuracy (one rule limit, 2 / 4 connections, staggered, both directions, 40 ms RTT; `lab/fair/`)
| method | slowest/fastest median (worst) at 20 / 100 / 500 | payload / set |
|---|---|---|
| gost limiter (fork.97) | 1.00 / 1.00 / 0.99 (0.96) | 1.00 |
| nft policer (fork.97) | 0.80 / 0.54 / 0.81 (0.19) | 0.98–1.00 |
| tc, gost rule | 0.98 / 0.97 / 0.97 (0.94) | 1.00 / 0.96 / 0.92 |
| tc, nft rule | 0.98 / 1.00 / 0.96 (0.42 = fq_codel bucket collision, 4 flows) | 1.00 / 0.98 / 0.93 |
Work-conserving (100): capped 5 + greedy → 95.6 / 92.0 (tc gost / nft, download); one flow alone 97.9 / 91.5.

### CPU (softirq + agent process, % of one core, median of 3; `lab/fair/cpu.json`)
gost limiter 8.3 / 15.4 / 27.7, nft policer 5.1 / 6.8 / 10.4, tc gost 11.2 / 19.4 / 24.8, tc nft 8.9 / 16.7 / 16.9
(20 / 100 / 500). Agent at a 500 Mbps rule limit: fork.97 35 % → 6.7 %. RSS 50–52 MB both, 13 threads both.
500 limited ports: 4024 filters / 102 classes, apply 0.5–0.7 s, tick 3–7 ms, full verify ~0.1 s; an unlimited
400 Mbps flow costs the same with 3 or 500 limited ports (softirq 8.3 vs 9.8).

### E2E (`lab/e2e.py`, 22/22) + DNAT v6 (`lab/dnat_v6.py`)
Row 20 over gost + nft rule 9.85 + 9.98 down / 9.80 + 9.91 up, bob 674; own 5 under it 4.9 + 14.6; live 20 → 40 →
removed on running connections (0.18 s, no drop); 3 restarts + kill -9 → one set, tree not re-pushed; panel restart
quiet (updated=0); IPv6 gost 4.8 + 14.5; UDP nft 19.7 / 20.4, gost 5.1; nft v6 DNAT TCP 9.6 + 9.6, UDP 19.5 / 19.9;
fork.97 agent and FLVX_ENTRY_RATE_TC=off → old path, no tc objects; back to tc; all removed → nothing left.
Tests: go-backend `go test ./...` SQLite 19/19, PostgreSQL 19/19; go-gost/x 20/20 packages (root kernel / netns tests
included; one first-run failure was the unix socket path of an over-long TMPDIR, passes with a short one); go-gost
1/1; frontend tsc 0 errors, build ok, lint no new errors (+5 prettier warnings in an already mis-indented block).
CI green. Screens: `/root/mmwx-preview/entry-rate-tc/` (16 shots, checks pass). Report: `runs/entry-rate-tc/final.md`.
Landing real-Core netns E2E (pinned Core v1.4.77): 3 of 4 runs pass on this branch (one borderline dip 35.2 < 36
right after heavy lab traffic), fork.97 2/2.

### Found and fixed during the lab
- Kill switch / failed probe still re-applied a kept `entry_rates.json` at start → now dropped and SetEntryRates refused.
- A removed user group needed 3 ticks to clean up (parent visited before children) → children first, gone in 1 tick.
- A speed-only rule edit restarted the gost service (UpdateService) → skipped on tc nodes when the agent already runs
  the identical service revision; only the tree changes.
- Rule list dropped the new `rateFallback` field (explicit mapping) → mapped.
