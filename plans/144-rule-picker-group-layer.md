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
- [ ] Backend repo method + handler wiring
- [ ] Backend tests (SQLite + PostgreSQL): admin per-entry + multi-group, user 2 groups + direct (其他), foreign group names
      hidden, disabled group, package path, backward compatibility
- [ ] Frontend pill layer + filtering + default/switch logic
- [ ] tsc / lint / build, bundle size vs 5 MiB
- [ ] Prod-copy gate: admin + real user, union of pills == today's option set; query time
- [ ] Screenshots (desktop 1440 + H5 390×844, light + dark), reviewed; gallery on :8791
- [ ] Push branch, CI green
- [ ] final.md / summary.md
