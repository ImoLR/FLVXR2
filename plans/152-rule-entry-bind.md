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
- [ ] Part A model/migration + repo helpers (parse/format, set binding, list bound forwards)
- [ ] Part A handler: create / update / chain pre-check / stale fallback / reconcile / tunnel entry add+remove /
      batch change / delete migration / import / rollback / ingress text
- [ ] Part A list `entryKey` + `entryLabel`
- [ ] Part B node alias column + node create/update/list
- [ ] Part B renderer + every consumer (list below)
- [ ] Part C latency `entryKey`
- [ ] Frontend: entryKey send rules, edit restore, hints, node form field, tunnel form nameTemplate, per-entry option names,
      per-option latency
- [ ] Backend tests (SQLite + PostgreSQL)
- [ ] Full go test vs known baseline (15 known failures)
- [ ] Frontend tsc / lint / build + bundle delta
- [ ] Prod-copy gates (picker union, no existing rule changes, timing/size) + netns E2E
- [ ] Prod-data script + undo + dry-run name gate per user
- [ ] Screenshots + gallery
- [ ] Push, CI green, final.md / summary.md

## Paths that compute a rule's entries (Part A)
(filled in while implementing)

## Tunnel name consumers (Part B)
(filled in while implementing)
