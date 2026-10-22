# 132 — landing-user-view: user-facing 落地 page cleanup

Base: `maintenance/3.0.27-fork.71-user-rule-limit` (f7b9c5bc). Branch `feature/3.0.27-landing-user-view`.
Build + screenshots only — no release, no prod change (waits for the user's screenshot approval).

## Problem (user 26 `leekao` on prod, 2026-10-22)
- 「等待同步」 should read 「正在同步，请稍后」 and flip by itself.
- The 落地 server card is an admin view; normal users see 妙妙屋共享上限 / 端口段 / 允许协议 / 总连接数, server-wide
  traffic and connection limits.
- 「节点数 2 · 每人最多 3」 although the user made one node: `landingServerView` counts every user's nodes on the server
  (admin node 10 + user node 95 on server 3) and returns server-wide connections / traffic / caps to non-admins.

## Tasks
- [ ] Repo helper: one place for the total-cap lookup (`LandingUserSetting.MaxNodes`, default 5) used by
      `createLandingNode`, `GetLandingUserSetting` and the new own-count helper (plan 131 changes one place)
- [ ] Repo helper `LandingUserNodeCounts(userID)` → total cap, own total, own count per server (one grouped query)
- [ ] `landingServerView` non-admin: own `nodeCount`, `myRemaining`; strip server-wide aggregates, admin limits,
      sync internals; `sharedCaps` reduced to `protocol_caps`
- [ ] `landingServerList`: aggregates computed once per request (no per-server full scan); admin output unchanged
- [ ] Frontend: `pending` label 「正在同步，请稍后」 + bounded auto-refresh (5 s, ≤ 2 min, only while pending, paused
      when the tab is hidden)
- [ ] Frontend server card (non-admin): hide SharedCaps block, 最近成功同步, 同步异常/notice, 妙妙屋模式 chip, source
      label; grid → 我的节点 / 还可创建 (已达上限 disables 创建节点) / 可用至
- [ ] Frontend 我的节点 cards: drop 连接数 line for non-admins
- [ ] Create dialog (non-admin): 「流量上限、倍率和有效期由管理员设置」, no port range / limit numbers
- [ ] Contract tests (SQLite + PG if available): own count, both caps (prod repro), forbidden fields absent, admin
      field set unchanged
- [ ] Backend suite baseline vs after; frontend tsc / lint / build
- [ ] Local lab: /landing/server/list request count + latency before/after
- [ ] Screenshots desktop + H5, light + dark (user + admin before/after); gallery on mmwx-preview
- [ ] Push branch, CI green, summary
