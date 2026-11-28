# 327 — 我的节点 tree (落地 → 我的节点, 节点 → 服务器)

User 2026-11-28: a friend coming from 妙妙屋 finds 落地 / 转发 / 我的节点 / 节点 confusing,
and 复制链接 lives in two places. Agreed restructure (build + screenshots only, no release):

- Menu: 「节点」(/node, admin) → 「服务器」; 「落地」(/landing) → 「我的节点」 (paths unchanged).
- 服务器 page gets 妙妙屋接入设置, 出站与路由, 落地全局设置 and the landing attention banners.
- 我的节点 page = one tree: server groups (header = the old server card facts) → node cards →
  children (转发 · name / 链式出口 → 外部节点), last group 我的外部节点 → children
  (线路机加速 · 线路 / 端口转发 · name). Up to 3 children, more folded.
- Forward page: 手动填写 → 「复制地址」; 面板节点 / 外部节点 → 「复制链接 ›」 (jumps to the
  tree, ring on the child row + its 复制链接), 「复制」 → 「复制一条」.

Branch `feature/3.0.27-327-my-nodes-tree` (from fork.179 branch @ 0c3b70d8). Panel only.

## Checklist

- [x] Backend: `POST /api/v1/landing/tree` (external nodes of the caller — admin `all` = every user —
      + plan-108 chain forward → external node map), Repository method, contract test (user isolation)
- [x] Menu labels admin + H5 (服务器 / 我的节点) + navigation texts (节点页 → 服务器页, 落地页 → 我的节点)
- [x] 服务器 page: view switch 服务器 | 出站与路由 | 妙妙屋接入, 落地全局设置 button, attention + scan banners
      (查看 → 我的节点 focus), ⚠ on 「落地设置」
- [x] 我的节点 page: server-group tree (header facts, ＋创建节点, 设置 / 共享详情), empty granted servers,
      node cards + child rows with connector lines + fold, 链式出口 child
- [x] 我的外部节点 group (cards, children, 被 N 个面板节点用作链式出口, 管理 = existing manager,
      unlisted pasted chain targets)
- [x] Admin: 只看我的 / 全部用户 switch, owner chips, search; chain-only servers only in 全部用户
- [x] `?focus=forward:<id>` / `node:<id>` / `server:<id>` / `external:<id>` arrival: open group, unfold,
      scroll, gold outline on the row + ring on 复制链接 (15 s or next click/tap)
- [x] Forward page: 复制地址 / 复制链接 › / 复制一条 (table, compact table, cards)
- [x] go test ./... + contract tests, npm run build (tsc + vite) + eslint on changed files
- [x] Lab + screenshots (desktop 1440 + 390 phone, light + dark) a–i, gallery /root/mmwx-preview/my-nodes-tree/
- [x] Push, CI green (CI 46142416717: frontend, backend, PG contract, agent all green), ledger row 327 待用户看图, summary

## What changed

Backend (additive, no DB migration):
- `repo.LandingTreeData(owner, all)` (repository_landing_tree.go): external nodes of one owner (or all
  + owner names) and every plan-108 chain forward → the external node it was made from. A chain keeps a
  copy of the node, not its id: match within the same owner on the node data (credentials included, tag /
  IP version ignored), else the only node of that owner with the same protocol + address; a pasted link
  not in the list stays 0. Three queries.
- `POST /api/v1/landing/tree` (handler landing_external.go): users always their own; admins their own,
  or every user's with `{"all": true}`. No credentials in the response.
- Contract test `TestLandingTreeContract` (sqlite; postgres when FLVX_POSTGRES_TEST_DSN is set): matching
  rules, user isolation (user asking all, another user, admin own / all), no credentials, 401.

Frontend:
- `pages/landing.tsx` rewritten as the tree (tabs 落地服务器 / 可用落地 / 全部节点 / 出站与路由 gone);
  `pages/landing/my-tree.tsx` child rows / fold / 外部节点 card; `api/landing-tree.ts`.
- Node card: same content and actions; 「链接 / 二维码」 → 「直连链接 / 二维码」, dialog shows the direct
  link (+ 利群 links) only, a line says where the forward links are now.
- `pages/node/landing-panels.tsx` + node.tsx: switch, notices, 出站与路由 / 妙妙屋接入 views, 落地全局设置;
  `LandingAttentionContext` gives 「落地设置」 its ⚠ (enable-button.tsx).
- `pages/forward/link-actions.tsx`: 复制链接 › / 复制地址; forward.tsx rows + cards use it; 复制一条.
- `ChainLinkButton` gets `ring` / `onPressed` (arrival).
- CSS `flvx-landing-tree-one`, `flvx-mytree-*` (violet child line, cyan for 外部节点 children, indigo
  外部节点 group, gold outline glow — outline because the luminous theme pins card shadows).
- Texts: 节点页 → 服务器页, 「节点」页 → 「服务器」页, 「落地」页 → 「我的节点」页, 落地页「全局设置」 →
  服务器页「落地全局设置」.

## Decisions ("your call" items)

- 服务器 page layout: one segmented switch 「服务器 | 出站与路由 | 妙妙屋接入」 above the toolbar (grey
  segmented control, unlike the coloured group pills), 「落地全局设置」 as a header button on the right,
  the two notices under it (shown in every view). Open view not remembered (always 服务器).
- Chain-only (线路机 exit) servers: a server group only in admin 全部用户 (with 链式 · N and 设置); never
  for users; their chain copies show only as 线路机加速 children of the 外部节点.
- Admin 只看我的: every non-chain-only server is listed (empty ones under 「还没有我的节点的服务器」), so
  every server's 设置 / 共享详情 stays one tap away. Users: granted servers without nodes under
  「可以创建节点的服务器」.
- Pasted 线路机 links that are not in the 外部节点 list get their own card 「未加入外部节点列表」, so every
  复制链接 › has somewhere to land.
- Glow ends after 15 s or at the next click / tap anywhere (not only on the row).
- 「管理」 in the 外部节点 group opens the signed-in account's own manager (also in 全部用户).
- Forward page: 复制链接 › shows where 复制链接 showed before (chain rules only when active); 复制地址 on
  手动填写 forwards with an entry address (not on wg_path).

## Results

- go test ./... (go-backend): all 30 packages ok; TestLandingTreeContract sqlite PASS (postgres skipped
  locally, no DSN).
- npm run build (tsc + vite): ok. eslint on changed files: new files clean; node.tsx / forward.tsx error
  counts identical to the base (pre-existing a11y errors only).
- Lab (synthetic data, isolated netns, real paneld + branch dist): 35 + 1 checks ok, 0 page errors.
  Gallery http://104.145.236.29:8791/my-nodes-tree/ (60 shots).

## Release needs

Panel only (backend binary + frontend), no DB migration, no agent / OTA. Waits for the user's OK on the
screenshots.
