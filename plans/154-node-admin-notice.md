# 154 — Admin node notice 「节点提示」 (⚠ triangle on the node card) — BUILD ONLY

Task `node-admin-notice` (user 2026-10-28 ~14:30 TW, after IPv6 RA auto-routing was disabled on node 39
「Bestvm sg 年付」: 「在新加坡那个节点那个地方也同样放一个小三角的感叹号通知一下……」).
User 10-28: batch releases → build only. No tag, no release, no prod writes; `release-batch-1028` ships
151 + 154 + 155 as one fork and sets node 39's notice.
Branch `feature/3.0.27-node-admin-notice` on `origin/feature/3.0.27-backend-test-cleanup` (plan 151 = fork.93 + 3 commits).
Run dir `/root/flvx-workers/runs/node-admin-notice/`.

## Design

- Column `node.admin_notice` varchar(512) not null default '' (AutoMigrate, SQLite + PostgreSQL, add-only;
  model field `json:"-"`, never serialised wholesale).
- `node/create` + `node/update` accept optional `adminNotice`: CRLF → LF, trimmed, ≤ 512 characters (runes);
  absent key on update keeps the stored value. Written by a dedicated `SetNodeAdminNotice` (no redeploy:
  `syncNodeUpdate` only redeploys on address/listen changes, notice is neither).
- `node/list` returns `adminNotice` for admins only (all `/api/v1/node/*` routes are admin-only already;
  the handler also strips it for role ≠ 0, like `ipv6RaStatus`). No other API returns it.
- UI: optional textarea 「节点提示」 in the node create/edit dialog with helper text
  「填写后节点卡片上显示 ⚠ 提示，留空则不显示」; amber `TriangleAlert` button (same pattern as the RA badge,
  label 「提示」 on ≥ sm, icon only on phones) in grid cards and list/grouped rows; click/hover → full text.
  Grid cards that already show another badge (IPv6 RA / 利群 / DDNS / 线路机) get the icon only, so the node
  name keeps its room (screenshot gate finding: a long name + RA + DDNS + 「提示」 shrank the name to 「L…」).

## Checklist

- [x] Backend: model column, repo setter, create/update parsing + validation, list field + non-admin strip
- [x] Backend tests: handler unit (round-trip, keep-on-absent, trim/CRLF, max length, non-admin strip, no redeploy),
      contract SQLite + PostgreSQL (create/update/list round-trip, 513 rejected, non-admin 403/strip, migration of an existing DB)
- [x] Frontend: types, form field + validation, badge component, grid card + list row
- [x] `go test ./...` SQLite + PostgreSQL green (19/19 packages each, 0 FAIL); frontend tsc 0 errors, lint of changed files
      = base (87 problems / 14 errors, all pre-existing), build ok
- [x] Node-39 step prepared + rehearsed on a prod DB copy through the admin API (no-op diff: no redeploy)
      → `runs/node-admin-notice/release/{set-node39-notice.py,NODE39-STEP.md}`
- [x] Resource numbers (backend RSS/CPU before/after, node/list payload)
- [x] Screens 390 + 1440, light + dark → `/root/mmwx-preview/node-admin-notice/` (:8791), 36 shots, 50/50 checks
- [x] Push branch, CI green (run 40652570574: agent / backend / frontend / PostgreSQL contract all success)
- [x] Summary (`runs/node-admin-notice/summary.md`) + plan marked complete

## Result (build only — not tagged, not released, prod untouched)

Commit `8fc87a0e` on `feature/3.0.27-node-admin-notice` (+ this plan). Ships with release-batch-1028 (151 + 154 + 155).

- Resource (prod DB copy, 30 nodes, panel in a no-network netns): base → new RSS 36 → 36 MiB, idle CPU 0.28 → 0.37 %
  (noise), node/list best 4.5 → 4.2 ms, payload 35 481 → 35 991 B (+17 B per node for `"adminNotice":""`).
  No new query, no polling: the column is read in the existing node SELECT.
- Non-admin scan on the copy (users 49 and 8): node/list + check-status 403; forward/list, tunnel/user/tunnel,
  monitor/nodes, landing node/server lists, landing monitor, user/package never contain the notice or the key.
- Node 39 step for the release worker: `runs/node-admin-notice/release/NODE39-STEP.md` + `set-node39-notice.py`
  (normal admin API, minimal payload, refuses if interface_name is set, verifies no redeploy and an unchanged
  tunnel/forward set). Rehearsed twice on the copy (node offline and forced online): tunnelsUpdated 0, failures [];
  control with a listen-address change on the same node: 3 redeploy failures, so the check is meaningful.

Found, not fixed (pre-existing, out of scope): node/list never returns `interfaceName`, so any node save from the UI
sends `""` and clears `node.interface_name`; when it was set, that also counts as a listen change and redeploys the
node's tunnels. No prod node has it set today (2026-10-28), so nothing is affected now.
