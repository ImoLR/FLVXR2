# 325 — 旧节点分组接口（/api/v1/node-group/*）只给管理员

Base: `feature/3.0.27-324-node-group-tabs` @ 4c179b25 (on fork.178 a4547740).
Branch: `feature/3.0.27-325-node-group-admin-guard` (worktree `/root/projects/flvxx/flvxx-325`).
Released with 323 + 324 as fork.179 (release doc plan 326).

## Finding (found by the plan 324 worker, verified 11-28)
- Routes `/api/v1/node-group/{list,create,update,delete,assign}` (handler.go ~l.585,
  handler/node_group.go) never check the caller's role.
- The JWT middleware (`middleware/auth.go` `requiresAdmin`) guards `/api/v1/node/` by
  prefix, but `/api/v1/node-group/...` does not start with `/api/v1/node/` → **gap is real**:
  any logged-in normal user could create / rename / delete groups and move any node
  between groups (incl. the 利群入口 group that 利群汇聚 picks entry machines from).
- Callers: only `vite-frontend/src/pages/node.tsx` (`getNodeGroupList`); the 节点 page is
  `adminOnly` in both menus (admin.tsx / h5.tsx) and its data (`/api/v1/node/*`) is
  admin-only already. No App / other project calls it. → no normal-user page needs `list`.

## Decision
- All five endpoints admin only, including `list`, via the same middleware rule as
  `/api/v1/group/` (`requiresAdmin` prefix `/api/v1/node-group/`) → same refusal as every
  other admin endpoint: `{code: 403, msg: "权限不足，仅管理员可操作"}`.
- Plan 324's 利群入口 delete refusal stays in the handler (admin path unchanged).
- Same-class gaps noticed but **not** changed here (outside the approved scope; reported):
  `/api/v1/node-tag/*`, `/api/v1/tunnel-group-new/*`.

## Tasks
- [x] Verify the gap (middleware prefix check, no handler role check)
- [x] Grep every frontend / App caller of node-group endpoints
- [x] Middleware: `/api/v1/node-group/` → admin only
- [x] Contract test: normal user refused on list/create/update/delete/assign (nothing changed
      in DB), admin still OK, 利群入口 delete still refused for admin (SQLite + PG)
- [ ] go vet + tests (SQLite all; PG contract for the new test)
- [ ] Merge into fork.179 release branch (plan 326)
