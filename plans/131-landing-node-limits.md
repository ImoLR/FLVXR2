# 131 — landing-node-limits: global default node cap + admin 「改所属用户」

Base: `feature/3.0.27-landing-user-view` (plan 132, 401aa570, on fork.71). Branch `feature/3.0.27-landing-node-limits`.
Build + tests + screenshots only — no release, no prod change (waits for the user's screenshot approval; released
together with plan 132).

## Problem (user 2026-10-23: 「可以把這兩個改動加上去吧」)
- The per-user total landing-node cap (`landing_user_setting.max_nodes`) has a hard-coded default 5 and no global
  setting; only per-user edits exist.
- An existing landing node's owner cannot be changed (admin can only create for another user).

## Tasks
- [ ] Plan doc + baseline backend suite (unmodified base)
- [ ] Model: `landing_user_setting.max_nodes_custom` (bool, not null, default false) + one-time backfill
      (`max_nodes <> 5` → custom, marker in vite_config); SQLite + PostgreSQL
- [ ] Global value `landing_default_max_nodes` in vite_config (missing → 5; validated 0–10000 also via generic
      config update); admin endpoints `landing/settings/get` + `landing/settings/save`
- [ ] One helper `EffectiveLandingMaxNodes` (custom / global / admin) used by create, transfer, user settings API,
      `LandingUserNodeCounts` (plan 132 `myRemaining`)
- [ ] `landing/user/get` returns `maxNodes` (effective) + `maxNodesSource` + `globalMaxNodes`; `landing/user/save`
      accepts `maxNodesSource: global|custom`; a bare numeric `maxNodes` (old clients) stays custom
- [ ] Transfer: repo `TransferLandingNode` (one tx, target user + server + node locked; checks: target exists and
      active, server permission, total + per-server caps counting the moved node, chain-only server, delete-pending,
      rules of other users referencing the node named); handler `landing/node/transfer` (admin only), best-effort
      counter flush before, `reconcileLandingPolicy` + `syncLandingServer` after, one log line
- [ ] Frontend: admin 「全局设置」 button + dialog (one number field); user edit 「落地」 跟随全局 / 自定义;
      node details 「所属用户」 select + 转移 button
- [ ] Contract tests (SQLite + PostgreSQL): global default live, custom override, migration, 0 blocks, lowering keeps
      nodes, 20 parallel creates never exceed the cap, transfer success/refusals, non-admin refused, billing after
      transfer → new owner (flow batch ingest)
- [ ] Acceptance runner: transfer scenario (local lab, real Core): admin creates for A, traffic → A, transfer to B,
      traffic → B, link still connects; shared desired owner updated
- [ ] Backend suite after (no new failures), frontend tsc / lint / build
- [ ] Panel RSS/CPU in the lab before/after
- [ ] Screenshots desktop + H5, light + dark (全局设置, user edit 落地 跟随全局/自定义, node details 所属用户, refusal toasts,
      before/after of touched screens); gallery on mmwx-preview
- [ ] Push branch, CI green, summary

## Decisions
- Storage: flag column instead of making `max_nodes` nullable (no column-type change on either dialect). A
  follow-global row keeps `max_nodes = 5`, so a rollback to fork.71 behaves exactly as before.
- Admin accounts (role 0) are not limited by the total cap (they are not in the user list, so they cannot get an own
  value; a global 0 would otherwise lock the admin out). The per-server cap still applies to them, as today.
- Transfer refuses when rules of any user other than the target reference the node (`forward.landing_node_id`), so
  the owner-only landing billing exemption never silently moves.
