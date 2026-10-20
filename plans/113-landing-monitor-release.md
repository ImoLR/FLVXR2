# 113 — Release the 落地 monitor tab (plan 111) as fork.59

User (2026-10-20): the server → node layout of plan 111 r2 is fine; small text
tweaks, then release directly.

## Changes on top of plan 111 r2
- Connection counts: no 「连接 / 连接数」 text, a plug icon (like 妙妙屋) + `当前/上限`
  (node cards, phone list rows, table column header, top summary chip, server tile).
- IP limit: `IP 上限 3` / `IP 上限 不限` → `IP 3` / `IP ∞`; table header 「IP」.
- Every 「不限」 in the tab → `∞` (connections, IP, traffic `x / ∞ GB`); server tile
  hint 「当前 / 上限」 dropped.
- The panel only stores the online-IP limit, not the current online-IP count
  (agent and Helper don't report it), so IP shows the limit only.

## Branch
`maintenance/3.0.27-fork.59-landing-monitor` = fork.58 (96d2b461) + plan 111
commits e652b3f3 893284bb bd21338c ae3bd412 96c02b85 263b59eb fafd8f7a
(conflicts in handler.go / landing.go / landing_runtime.go: kept both the
fork.58 egress parts and the monitor parts) + this change.

## Tasks
- [x] Cherry-pick plan 111 onto fork.58, resolve conflicts
- [x] Plug icon / `IP n` / `∞` display changes (`monitor-view.tsx` only)
- [x] tsc, eslint, vite build; go build, go vet, landing tests; full `go test ./...` = 10 known failing tests (backup, federation, legacy migrations, renewal, service monitor, tunnel port in use), none landing-related
- [x] Local demo panel (lmdemo) screenshots desktop + H5, 0 page errors
- [ ] Push branch, tag `3.0.27-fork.59`, CI + images green, release verified
- [ ] Back up and upgrade the production panel (panel only, no agent OTA)
