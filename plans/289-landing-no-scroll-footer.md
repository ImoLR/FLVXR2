# 289 — 落地页关位置修正 + 落地服务器分组底部「收起」 + 全部分组列表不记住展开

Base: tag 3.0.27-fork.156 (512ab25b). Branch `feature/3.0.27-landing-no-scroll`. Frontend only, panel only, no DB, no go-gost.

## Part A — 落地 page (user 11-20)
- Both tabs (落地服务器 / 可用落地, 全部节点 / 我的节点): no position correction after a header / footer tap (plan 287
  `useGroupScrollSettle` removed from this page). Tree line + accordion unchanged. Jumps (⚠ 查看, 创建节点, 设置 saved,
  ?server= link) keep their own scrolling.
- No `overflow-anchor` override: the browser's default scroll anchoring keeps the visible content in place where
  supported (Chrome / Android); Safari just reflows. No scroll calls from a tap.
- 落地服务器 / 可用落地 region groups get the bottom 「收起 · 地区名」 footer, in the region colour.
- 转发 / 隧道 / 节点 分组 / 订阅 / 利群汇聚 position correction unchanged.

## Part B — no remembered open groups (user 11-20)
- Open state lives in component memory only; leaving a page or reloading = all collapsed.
- 落地 `useGroupOpenState` (3 lists), 节点 分组 `openNodeGroup`, 隧道 `savedOpenTunnelGroups`, 转发 expanded map (local +
  admin backend config `forward_group_expanded_map` — no longer read or written; stored value left unused).
- 订阅 admin users, 利群汇聚 专线, H5 expand rows: already memory-only (checked).
- Old keys removed at app start (`utils/forget-group-open.ts`, prefix match, all accounts on the browser).
- Kept: 转发 group order, grouping modes, view modes, filters, sidebar_collapsed, probe settings.

## Checklist
- [x] A1 landing.tsx: toggleGroup without scroll settle; comments updated
- [x] A2 admin-groups.tsx: ServerRegionSection footer (region tone CSS)
- [x] B1 useGroupOpenState → in-memory
- [x] B2 node.tsx 分组 open state → in-memory
- [x] B3 tunnel.tsx open state → in-memory
- [x] B4 forward.tsx expanded map → in-memory, no local / backend read or write
- [x] B5 startup cleanup of old keys
- [x] Build: tsc + lint + `npm run build`
- [x] Lab: scrollY per tap on 落地 (admin + user, desktop + 390), footer look, 转发/隧道 glide identical to fork.156,
      navigate-away / reload → all collapsed on every page; gallery /landing-no-scroll/
- [x] Release fork.157 (tag 3.0.27-fork.157 → 4ba5aca7; CI 44716647586, Images 44716651149, Latest, 10 assets, gost sha256 ok)
- [x] Prod backup + rollback script + prune, swap 11-20 18:21:44–18:21:53 TW between OTA idle checks, OTA brief note (rollback /opt/flvx-svc/rollback/pre-fork157-20261120T102144Z/rollback.sh)
- [x] Prod verify: 30/30 nodes (30 redeploys, 0 failed), 64/64 forwards, state identical, light check no regression, log 15 min clean; read-only prod shots (taps 0 scroll frames, 转发 still glides)
- [x] Ledger + memory + cleanup

## Notes
- No scroll anchoring help possible: the tapped header becomes `position: sticky` on open, which suppresses browser
  scroll anchoring by spec (tested `overflow-anchor` variants in the lab: no effect). When a long open group above
  collapses, the tapped header moves up by that height (natural reflow, instant, no glide).
- Lab: A 56/56, B 50/50, C 4/4 (scripts /root/flvx-workers/runs/landing-no-scroll-fix/lab/). Gallery
  http://104.145.236.29:8791/landing-no-scroll/.
