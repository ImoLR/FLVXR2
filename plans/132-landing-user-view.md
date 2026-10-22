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
- [x] Repo helper: one place for the total-cap lookup (`LandingUserSetting.MaxNodes`, default 5) used by
      `createLandingNode`, `GetLandingUserSetting` and the new own-count helper (plan 131 changes one place)
- [x] Repo helper `LandingUserNodeCounts(userID)` → total cap, own total, own count per server (one grouped query)
- [x] `landingServerView` non-admin: own `nodeCount`, `myRemaining`; strip server-wide aggregates, admin limits,
      sync internals; `sharedCaps` reduced to `protocol_caps`
- [x] `landingServerList`: aggregates computed once per request (no per-server full scan); admin output unchanged
- [x] Frontend: `pending` label 「正在同步，请稍后」 + bounded auto-refresh (5 s, ≤ 2 min, only while pending, paused
      when the tab is hidden)
- [x] Frontend server card (non-admin): hide SharedCaps block, 最近成功同步, 同步异常/notice, 妙妙屋模式 chip, source
      label; grid → 我的节点 / 还可创建 (已达上限 disables 创建节点) / 可用至
- [x] Frontend 我的节点 cards: drop 连接数 line for non-admins
- [x] Create dialog (non-admin): 「流量上限、倍率和有效期由管理员设置」, no port range / limit numbers
- [x] Contract tests (SQLite + PG if available): own count, both caps (prod repro), forbidden fields absent, admin
      field set unchanged
- [x] Backend suite baseline vs after; frontend tsc / lint / build
- [x] Local lab: /landing/server/list request count + latency before/after
- [x] Screenshots desktop + H5, light + dark (user + admin before/after); gallery on mmwx-preview
- [x] Push branch, CI green (run 39781431592 on e1344124), summary

## Decisions
- Non-admin source label (妙妙屋共享 / FLVX 自有落地) and the 妙妙屋 mode chip are hidden: they only reveal infrastructure.
- Non-admin create dialog drops the port range too (「端口自动分配。流量上限、倍率和有效期由管理员设置。」).
- Non-admin server JSON is a blocklist on the model map (`landingServerUserHidden`): admin limits/defaults, core sizing,
  revisions, sync time/error, remoteServerId, created/updated time, chainOnly, egress internals; server-wide
  aggregates are never added. Kept: name, source, status, online, protocols, flowType, trafficRatio, availableUntil,
  egressLabel, publicHost / ports / Reality / AnyTLS / Snell / Mieru defaults (create dialog + links), maxNodesPerUser,
  sharedAvailable, landingSupported, nodeCount (own), myRemaining, sharedCaps = {protocol_caps}.
- `nodeCount` / `myRemaining` count every landing_node row of the user (delete-pending included), exactly like
  `createLandingNode`; total cap via the single helper `landingUserSetting` (default `DefaultLandingUserMaxNodes` = 5).
- 「正在同步，请稍后」 also shows in 监控 (shared `landingStatus` map); nothing else in 监控 changed.

## Results (lab, 2026-10-22)
- Backend full suite (SQLite + PostgreSQL): 15 failures before = 15 after (same set); new
  `TestLandingUserServerViewContract` passes on both; its admin field-set/aggregate assertions also pass on the
  unmodified base. `TestLandingExtraProtocolsHandlers` updated: users no longer get `server_mode` / `busyPortCount`.
- Frontend: tsc clean, eslint no new warnings, build OK.
- Screens: 56 (before/after), 120/120 automated checks, 0 page errors; pending → 正常 flip in ~1.7 s after Custom
  confirms (1 node/list request), polling stops when nothing is pending (0 requests in 12 s).
- /landing/server/list p50: lab 2 servers admin 1.88→1.55 ms, user 1.77→1.30 ms; 42 servers / 2004 nodes admin
  1055→39 ms, user 1043→8 ms (admin response bytes identical, user 41.7 kB → 22.0 kB).

## Release (fork.72, user approved 2026-10-22 ~21:00 TW 「可以发布吧」)
Plan 132 alone → `3.0.27-fork.72`, panel/frontend only, no agent OTA. Branch
`maintenance/3.0.27-fork.72-landing-user-view` from `feature/3.0.27-landing-user-view` (401aa570, already on top of
fork.71 f7b9c5bc — no rebase). Plan 131 must rebase onto this branch before its own release.
Run dir `/root/flvx-workers/runs/release-fork72-landing-user-view`.

- [x] Pre-upgrade prod capture: admin + user 26 `/landing/server/list` (fields, latency), panel CPU/RSS
- [x] Push branch, CI green
- [x] Tag `3.0.27-fork.72`, Images run green, release Latest with the same asset set as fork.71
- [x] Prod backup `pre-fork72-*` (validated SQLite, compose/.env, local image tags, rollback.sh) + prune to 2
- [x] Upgrade prod panel to fork.72, health + version.json
- [x] Verify: 29/29 online, agents unchanged, user 26 view (nodeCount 1, myRemaining 1, no stripped fields), admin
      field set unchanged, 「正在同步，请稍后」 in served bundle, CPU/RSS + latency after

### Release results (2026-10-22, 台湾时间)
- Branch CI 39783306139 green; tag `3.0.27-fork.72` → 74f5890b; Images 39783538465 green; release Latest, not
  prerelease, 10 assets = fork.71 set, compose pinned `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.72`, install scripts
  PINNED_VERSION/REPO OK, gost sha256 OK, ghcr amd64+arm64.
- Rollback point `/opt/flvx-svc/rollback/pre-fork72-20261022T132042Z` (→ fork.71; SQLite quick/integrity ok,
  local image tags `local/flvxx-*:pre-fork72-20261022T132042Z`); kept + `pre-fork71-*`; pre-fork70 + fork.69 images
  pruned.
- Prod upgraded 21:21:30 TW, healthy, version.json `3.0.27-fork.72-mvjk1atg`; 29/29 online, agents unchanged
  (21 = fork.70, 28 × fork.67), forwards 34 (33 running) unchanged, 0 error log lines.
- User 26 (in-memory JWT, read-only) server 3 「Boil Hinet 马年」: nodeCount 2 → **1**, myRemaining **1**, stripped
  fields absent (23 keys dropped incl. connections/usedBytes/billedBytes/maxConnections/defaultMax*/busyPortCount/
  sharedSyncError/sharedNotice), sharedCaps = protocol_caps only; response 1967 → 833 B.
- Admin list: nested key set identical before/after, 8736 B both.
- Served bundle `index-DrfR14Lr.js` contains 「正在同步，请稍后」, no 「等待同步」.
- Resources: backend CPU (60 s) 4.65 % → 4.98 % (7.67 % right after restart), RSS 37.9 → 32.0 MiB;
  `/landing/server/list` p50 admin 3.32 → 2.83 ms, user 26 1.42 → 1.33 ms.
- Plan 131 must rebase onto `maintenance/3.0.27-fork.72-landing-user-view` before its own release.
