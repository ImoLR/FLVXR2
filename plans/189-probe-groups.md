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
- [x] Backend: settings load/validate (`probe_groups`, `probe_group_mode`) incl. generic config API
- [x] Backend: admin GET/save carry groups + default mode (prune deleted nodes)
- [x] Backend: snapshot `groups` / `group_mode` / node `g` (internal/probe BuildSnapshot)
- [x] Unit tests (probe package + handler validation)
- [x] Contract tests SQLite + PostgreSQL (CRUD/validation, multi-membership, hidden, empty group, deleted node, key set)
- [x] Frontend probe page: mode switch, group pills, 未分组, localStorage, merge of `g`/groups
- [x] Frontend settings card: group list (add/rename/delete-confirm/order), per-machine multi-select, default mode
- [x] go build / vet / test (SQLite + PostgreSQL); tsc / build / lint vs base
- [x] Snapshot size delta measured
- [x] Demo + screens gallery `/root/mmwx-preview/probe-groups/` (:8791)
- [x] Push, CI green
- [x] release-notes.md updated (184 + 187 + 188 + 189 batch, merge-tree re-check)
- [x] Summary `runs/probe-groups/summary.md`

- [x] Extra (found while taking screens): the 187 settings card widened the whole 设置 page on phones (all cards 410 px
      on a 390 px screen) → left grid column `min-w-0`, 显示名 box under the name on narrow screens

## Results (2026-11-02, BUILD ONLY — no tag, no prod change)
- Commits: 21fa65cc (backend), e8ebe395 (frontend), plan doc. CI Build Check green (41344336566, 41346268955).
- Storage: config rows `probe_groups` + `probe_group_mode` (additive, admin-only, no schema change). Public snapshot:
  `groups:[{name}]` + `group_mode` (omitted when nothing is published) and per node `g:[idx]`.
- Tests: `go vet` = same single pre-existing finding as base (leikwan/testapi unkeyed fields); `go test ./...` with
  FLVX_POSTGRES_TEST_DSN → 22/22 packages ok (SQLite + PostgreSQL). New: `TestBuildSnapshotGroups`,
  `TestNormalizeProbeGroups`, `TestProbeGroupsContract` (sqlite + postgres).
- Frontend: tsc 0; build ok; eslint whole project 94 errors / 3753 warnings, none in changed files (changed files: 0
  errors, same 2 pre-existing prettier warnings in config.tsx as base).
- Sizes: snapshot (25 machines, 2 demo groups, same frame) raw 7914 → 8068 B (+154), gzip 2317 → 2382 B (+65).
  JS gzip: /tz probe-app +435 B, shared sparkline chunk +82 B; admin-main +2.4 KB.
- CPU: BuildSnapshot 5.7 µs → 18.3 µs worst case (30 groups × 25 machines), once per 2 s tick only while watched.
- Demo + screens: 37 shots, 67/67 checks, 0 page errors — http://104.145.236.29:8791/probe-groups/
- `git merge-tree` with origin/feature/3.0.27-latency-tag-region (39a4826d): clean. Release notes:
  `runs/probe-page/release-notes.md` (batch 184 + 187 + 188 + 189).
