# 131 — landing-node-limits: global default node cap + admin 「改所属用户」

Base: `feature/3.0.27-landing-user-view` (plan 132, 401aa570, on fork.71). Branch `feature/3.0.27-landing-node-limits`.
Build + tests + screenshots only — no release, no prod change (waits for the user's screenshot approval; released
together with plan 132).

## Problem (user 2026-10-23: 「可以把這兩個改動加上去吧」)
- The per-user total landing-node cap (`landing_user_setting.max_nodes`) has a hard-coded default 5 and no global
  setting; only per-user edits exist.
- An existing landing node's owner cannot be changed (admin can only create for another user).

## Tasks
- [x] Plan doc + baseline backend suite (unmodified base)
- [x] Model: `landing_user_setting.max_nodes_custom` (bool, not null, default false) + one-time backfill
      (`max_nodes <> 5` → custom, marker in vite_config); SQLite + PostgreSQL
- [x] Global value `landing_default_max_nodes` in vite_config (missing → 5; validated 0–10000 also via generic
      config update); admin endpoints `landing/settings/get` + `landing/settings/save`
- [x] One helper `EffectiveLandingMaxNodes` (custom / global / admin) used by create, transfer, user settings API,
      `LandingUserNodeCounts` (plan 132 `myRemaining`)
- [x] `landing/user/get` returns `maxNodes` (effective) + `maxNodesSource` + `globalMaxNodes`; `landing/user/save`
      accepts `maxNodesSource: global|custom`; a bare numeric `maxNodes` (old clients) stays custom
- [x] Transfer: repo `TransferLandingNode` (one tx, target user + server + node locked; checks: target exists and
      active, server permission, total + per-server caps counting the moved node, chain-only server, delete-pending,
      rules of other users referencing the node named); handler `landing/node/transfer` (admin only), best-effort
      counter flush before, `reconcileLandingPolicy` + `syncLandingServer` after, one log line
- [x] Frontend: admin 「全局设置」 button + dialog (one number field); user edit 「落地」 跟随全局 / 自定义;
      node details 「所属用户」 select + 转移 button
- [x] Contract tests (SQLite + PostgreSQL): global default live, custom override, migration, 0 blocks, lowering keeps
      nodes, 20 parallel creates never exceed the cap, transfer success/refusals, non-admin refused, billing after
      transfer → new owner (flow batch ingest)
- [x] Acceptance runner: transfer scenario (local lab, real Core): admin creates for A, traffic → A, transfer to B,
      traffic → B, link still connects; shared desired owner updated
- [x] Backend suite after (no new failures), frontend tsc / lint / build
- [x] Panel RSS/CPU in the lab before/after
- [x] Screenshots desktop + H5, light + dark (全局设置, user edit 落地 跟随全局/自定义, node details 所属用户, refusal toasts,
      before/after of touched screens); gallery on mmwx-preview
- [x] Push branch, CI green, summary

## Results
- Backend suite: base 15 FAIL (pre-existing) → after the same 15, identical list. New contract tests
  (`landing_node_limits_contract_test.go`) pass on SQLite + PostgreSQL 18; existing landing repo/handler/contract tests
  pass on both (repo fixture users 501/502 now `RoleID: 1` — they modelled normal users; admins have no total cap now).
- Frontend: tsc clean, build OK, lint 0 errors and no new warnings in touched files.
- Acceptance N1 (local lab, real Core, fake Custom): 20/20; R1 2/2.
- Screens: 53/53 automatic checks, 0 page errors; gallery `/landing-node-limits/` on mmwx-preview.
- Panel lab perf (same data, alternating runs): server list p50 ~1.3 vs ~1.1–2.5 ms, CPU 480 vs 450–830 ms per 465
  requests, settled RSS 73.7 vs 71.6 MB — no measurable change.
- Custom (v1.4.73 code read, `flvx_api.go`) shows `owner` straight from the latest desired state: no mmwx change needed.

## Decisions
- Storage: flag column instead of making `max_nodes` nullable (no column-type change on either dialect). A
  follow-global row keeps `max_nodes = 5`, so a rollback to fork.71 behaves exactly as before.
- Admin accounts (role 0) are not limited by the total cap (they are not in the user list, so they cannot get an own
  value; a global 0 would otherwise lock the admin out). The per-server cap still applies to them, as today.
- No credential-reset function exists for landing nodes, so the optional 「转移后重置密码」 checkbox is skipped; there is
  no admin operation log either — the transfer writes one `[landing-transfer]` line to the panel log.
- Before the owner changes, the node's current counters are collected once (native: ApplyLandingDesired snapshot;
  shared: Custom status poll) so traffic up to the transfer bills the old owner; offline → skipped, the next report
  then bills the new owner.
- Transfer refuses when rules of any user other than the target reference the node (`forward.landing_node_id`), so
  the owner-only landing billing exemption never silently moves.

## Release (fork.73, user approved 2026-10-22 ~22:30 TW 「可以」, incl. admin total-cap exemption)
Plan 131 alone → `3.0.27-fork.73`, panel/frontend only, no agent OTA. Branch
`maintenance/3.0.27-fork.73-landing-node-limits` = `feature/3.0.27-landing-node-limits` (966386da) rebased onto
`maintenance/3.0.27-fork.72-landing-user-view` (485ef33e; no conflicts, plans/132 = fork.72 version); stray
`scripts/landing-acceptance/__pycache__/*.pyc` removed, `__pycache__/` + `*.pyc` ignored. Plan 134 (landing rule
layers) is NOT part of this release and must rebase onto this branch before its own release.
Run dir `/root/flvx-workers/runs/release-fork73-landing-node-limits`.

- [x] Rebase onto fork.72, drop pyc, `go build ./...` + frontend build sanity
- [ ] Pre-upgrade prod capture (read-only): `landing_user_setting` rows + effective caps, global default, admin
      user-settings view, user 26 `/landing/server/list` myRemaining, 29 nodes / forwards / landing nodes, CPU/RSS,
      latency
- [ ] Push branch, CI green
- [ ] Tag `3.0.27-fork.73`, Images run green, release Latest with the same asset set as fork.72
- [ ] Prod backup `pre-fork73-*` (validated SQLite, compose/.env, local image tags, rollback.sh) + prune to 2
- [ ] Upgrade prod panel to fork.73, health + version.json
- [ ] Verify: migration ran once (column added, rows with 5 → follow global, user 26 custom 2), effective caps
      identical (admin now uncapped on total), user 26 still-creatable unchanged, admin view 「跟随全局（5）」,
      29/29 online, agents unchanged, forwards + landing nodes unchanged, new UI strings in served bundle, CPU/RSS +
      latency after
