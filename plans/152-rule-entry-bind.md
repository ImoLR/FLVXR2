# 152 — Rule entry binding (「只开所选入口」) + 「{入口}」 tunnel-name placeholder + one latency per picker option

Source: user 2026-10-25 ~01:30 / ~01:50 TW (admin, 规则 → 新增规则, 沪日IEPL pill; tunnels 25–33, 39, 42, 43 have entries
23 「Mkcloud 沪日ixp 440」 and 51 「vmsilo 沪日首发」, all named `mk沪日ixp440 -> …`).
Base: `origin/maintenance/3.0.27-fork.92-core-pin-dht` (fork.92 = prod, head 71c92de7).
Branch: `feature/3.0.27-rule-entry-bind`. Build + screenshots only — no tag, no release, no prod writes.

## Design

### Part A — per-rule entry binding
- New column `forward.entry_node_ids` (text, not null, default `''`; CSV of node ids). `''` = unbound = today's behaviour.
  Plain AutoMigrate add (SQLite + PostgreSQL); existing rows stay `''`.
- Invariant kept everywhere: **`forward_port` rows = the rule's effective entries**. Every deploy/runtime/attribution path
  already reads `forward_port`, so they honour the binding by construction. Only the paths that *compute* entries were
  changed.
- Effective entries = owner's allowed entries (`allowedTunnelEntries` / `TunnelEntryRestriction`, unchanged) ∩ binding.
  Stale binding: ids that left drop out (pruned binding persisted); nothing left → binding cleared (`''` → all allowed),
  one log line `[entry-bind] forward=… binding … cleared/pruned`, idempotent (next pass sees the stored result).
- API `forward/create` / `forward/update` (also replayed by the 「我自己的节点」 chain paths): optional `entryKey`
  (the picker option's `entryGroups[].key`).
  - absent → create: unbound; update: keep the stored binding (tunnel unchanged) / clear it (tunnel changed).
  - `""` → unbind. non-empty → resolve against the tunnel's entries ∩ the owner's allowed entries:
    admin key `node-<id>` (admin callers only), user key = hashed label key (may cover several nodes → bind all).
    Unknown key → error 「入口已变化，请重新选择隧道」. Resolved set = every allowed entry → stored unbound.
- Forward list returns `entryKey` (viewer's key format: admin `node-<id>`, user hashed key) + `entryLabel` (admin: node
  alias/name; user: alias/label) for bound rules only. Never node ids/names to non-admins.
- Batch 换隧道 and tunnel-delete migration clear the binding (`UpdateForwardTunnel` clears it). Import clears it
  (`entry_node_ids = ''` on upsert). Export unchanged (no binding in the export format; documented).

### Part B — 「{入口}」 placeholder
- New column `node.entry_alias` varchar(100) not null default `''`; admin node form field 「入口显示名」.
- One renderer (`repo.TunnelNameRenderer`): rows whose raw name has no `{入口}` pay nothing (no query). Otherwise one batched
  query (entry nodes of the affected tunnels) + for non-admin viewers one batched entry-restriction lookup.
  - specific entries → their displays; no entry context → distinct displays of the entries the viewer may see, joined 「/」.
  - display = alias; empty alias → admin: node name; non-admin: the entry label (city → region → 默认入口).
- Raw template only to admins as `nameTemplate` on `/tunnel/list` (tunnel edit form). Backups/export keep the raw name.
  Federation share/parse code keeps using raw names (internal).

### Part C — latency per option
- `/tunnel/user/latency` entries get `entryKey` (viewer's key format, same as `entryGroups[].key`; one batched node query).
  Picker option + trigger show only the entries whose `entryKey` equals the option's key. No entry context (filter
  select) → unchanged. Measuring unchanged.

## Checklist
- [x] Investigate paths, write this plan
- [x] Part A model/migration + repo helpers (parse/format, set binding, list bound forwards)
- [x] Part A handler: create / update / chain pre-check / stale fallback / reconcile / tunnel entry add+remove /
      batch change / delete migration / import / rollback / ingress text
- [x] Part A list `entryKey` + `entryLabel`
- [x] Part B node alias column + node create/update/list
- [x] Part B renderer + every consumer (list below)
- [x] Part C latency `entryKey`
- [x] Frontend: entryKey send rules, edit restore, hints, node form field, tunnel form nameTemplate, per-entry option names,
      per-option latency
- [x] Backend tests (SQLite + PostgreSQL)
- [x] Full go test vs known baseline (fork.92 list, 15 top-level/sub failures; nothing new)
- [x] Frontend tsc / lint / build + bundle delta
- [x] Prod-copy gates (picker union, no existing rule changes, timing/size) + netns E2E
- [x] Prod-data script + undo + dry-run name gate per user
- [x] Screenshots + gallery
- [x] Push, CI green, final.md / summary.md (no tag, no release; prod data step = `runs/rule-entry-bind/prod-data/apply.py`, run by the release worker)

## Results (prod copy 10-25, netns, fork.92 binary vs this branch)
- No existing rule changes: forward_port 36 rows identical (orig = fork.92 run = new run), 0 bound rules after migrate;
  every list (picker, rule list, dashboard, latency, tunnel list, group list, per-user grants) identical for admin + 12
  users except the intended latency `entryKey`.
- Plan-144 pill gate: union == today's options — admin 54 (深港 10 / 沪日440 12 / 沪港666m 11 / 沪日IEPL 12 / 其他 9),
  user 4: 22, user 8: 24, user 11: 12 — before and after the rename.
- Rename dry run (`prod-data/apply.py`, 14 rows): every non-admin user's names byte-identical (users 3/4/7/8/11/13/16/23;
  4/7/8/11 see the 12 shared tunnels); admin pills: 沪日440 → 12 × `mk沪日ixp440 -> …`, 沪日IEPL → 12 × `vmsilo沪日首发 -> …`;
  admin rule list byte-identical too (rules on entry 23 keep `mk沪日ixp440 -> …`). Undo round trip exact.
- Timing (min of 10, ms, fork.92 → new → renamed): admin rule list 11.05 → 11.24 → 11.45, admin picker 1.91 → 2.46 → 1.78,
  tunnel list 2.83 → 3.12 → 2.75, user 4 picker 7.85 → 8.83 → 7.01. Sizes: admin picker +0 / +1958 B after rename,
  tunnel list +1861 B (nameTemplate), latency +954 B (entryKey). Panel RSS 36 MB either way.
- Frontend: main JS 2,998,298 → 3,001,043 B (+2.7 KB, 2.86 MiB < 5 MiB); tsc 0; eslint errors unchanged (17/8/4/0),
  +35 prettier warnings on touched lines.


## Paths that compute a rule's entries (Part A)
Invariant: `forward_port` rows = effective entries. Checked with
`grep -rn "allowedTunnelEntries\|TunnelEntryRestriction\|CreateForwardTx\|ReplaceForwardPorts\|ReconcileForwardEntryPorts\|ForwardPort{\|UpdateForwardTunnel\|tunnelEntryNodeIDs("`.

| Path | Where | Result |
|---|---|---|
| Create | `forwardCreateWithReq` → `CreateForwardTx(..., entryBinding)` | entryKey resolved, ports only on bound entries, port picked over bound entries only |
| Update | `forwardUpdateWithReq` → `SetForwardEntryBinding` + `replaceForwardPorts*` (→ `forwardEntryNodes`) | absent key keeps binding; entries that left get DeleteService / nft delete after the sync; rollback restores binding |
| 「我自己的节点」 chain create/attach/update | `line_chain.go` pre-check + replay of `forwardCreateWithReq`/`forwardUpdateWithReq` (cloned req keeps entryKey) | same binding; key checked before the chain is prepared |
| Single / batch redeploy, tunnel redeploy | `redeployTunnelAndForwards`, `syncForwardServicesOnNode` (forward_port) | by construction |
| Node reconnect (fork.20 node-scoped replace, plan 138 diff) | `redeployNodeRuntime` → `ListActiveForwardIDsByNode` (forward_port) → orphans removed | bound rule not on other entries (test) |
| Tunnel entry add | `validateTunnelEntryPortConflictsForNewEntriesTx` (skip non-bound nodes), `syncTunnelForwardsEntryPorts` (skips bound) , `reconcileTunnelEntryPermissions` | bound rule never gains a new entry (test) |
| Tunnel entry remove | `cleanupTunnelForwardRuntimesOnRemovedEntryNodes` (skips bound), `reconcileTunnelEntryPermissions` → `applyForwardEntryBinding` | stale ids pruned / cleared once, log line, idempotent (test) |
| Grant change (user groups, group assign/remove, permission edits) | `reconcileTunnelEntryPermissions(…)` for restricted pairs + new `BoundForwardPairs` for unrestricted owners | binding ∩ new grant; cleared when nothing left (test) |
| nftables mode | `syncNftablesRules(ports)`, binding change → `deleteNftablesRules(left ports)` | by construction |
| Batch 换隧道 / tunnel-delete migration | `UpdateForwardTunnel` clears `entry_node_ids` | moved rules open on all permitted entries of the new tunnel |
| Backup import | `importForwards` upsert sets `entry_node_ids = ''` | imported ports as in the backup, unbound |
| Rule import (text) / export | frontend `createForward` per line without entryKey / export format unchanged | unbound |
| Ingress text (rule list address) | `resolveForwardIngress` treats a bound rule like a partial grant | only the bound entries' addresses (test) |
| Diagnose, live conns, client IPs, user pool, traffic attribution, landing guard IPs | all read `forward_port` (`forwardDiagnose`, `sumForwardConnections`, `repository_landing_lifecycle.go:223`, flow ingest per node) | by construction |
| Port allocation | `pickTunnelPort(tunnelID, entryNodes)`, `resolvePortForNewEntryNode` | bound entries only |
| Node delete | refuses while any `forward_port` uses the node | binding can only go stale via tunnel/grant changes |

## Tunnel name consumers (Part B)
`grep -rn "TunnelName\|tunnel.name\|GetTunnelName" go-backend/internal` (non-test):

| Consumer | Viewer | Rendering |
|---|---|---|
| `/tunnel/user/tunnel` admin (`listEnabledTunnelSummaries`) | admin | all entries + `entryGroups[].name` per entry |
| `/tunnel/user/tunnel` user (`listUserAccessibleTunnels`) | user | permitted entries (already filtered) + per entry |
| `/tunnel/list`, tunnel detail, tunnel update response (`ListTunnels`) | admin | all entries + `nameTemplate` (raw) |
| `/forward/list` (`DecorateForwardItems`) + speed-limit binding options | viewer | bound rule → its entries, else the viewer's entries; `entryKey`/`entryLabel` |
| `/user/package` (dashboard) tunnels + rules | viewer | same as rule list |
| `/tunnel/user/list` (admin view of a user's grants) | admin | all entries |
| `/group/tunnel/list` `tunnelNames` | admin | all entries |
| tunnel lists (`TunnelListItem.tunnelNames`), tunnel batch delete/redeploy failures, tunnel delete preview, tunnel diagnosis (+stream), node-edit sync results | admin | `GetTunnelDisplayName` |
| federation remote usage + remote node delete preview (local admin pages) | admin | `RenderTunnelNamesAdmin` |
| `parsePeerShareInfoFromFederationTunnelName` (tunnel delete, flow ingest) | internal | raw (unchanged) |
| LeiKwan pipe tunnel names (`LeiKwanTunnelName`) | admin | raw (LeiKwan tunnels are created by the pipe code, never templated) |
| backup export / import | — | raw |
| Telegram / notifications | — | none in FLVX backend (`grep -i telegram` → only payment providers) |
| agent configs / service names | — | never: `TunnelRecord` has no name, service names are `<forward>_<user>_<user_tunnel>` |
