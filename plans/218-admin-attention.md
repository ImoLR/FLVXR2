# 218 — admin-attention: ⚠ marker on 落地 server cards when 设置 needs the admin

Base: tag `3.0.27-fork.117`. Branch `feature/3.0.27-admin-attention`. BUILD ONLY (no release, prod untouched).

## Why
2026-11-05 evening (prod fork.117): Breadcloud US showed 「禁止端口扫描」 ✗ after its agent OTA
(22:17:25 TW). The fix was the 「保存时重启落地 Core 以升级」 checkbox in the server's 「设置」, but the admin
did not see that 设置 was waiting. Prod log: the server list was loaded at 22:17:35 (10 s after the OTA,
before the agent's first report set `core_stale_epoch` / `scan_state=core_update`) and not reloaded until
22:25:39, while the node 「路由规则」 dialog was reopened 4× and showed the fresh 「需要重启」 label. So the
card's existing amber 「Core 待升级」 pill could not show (stale list), and when it does show it is easy to miss.

User rule (binding): every state that needs an admin action gets a small ⚠ on the card/button that leads to it.

## Design
- One helper `serverAdminAttention(server)` (frontend, `pages/landing/admin-attention.ts`) is the single
  place deciding whether a server needs the admin → `{level: "warn" | "error", reasons: [{text, section}]}`.
- Backend: admin server list gets one read-only `scanGuard: {state, label}` per server (same function as the
  内置 per-server row; global switch read once per request; agent node already loaded) and
  `egress.withheldNodes` (parsed from the stored egress warnings with the producer's format string).
  No new endpoint, no polling, no DB change.
- Card (admin only, add-only): amber/red ⚠ on the 「设置」 button (option A) and/or a tiny ⚠ next to the
  server name (option B); title + tap → opens 设置. Existing pills unchanged.
- 设置 dialog: same ⚠ on the warning block / section title that needs the action; scan-guard state line
  added under 出站与路由 (it was not shown there before).
- Stale list fix (trivial): closing the 路由规则 dialog reloads the list once when the dialog's fresh
  禁止端口扫描 state differs from the card's (event-driven, no polling; no mismatch → no request).
- Header: 「N 台服务器需要处理」 (admin, N > 0), tap scrolls to the first one.

## Decisions
- Shared servers: states only 妙妙屋 can fix (Helper 需升级, share Core restart/upgrade, share guard/scan read
  errors) get **no marker** (not a neutral one): the 「共享详情 · 异常」 button already covers 妙妙屋 problems.
  A shared server's 出站与路由 `error` still counts (often FLVX's own rules failing to compile).
- Waiting states (Core upgrade / restart already confirmed, pending sync) are not actions → no marker.
- Reasons are grouped by the action: upgrade the node program / confirm the Core upgrade / confirm the
  one restart / failures (red). Recommended placement: option B (设置 corner + next to the name).

## Checklist
- [x] Root cause checked (which signal the card missed) — prod read-only
- [x] Backend: `scanGuard` + `egress.withheldNodes` in admin server view, unit test
- [x] Frontend: `serverAdminAttention` helper (comment: new admin-action states go here)
- [x] Card ⚠ on 设置 button (+ optional name marker), tooltip / long-press, tap opens 设置
- [x] 设置 dialog section markers + scan-guard line
- [x] 路由规则 close → reload list when it saw an admin-action scan state
- [x] Header 「N 台服务器需要处理」
- [x] Gates: tsc / lint / build vs base; go test ./... green
- [x] Demo panel + screenshots (H5 390 / desktop 1440, light/dark), gallery /root/mmwx-preview/admin-attention/
- [ ] Push, CI green
- [ ] Ledger row, final.md / summary.md, cleanup
