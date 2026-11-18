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
- [ ] Audit every other path that writes forward.mode / owner; guard same-class holes
- [ ] Frontend: non-admins see no 「转发模式」 select (mode fixed gost); admin + non-admin owner → NFtables disabled with hint
- [ ] Tunnel page note extended 「普通用户转发只能用 Gost 模式」
- [ ] Handler tests (user create nft refused, user update gost→nft refused, admin self ok, admin for user refused, batch/copy paths, WG admin-only)
- [ ] go vet + go test ./... (-timeout 30m) + npm run build
- [ ] Screenshots user/admin 添加转发 desktop + H5 → gallery /nft-admin-only/
- [ ] Ledger row 282
- [ ] Release fork.N (push, tag, CI, images, assets, Latest)
- [ ] Prod backup + rollback script + prune, panel upgrade, health checks
- [ ] Convert the 5 prod nftables forwards (621 canary, 106, 98, 6, 153) to gost via admin API; verify
- [ ] Filter-hit counter ~20–30 min after conversion
- [ ] Ledger / briefs / memory updated, worktree + caches cleaned

## Notes
