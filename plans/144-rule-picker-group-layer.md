# 144 — Rule picker: 线路分组 layer before 选择隧道

Source: user voice message 2026-10-24 (规则 → 选择隧道 lists too many tunnels for users with several tunnel groups).
Base: `origin/maintenance/3.0.27-fork.83-builtin-abuse-unify` (fork.83, newest released maintenance branch at start).
Branch: `feature/3.0.27-rule-picker-group-layer`. Build + screenshots only — no tag, no release, no prod change.

## Design

### Backend — `groups` on `/tunnel/user/tunnel`
Each item gets `groups: [{id, name, color, inx, entryKeys}]` (additive; `entryKeys` = the item's `entryGroups[].key`s the group
covers, `[]` = all entries, used for tunnels without entry nodes). Built by one batched query per request
(`Repository.tunnelPickerGroupRows`), only in the `/tunnel/user/tunnel` handler (path-latency callers unchanged).

- Admin: every `tunnel_group` with `status = 1`; (tunnel, entry node) belongs to the group when the group contains the tunnel
  (`tunnel_group_tunnel`) and the group has no `tunnel_group_tunnel_entry` rows for the tunnel or has a row for that node.
- User: mirrors `ListUserAccessibleTunnels` / `TunnelEntryRestriction`:
  - `group_permission_grant` rows of the user's `user_tunnel` (persisted grants are authoritative, as in the access check).
    Restricted tunnel (a `created_by_group = 1` grant exists): group covers only its entry rows (no rows → nothing, exactly
    like the access check). Unrestricted tunnel (direct grant came first): group covers its entry rows, or all entries if it
    has none but still contains the tunnel.
  - Active `package_subscription` (status 1) → `subscription_package_tunnel_group` → group membership (package grants all
    entries; group covers its entry rows or all).
  - Only groups with `status = 1` are named; everything not covered → 「其他」 on the client. Group names the user has no
    grant for are never returned.

### Frontend — pills above the picker (`vite-frontend/src/pages/forward.tsx`)
- 「线路分组」 pill row (full row width) above 选择隧道, only when ≥ 2 pills; count = tunnel options in the group; order inx,
  id, 「其他」 last; selected = primary flat tinted, others bordered neutral; group colour dot.
- Dropdown = existing grouped listbox built from the chosen group's options only (same sections/look).
- Default: create → last picked group (localStorage per user id) else first pill; edit/copy → first pill containing the
  rule's tunnel, else 其他. Switching clears a selection not in the new group; edit mode restores the original tunnel when
  switching back to a group that contains it. WG mode: no pills.

## Checklist
- [x] Backend repo method + handler wiring
- [x] Backend tests (SQLite + PostgreSQL): admin per-entry + multi-group, user 2 groups + direct (其他), foreign group names
      hidden, disabled group, package path, backward compatibility
- [x] Frontend pill layer + filtering + default/switch logic
- [x] tsc / lint / build, bundle size vs 5 MiB
- [x] Prod-copy gate: admin + real user, union of pills == today's option set; query time
- [x] Screenshots (desktop 1440 + H5 390×844, light + dark), reviewed; gallery on :8791
- [x] Push branch, CI green
- [x] final.md / summary.md

## Results
- Prod-copy gate (online backup of fork.83 prod SQLite, test under `unshare -n`, copy sha unchanged): union of pills ==
  today's option set for admin (54 options / 42 tunnels: 深港 10, 沪日440 12, 沪港666m 11, 沪日IEPL 12, 其他 9), user 4
  test (深港 10, 沪日440 12), user 8 @jiuwei (沪日440 12, 沪港666m 11, 其他 1), user 12 mraruer (沪日440 12 → one pill,
  layer hidden). 0 lost / 0 gained. The 12 two-group tunnels show in both 沪日 groups with different entries (per-entry
  rows), so no single option is in two pills on prod.
- Extra query on the prod copy: admin 0.12 ms, users 0.09-0.11 ms (best of 20); admin list 0.54 → 0.91 ms.
- Frontend: tsc clean; eslint errors 17 = base 17 (new lines only add prettier-style warnings like the rest of the file);
  build OK, largest chunk 2,987,472 B (+2,997 B), dist total +3,444 B, limit 5 MiB.
- Gate extended to user 11 @sure (single group, active; user 12 is disabled on prod and loops to logout in the UI): 12 = 12.
- Screenshots: 50 shots (desktop 1440 + H5 390×844, light + dark), 56/56 automatic checks, 0 page errors; demo panel on a
  prod-copy DB inside a loopback-only network namespace, every write route aborted in the browser. Gallery under the
  token dir of the :8791 preview server (`…/rule-picker-group-layer/`), not a guessable path (real names/IPs in shots).
- Review fix: colour dots only when groups have different colours (prod groups all keep the default blue).
- Go tests: new repo tests pass on SQLite + PostgreSQL, new contract test passes; full suite has 10 failures, all failing
  identically on the untouched fork.83 base (2 handler + 8 contract), none new.
- CI Build Check green on 704866b9. No tag, no release, no prod change (release = panel only, no DB change; rollback =
  fork.83 image).

Status: RELEASED as 3.0.27-fork.84 (panel only), on prod since 10-24 09:47 TW. Rollback: `bash /opt/flvx-svc/rollback/pre-fork84-20261024T014647Z/rollback.sh` (no --restore-db needed).

## Release (fork.84, panel only)
Branch `maintenance/3.0.27-fork.84-rule-picker-group-layer` = feature head 5b48d2c5 fast-forward (already on top of
fork.83 maintenance head 9ffec7f7, no conflicts). Gate waived by user 10-24; release worker reviewed the gallery.
- [x] Release worktree, branch from feature head (fast-forward over fork.83)
- [x] Gallery review (H5/desktop, light/dark): no defect (pills wrap on H5, readable light+dark, picker unchanged), no re-shoot
- [x] go build + go test ./... SQLite + PostgreSQL vs fork.83 base failure list (both identical to base: 15 env failures, 17 ok pkgs)
- [x] Frontend tsc / build / lint (tsc 0, build ok main 2,987.51 kB, changed files 17 err = base, warnings +30 prettier only)
- [x] Prod-copy gate re-run (fresh read-only copy, union of pills == plain option set), copy deleted — ALL_EQUAL: admin 54, test 22, @jiuwei 24, @sure 12, mraruer 12; 0 lost / 0 gained; grouped list +0.2–0.9 ms
- [x] Push, CI green (40050448432), tag 3.0.27-fork.84 → b683ce04, Images 40050567013, Release Latest with the fork.83 asset set (10 assets, amd64+arm64, scripts pinned)
- [x] Prod: validated DB backup + rollback dir pre-fork84-20261024T014647Z, upgraded /opt/flvx-svc 09:46:57–09:47:06 TW, prune (pre-fork82 + fork.81 images removed)
- [x] Prod checks: version.json local+public fork.84, healthy, 30/30 online (29 fork.79 + node 48 fork.82), no migration (2nd restart clean), 线路分组 in served bundle; real-JWT read-only /tunnel/user/tunnel: imolr 54 / test 22 / @jiuwei 24 / @sure 12 options, pill union == pre-upgrade set (0 lost, 0 gained), users see only own group names, unauth → 401; config tables fingerprint-identical (only landing_node traffic counters moved)
- [x] After 10 min: backend 25.1 MiB / 2.6–3.8 % CPU (fork.83: 31.1 MiB / 2.0–4.0 %), DB 56.77 MB (unchanged), 1 log line/10 min (hourly node_metric maintenance; fork.83: 0), 30/30 online
