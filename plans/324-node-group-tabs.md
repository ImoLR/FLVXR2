# 324 — 节点页分组标签（与探针分组同一套）+ 利群入口分组 + 分组状态标记

Base: production head `maintenance/3.0.27-fork.178-entry-labels` @ a4547740 (tag 3.0.27-fork.178).
Branch: `feature/3.0.27-324-node-group-tabs` (worktree `/root/projects/flvxx/flvxx-324`).
Work dir: `/root/flvx-workers/runs/node-group-tabs/` (lab, screenshots, summary).
Scope: **build + screenshots only** — no release, no OTA, no production change. The
user approves the screenshots first; then it joins the next release batch (ledger row 324).

## Request (user 2026-11-28)
With many servers the 节点 page is hard to search. Group them like the 探针 groups
(线路机 / IEPL / IPLC / IXP …), admin-defined; machines in no group → 「其他」; the
page opens on 「综合」; a group holding an offline machine or a machine in a bad state
gets a marker on its tab.

## Findings (read-only)
- Two group systems: old `node_group` (single membership via `node.group_id`, node page
  manager modal + 分组 accordion view + list 「按分组筛选」 + edit-dialog select) and the
  plan 189 探针分组 (`vite_config.probe_groups` JSON `[{id,name,nodes,old?}]`, multi-membership).
- `old` in a probe group = 救命版 (old Flux panel) node ids (plan 195). The admin probe
  API sends them as negative ids; storage keeps them apart. The node page only knows
  FLVX nodes, so a node-page save must carry each group's stored `old` list unchanged.
- 利群汇聚 reads its entry machines from `node_group` id = `leikwan_setting.entry_group_id`
  (`leiKwanEntryNodes`, ~30 uses). That stays the source of truth.
- `node/update` writes `group_id` from the payload (missing → NULL), so the edit dialog
  must keep sending the node's current `groupId` (hidden old membership kept).
- `/api/v1/node/*` is admin-only in the middleware and the 节点 menu is admin-only, so
  normal users never reach this page (nothing to hide from them).
- `/api/v1/node-group/*` has no admin check in the middleware (pre-existing; noted, not
  changed here except the delete guard below).
- The list view's stored group filter (`filterGroupId`, localStorage) would silently filter
  rows once its select is gone → removed with the select.

## Design (agreed with the user 11-28)
1. Node page uses the probe groups (one source; editing in either place changes both).
   Old node_group groups other than the 利群 entry group are no longer shown on the node
   page (data + API kept). Old 分组 manager modal, 分组 accordion view and list 「按分组筛选」
   leave the page; view button = 卡片 ↔ 列表 (stored `grouped` → 卡片).
2. Tab bar above cards / list: 「综合」 (all, default every visit, not remembered) + groups
   in order + 「其他」 (no group, hidden when empty). Counts after the 到期 filter mode.
   Search searches all machines (typing switches to 综合).
3. 利群入口 = `node_group` row `entry_group_id`, shown as a group with a 利群 🔒 mark,
   admin page only, never on the probe. Rename / add / remove / reorder allowed; delete
   refused (UI + backend). Removing a machine that 利群汇聚 uses is blocked with a message.
4. Status markers on tabs (admin): red ⚠ = offline machines; amber △ = DDNS 失败/待同步/需升级,
   IPv6 RA 待恢复/需处理, DNS 缓存 warning, VPS 厂商 未设置/不在列表, 到期 ≤ 7 天 / 已过期.
   One helper, same functions as the card badges. Popover lists 「名字 — 原因」; click → card.

## Decisions on the "your call" points
- Save path: a small dedicated endpoint `POST /api/v1/node/groups` + `/api/v1/node/groups/save`
  (admin). It writes only `probe_groups` (+ the 利群 group row / members and its position);
  title / domain / hidden / names / mode of the probe stay untouched.
- 利群 group position among the probe groups: `vite_config.node_page_leikwan_pos`
  (index; missing → last). Not in `probe_groups`, so the probe output cannot contain it
  and an older panel (rollback) ignores it.
- Drag sorting: keeps working inside a group tab. The existing handler already re-sequences
  the shown subset inside its own slots of the global order (same as with a search), so
  dragging in a tab only swaps that group's machines among themselves.
- Card / list 「分组」 chip: it showed the old node_group name; it now shows the node's
  unified groups (first + 「+N」), same pill shape; 「未分组」 when none.
- Remove guard blocks: 主入口 of a 利群 grant (user + their 专线), the old 主入口 during the
  24 h 换主入口 grace, the reserved 备用入口. The 备线隧道 entry does not depend on the
  entry group (the backup starts from the 前置 = 主入口), so it is listed in the confirm
  as information, not blocked.

## Tasks
- [x] Investigate both group systems, 利群 dependency, node update, probe `old`
- [x] Backend: `node_groups_page.go` GET + save (merge stored `old`, validation, 利群 rename /
      members / position, remove guard), repo method for the 利群 member change (transaction)
- [x] Backend: node_group delete refuses the current entry group
- [x] Backend tests: delete refusal, rename, add / remove guard (grant / grace), probe output
      never has the 利群 group, `old` kept, other probe settings untouched
- [x] Frontend: API client + `nodeGroupAttention` helper (rule list in one place)
- [x] Frontend: tab bar (desktop + H5 scroll strip), counts, markers + popover, 其他, search → 综合
- [x] Frontend: 管理分组 modal (shared editor + member picker + 利群 row rules, guard message)
- [x] Frontend: remove old 分组 manager / accordion view / list filter; view toggle 卡片 ↔ 列表
- [x] Frontend: node edit dialog multi-select (unified groups, 利群 via the same guard)
- [x] Frontend: card / list chip shows unified groups
- [x] `go test ./...`, `npm run build`, tsc / lint as CI
- [x] Lab (fake data ≥ 10 groups incl. 利群入口, offline / amber / clean / 其他) + screenshots
      desktop + 390 px, light + dark; index at /root/mmwx-preview/node-group-tabs/
- [x] Push branch, CI green; ledger row 324; summary

## Results (2026-11-28, build only)
- Commits on `feature/3.0.27-324-node-group-tabs`: 748b6e71 (backend API + guards + contract
  test), 9cb1b5d4 (frontend), 9c1357a3 (marker colours / phone strip / jump ring / guard text),
  5fe39d82 (no group picker for remote nodes) = head. CI Build Check 46128936926 green.
- `go test ./...` (SQLite) all ok; `TestNodeGroupTabsContract` + `TestProbeGroupsContract` +
  `TestProbeOldPanelContract` also on PostgreSQL 18 ok. `tsc` + `npm run build` ok; eslint clean on
  the new files (node.tsx / node-list-view.tsx keep their pre-existing a11y errors: 7 / 6, same as base).
- Merge dry run with plan 323 (`feature/3.0.27-323-entry-select-endin-avg`): clean.
- Lab (`runs/node-group-tabs/lab`, netns, synthetic data, 33 nodes, 10 groups incl. 利群入口,
  agents simulated via a mocked realtime socket): 44/44 checks; 30 screenshots desktop + 390 px,
  light + dark: http://104.145.236.29:8791/node-group-tabs/
- New: `vite_config.node_page_leikwan_pos` (written on the first node-page save; no DB migration).
  New endpoints `POST /api/v1/node/groups`, `/api/v1/node/groups/save` (admin). `node-group/delete`
  refuses the current 利群 entry group.
- Release needs: panel only (backend + frontend image), no OTA, no DB migration. Rollback = image
  swap; the extra config row is ignored by fork.178.
- Not done on purpose: `/api/v1/node-group/*` still has no admin check in the middleware
  (pre-existing; the brief allowed only the delete guard) — worth a follow-up.
