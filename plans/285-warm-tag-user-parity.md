# 285 — 普通用户转发页的「预热」标签和管理员一样

User 2026-11-18 ~21:55 TW: rule 641 「test ss」 of normal user `test-ns-fx` showed a 预热 tag to the admin
but nothing to the user. Cause: plan 249 gave non-admins only `warmPoolUserView` and only when
`status == "faster" && savedMs >= 1`; the new rule was 「预热中 · 数据不足」 → hidden.
Decision: normal users get the same tag, the same states and the same dropdown detail as admins
(green −N ms, red +N ms + reason, grey 目标不支持预热, grey 预热中 · 数据不足, hop rows).

Privacy guard: a tunnel hop whose next node can't be mapped falls back to the agent-reported address
(a node's IP:port) → for non-admins it is shown as 「下一跳」. Port-forward hop `to` = the rule's own
target (user's own data) → kept. Node names are fine.

Scope: panel only (go-backend handler + frontend comments). No DB change, no agent change, no extra queries.

## Checklist
- [x] Worktree from fork.151, branch `maintenance/3.0.27-fork.153-warm-tag-user` (renamed + rebased onto fork.152 — quic-block released fork.152 at 22:37 TW)
- [x] Backend: non-admins get the full `warmPoolView` (remove user-only view + faster/≥1 ms gate)
- [x] Backend: address guard for unmapped tunnel hops in the user view
- [x] Tests: user view = admin view incl. warming / unsupported / slower + hops; unmapped hop never leaks a node IP
- [x] Frontend: stale plan-249 comments updated (no UI behaviour change needed — dropdown has no admin-only action)
- [x] `go test ./...` (-timeout 30m) vs base; `npm run build`
- [x] Lab screenshots (user: faster / slower / 不支持 / 数据不足 + dropdown, desktop + H5; admin unchanged) → gallery
- [x] Ledger row 285
- [x] Release fork.N (tag, CI, images, assets, Latest)
- [x] Prod backup + rollback script, panel upgrade, health checks
- [x] Prod check: rule 641 admin vs test-ns-fx identical; per-state counts before/after; prod screenshots
- [x] Ledger → 已发布, memory, OTA brief note
- [x] Cleanup (worktree, caches)

## Results (build)
- Backend: `attachForwardWarmPoolStates` gives non-admins `userWarmView(view)` = a copy of the admin view; tunnel hops with
  `ToNodeID == 0` get `To = 「下一跳」`. Removed `warmPoolUserView` + `warmUserMinSavedMs` (users = admins, incl. −0 ms).
- Tests: handler `TestForwardWarmPoolRuleView` (user JSON == admin JSON for faster / unsupported / warming), new
  `TestUserWarmViewHidesUnmappedNodeAddress` (SQLite + PG: admin sees 203.0.113.77:39999, user 「下一跳」, slower port rule
  identical), `TestForwardListCarriesWarmTag`, contract `TestForwardWarmTagContract` updated (owner == admin).
  Full `go test ./...` (PG DSN): all ok except the old contract expectation (updated, rerun ok). `npm run build` ok.
- Lab (synthetic DB + simulated agents, runs/warm-tag-user-parity/lab): 27/27 checks; gallery
  http://104.145.236.29:8791/warm-tag-user-parity/

## Results (release, 11-18 TW)
- Released as **3.0.27-fork.153** (fork.152 was taken by quic-block at 22:37 TW → rebased onto it). Tag → 08426f68,
  CI 44327400426 + Images 44327403627 green, Latest, 10 assets, gost-amd64 sha256 ok.
- Prod panel swap 22:57:11–22:57:18 TW, no DB change. 30/30 nodes online, public version.json fork.153, 0 panel errors,
  state.py identical except one user-made forward (644, 22:52 TW). Rollback:
  `/opt/flvx-svc/rollback/pre-fork153-20261118T145710Z/rollback.sh` (→ fork.152). Backups kept: pre-fork152 + pre-fork153.
- Prod check (read-only API, all 25 normal users' own rules): before = admin saw faster 23 / warming 1 / none 16, users saw
  only faster 22 (+18 none); after (window refilling post-restart, 23:3x TW) = users faster 22 / warming 6 / none 17,
  identical to the admin view on every rule (mismatch 0). Rule 641 「test ss」 (test-ns-fx): no stream after the restart →
  no tag for anyone; 6 short TCP connects to its entry → 「预热中 · 数据不足」 for both, byte-identical (hop to = its own target).
- OTA briefs (ota-old-agents-1119, quic-block) told: node/upgrade now offers/accepts only fork.153 (agent code = fork.152).
