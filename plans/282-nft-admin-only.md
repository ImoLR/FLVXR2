# 282 — 普通用户禁用 NFtables 转发模式（协议过滤被绕过）

Task `nft-admin-only` (11-18 TW). NFtables mode = kernel DNAT on every hop, so the tunnel 协议过滤
(SOCKS / HTTP / TLS / 屏蔽其它协议, enforced inside gost only) never sees that traffic. A normal user could pick
「NFtables 模式」 and walk around the filter on 专线 / 线路机.

## Rule
A forward may be `nftables` only if the actor is an admin AND the forward's owner is an admin (role 0).
One helper `ensureForwardModeAllowed` (go-backend/internal/http/handler/forward_mode_guard.go) gates every path
that sets or keeps the mode. Refusal message:
「NFtables 模式仅管理员自己的转发可用；普通用户转发只能用 Gost 模式（协议过滤只在 Gost 模式生效）」.
WG 隧道 stays admin-only as before.

## Checklist
- [x] Worktree from fork.148, branch maintenance/3.0.27-fork.149-nft-admin-only
- [x] Backend helper + create / update guard
- [x] Audit every other path that writes forward.mode / owner; guard same-class holes
- [x] Frontend: non-admins see no 「转发模式」 select (mode fixed gost); admin + non-admin owner → NFtables disabled with hint
- [x] Tunnel page note extended 「普通用户转发只能用 Gost 模式」
- [x] Handler tests (user create nft refused, user update gost→nft refused, admin self ok, admin for user refused, batch/copy paths, WG admin-only)
- [x] go vet + go test ./... (-timeout 30m) + npm run build
- [x] Screenshots user/admin 添加转发 desktop + H5 → gallery /nft-admin-only/
- [x] Ledger row 282
- [ ] Release fork.N (push, tag, CI, images, assets, Latest)
- [ ] Prod backup + rollback script + prune, panel upgrade, health checks
- [ ] Convert the 5 prod nftables forwards (621 canary, 106, 98, 6, 153) to gost via admin API; verify
- [ ] Filter-hit counter ~20–30 min after conversion
- [ ] Ledger / briefs / memory updated, worktree + caches cleaned

## Notes
- Guarded paths: forwardCreateWithReq (also idempotent create + chain replays), forwardUpdateWithReq (also chain edit/detach
  replays; an edit that keeps a legacy user nft mode is refused — saving as gost converts), forwardBatchChangeTunnel
  (normal user's nft rule refused per row), backupImport (normal user's nft rows rewritten to gost, listed in
  `nftables_to_gost`). Chain create/driver hardcode gost; 利群备线 creates gost; no API changes forward.user_id.
- Tests: TestForwardNftablesAdminOnly, TestForwardBatchChangeTunnelNftUser, TestNormalizeForwardImportModes
  (sqlite + PG), TestEnsureForwardModeAllowedUnit. TestForwardCreatePartialDispatchRollback/nftables now uses an
  admin-owned rule (it created nft for user 7, which the new rule refuses).
- Full `go test -p 1 -timeout 30m ./...` (SQLite + PG 18): all ok except that one expected failure, fixed + re-run green.
- Screenshots: http://104.145.236.29:8791/nft-admin-only/ (lab runs/nft-admin-only/lab, fresh DB).
