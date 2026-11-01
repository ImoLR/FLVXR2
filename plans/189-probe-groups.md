# 189 — 探针自定义分组（按地区 / 按自定义分组）

Branch `feature/3.0.27-probe-page` (same as plans 187 + 188). BUILD ONLY + screens; release later in one batch with
184 + 187 + 188. Brief: `/root/flvx-workers/runs/probe-groups/brief.md` (user 2026-11-02, coordinator 6e28c3e5).

## Design (as approved, nothing more)
- Two grouping modes on `/tz`: 按地区 (existing pills) and 按自定义分组.
- Admin (设置 → 公开探针页): group list (add / rename / delete with confirm / move up-down) + a 「分组」 multi-select
  column in the per-machine list (a machine may be in several groups) + 「默认分组方式」 按地区 (default) / 按自定义分组.
- Probe toolbar: segmented 「地区 | 分组」 (only when ≥ 1 non-empty group is published), then the pills of the mode
  (全部 + each group with count, 「未分组」 only if any). The page has no section headers today → none for groups either;
  a machine is listed once in 全部. Visitor choice in localStorage, default = admin setting. Table mode + search + sort
  work inside the mode.
- Hidden machines never appear in a group; groups without a visible machine are not sent at all.

## Storage (additive config rows, no schema change)
- `probe_groups`: JSON `[{"id":1,"name":"IXP","nodes":[3,5]}]` in display order. ≤ 30 groups, name 1–20 chars (trimmed,
  no control characters), unique (case-insensitive), node ids must exist on save; deleted nodes are pruned on read.
- `probe_group_mode`: `region` (default) | `group`.
- Both are admin-only (not in the public config whitelist).

## Public snapshot
- `groups:[{"name":…}]` (only groups with ≥ 1 shown machine, in order) + `group_mode` — both omitted when no group is
  published. Per node `g:[0,2]` (indexes into `groups`, omitted when none). No group ids, no node ids.

## Checklist
- [ ] Backend: settings load/validate (`probe_groups`, `probe_group_mode`) incl. generic config API
- [ ] Backend: admin GET/save carry groups + default mode (prune deleted nodes)
- [ ] Backend: snapshot `groups` / `group_mode` / node `g` (internal/probe BuildSnapshot)
- [ ] Unit tests (probe package + handler validation)
- [ ] Contract tests SQLite + PostgreSQL (CRUD/validation, multi-membership, hidden, empty group, deleted node, key set)
- [ ] Frontend probe page: mode switch, group pills, 未分组, localStorage, merge of `g`/groups
- [ ] Frontend settings card: group list (add/rename/delete-confirm/order), per-machine multi-select, default mode
- [ ] go build / vet / test (SQLite + PostgreSQL); tsc / build / lint vs base
- [ ] Snapshot size delta measured
- [ ] Demo + screens gallery `/root/mmwx-preview/probe-groups/` (:8791)
- [ ] Push, CI green
- [ ] release-notes.md updated (184 + 187 + 188 + 189 batch, merge-tree re-check)
- [ ] Summary `runs/probe-groups/summary.md`

## Results
(filled in at the end)
