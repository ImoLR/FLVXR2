# 155 — Hide the plan-152 「{入口}」 naming + split the 12 shared 沪日 tunnels into one-entry tunnels

Source: user 2026-10-28 ~15:00 TW (split the 12 shared tunnels, one entry each, names = the entry machine's name like
today) + correction ~15:20 TW (binding): 「FLVX 面板原本的多入口逻辑不要去动它」 — the original multi-entry tunnel
feature stays exactly as is; only plan 152 Part B (`{入口}` placeholder + node 「入口显示名」) is hidden. Plan 152
Part A (rule entry binding) / Part C (per-option latency), plan 144 pills, fork.34 per-entry permissions untouched.
Base: `origin/feature/3.0.27-node-admin-notice` (plan 154 on plan 151 on fork.93). Branch
`feature/3.0.27-tunnel-single-entry`. BUILD ONLY — no tag, no release, no prod writes; the release worker
(`briefs/release-batch-1028.md`) ships 151 + 154 + 155 and runs the data step.

## Facts (fresh prod copy 2026-10-28 07:59Z, read-only)
- Only tunnels with ≥ 2 entries: 25–33, 39, 42, 43 (12), entries node 23 「Mkcloud 沪日ixp 440」 (v4 163.53.55.158) +
  node 51 「vmsilo 沪日首发」 (v6-only). Raw names `{入口} -> 出口…` (the only 12 names with the token, 43 tunnels).
  11 are type 2 (tunnel forwarding, 1–3 hop/exit nodes), 39 is type 1 (port forwarding, entries only).
- 13 rules, all `forward_port` on node 23 only, 0 bound (`entry_node_ids`), 0 forward_chain rows.
- Each tunnel: 6 `user_tunnel` rows (users 4/7/8/10/11/12), all **group-created** via user group 3 ↔ tunnel group 3
  「沪日440」 whose entry rows cover node 23 only → every non-admin user is restricted to entry 23; **no user can see
  entry 51 today** (tunnel group 8 「沪日IEPL」 = the 12 tunnels × entry 51, has no user-group permission).
- `tunnel_group_tunnel_new` (tunnel page folders): group 2 「Mkcloud 沪日ixp 440」 has 25–33; `tunnel.tunnel_group_id`
  = 2 on all 12. `tunnel_list_tunnel`, `speed_limit.tunnel_id`, `federation_tunnel_binding`, LeiKwan pipes: none.
- `tunnel/update` (the only API that removes an entry / renames) calls `cleanupTunnelRuntime` (DeleteChains on every
  entry + DeleteService/DeleteChains on every hop/exit), re-adds everything and sends `UpdateService` for every rule of
  the tunnel; the agent restarts a listener on every Add/Update (plan 138 comment, no same-rev skip) → would cut the
  live connections of all 12 tunnels' hops and the 13 rules on node 23. Avoided (see design).

## Design
### Part 1 — hide plan 152 Part B (frontend only)
- Node form: field 「入口显示名」 + its validation removed; the form no longer sends `entryAlias` (backend keeps the
  stored value when the key is absent). Tunnel form: the `{入口}` description hint removed.
- Kept on purpose (rollback/backup safety, zero cost without the token): backend renderer, `node.entry_alias` and
  `forward.entry_node_ids` columns, `nameTemplate` on the admin tunnel list (an edit of a templated name keeps the raw
  template), per-entry option names in the picker (only produced when a raw name has the token).
- Aliases of nodes 23/51 stay in the DB (no visible effect once no name has the token; plan-152 rollback keeps working).

### Part 2 — split endpoint (admin, new code only; original multi-entry logic untouched)
`POST /api/v1/tunnel/split-entry` {tunnelId, entryNodeId, name, twinName, dryRun}
- Refuses: <2 entries / node not an entry, rules with a `forward_port` on that entry (or bound to it), LeiKwan pipe,
  federation binding / remote nodes, duplicate or empty names, any user who would lose the original (all their
  visible entries = the moved one).
- Twin = the normal `/tunnel/create` path (called in-process) with the moved entry only and the same hop chain
  (nodes, protocols, strategies, connect_ip/type, ip_preference, traffic_ratio, flow, status, speed, flags, list,
  folder); hop ports picked fresh from each hop node's range.
- One transaction: remove the entry row from the original, rename, recompute `in_ip` (`TunnelEntryInIPTx`), twin inx =
  original + 1 (later tunnels +1, relative order unchanged), copy folder/list rows, move tunnel-group membership of the
  moved entry (entry row → twin; original leaves a group whose only selected entry was the moved one).
- Users: whoever could see the moved entry (`EffectiveTunnelEntryNodeIDs`) gets the twin with the original's limits
  and fresh counters (group sync first, then limits copied; direct grant otherwise). Prod: nobody.
- Runtime: twin = create pushes (new objects only); original = one `DeleteChains chains_<id>` on the moved entry
  (type 2), nothing on type 1; no hop/rule push.
`POST /api/v1/tunnel/merge-entry` {tunnelId, twinId, name, inIp, dryRun} = exact undo (entry row back, name/in_ip
back, group rows back, `AddChains` on the entry, twin deleted through the normal delete path, inx shift back).

### Part 3 — data-step driver (Python, API only, kept in the run dir like plan 152's)
`/root/flvx-workers/runs/tunnel-single-entry/prod-data/split_shared_tunnels.py` (+ README with the release-worker
commands) — dry run by default, `--apply`, `--undo FILE`, `--verify`; undo file written before applying; guards on the
exact expected prod state (entries [23, 51], `{入口} -> ` names, aliases, nodes online, no other templated name);
idempotent (a split tunnel is reported as done).

## Checklist
- [x] Investigate (plan 152 commits, prod copy schema/rows, tunnel/update runtime path), write this plan
- [x] Frontend: hide 入口显示名 + `{入口}` hint (5b68e729)
- [x] Backend: repo split/merge helpers + `/tunnel/split-entry` + `/tunnel/merge-entry` (0c4444f2, 1986191d)
- [x] Contract tests SQLite + PostgreSQL: dry-run/apply/undo, groups/permissions/users copy, names, pushes per node;
      regression: multi-entry tunnel create/edit/deploy unchanged (3 tests × 2 DBs pass)
- [x] Data-step driver + README (exact release-worker command)
- [x] Prod-copy proof: local panel + fake agents, before/after tables, forward ports, per-user visibility, pushes
- [x] Resource numbers: one extra relay listener RSS/fd (local gost), projected per hop node
- [x] Full `go test ./...` SQLite + PostgreSQL; frontend tsc/build/lint (SQLite 4m50s, PostgreSQL 10m46s: 19/19 packages ok each, 0 FAIL)
- [x] Screens 390 + 1440 light/dark → `/root/mmwx-preview/tunnel-single-entry/` (28 shots, 32/32 checks)
- [ ] Push, CI green, summary.md

## Results (build only, 10-28)
- **Lab** (fresh prod copy 10-28 07:59Z, this branch's panel in netns `flvx155` with no outbound, 12 fake agents =
  nodes 23, 51 and every hop/exit node of the 12 tunnels): dry run → apply → `--verify` → snapshot → undo → snapshot.
  - Split: 12 twins (ids 92–103 on the copy), no multi-entry tunnel and no `{入口}` name left; `forward` (38) and
    `forward_port` (38) rows identical; `user_tunnel` (136) and grants (134) identical (no twin rows — all 6 users of
    these tunnels were group-restricted to entry 23); original chain rows = before minus the 12 entry-51 rows, every
    hop/exit row and port identical; twins: same hop chain + flags, entry 51 only, fresh hop ports, in_ip = node 51's v6;
    the other 31 tunnels unchanged except inx (relative order kept, each twin right after its original); groups:
    沪日440 unchanged (12 × entry 23), 沪日IEPL = 12 twins × entry 51; every non-admin user's picker and rule list
    byte-identical (users 4/7/8/11 see the 12 `mk沪日ixp440 -> …`), admin rule list identical.
  - Pushes during apply (fake-agent log, = dry-run prediction): 76 total — node 51: 34 (11 AddChains twin, 11
    DeleteChains original, 12 SetProtocol same values), node 18: 13, 12/21/26: 7, 14/45: 2, 15/17/30/39: 1, **node 23:
    0**; nothing touched an original tunnel's hop service or rule. Undo: 64 pushes (51: 22, hops: Delete* of the twins).
  - Undo: 26/26 snapshot sections identical to before (tunnel rows incl. names/inx/in_ip, chain rows incl. ports,
    groups, users, every API view). First run found the restored entry row with connect_ip '' instead of NULL → fixed.
  - Reference for the regular path (contract test): `/tunnel/update` dropping one entry sends DeleteService + AddService
    `<id>_tls` on every hop and exit (listener restart) plus UpdateService of the rules — that is what the split avoids.
- **Resources**: agent (go-gost, standalone in the netns) with 1/13/25/49 idle relay+tls listeners: RSS 42.0/41.5/41.0/
  42.7 MB (noise ±1 MB, ≈15 KB per listener), fds 6/18/30/54 (+1 per listener), threads 10–13, idle CPU 0 ticks/30 s.
  Projected after the split: node 18 +7 listeners/+7 fds, 12/21/26 +4, 14/45 +2, 15/17/30/39 +1, node 51 ±0 (11 chain
  objects replaced, chains have no socket), node 23 0; < 0.2 MB RSS on any node. No new polling; the existing 1-min
  path-latency probe now also measures the twins' own hop ports: +19 TcpPing per cycle (66 → 85: node 18 +9, 21 +4,
  12 +3, 26 +3; 23/51/15 unchanged) — one short TCP connect each per minute.
- **Tests**: new contract tests (SQLite + PostgreSQL) `TestMultiEntryTunnelUnchangedContract`,
  `TestTunnelSplitEntryContract`, `TestTunnelSplitEntryPortForwardAndRefusalsContract` pass; frontend tsc 0, vite build
  ok (main JS 3,002,197 B < 5 MiB), eslint errors unchanged (node 8 / tunnel 4; warnings −1 / −3).
- Still open: twins copied the tunnel-page folder `tunnel_group_new` 2 「Mkcloud 沪日ixp 440」 (brief: same groups), so
  vmsilo cards carry that blue folder chip — move them in 隧道 → 分组管理 if wanted; node 23/51 aliases stay in the DB
  (no visible effect without `{入口}` names; plan-152 rollback keeps working).
