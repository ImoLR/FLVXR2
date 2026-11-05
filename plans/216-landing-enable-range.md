# 216 — 转换落地 auto-picks a free port block + readable error toasts (BUILD ONLY)

Branch `feature/3.0.27-landing-enable-range` from tag `3.0.27-fork.116`.
Worktree `/root/projects/flvxx/flvxx-landing-enable-range`. Panel only, no DB change, no OTA.

## Background
2026-11-05 ~18:15 TW the admin tried to turn Breadcloud US into a landing. Enable always used
30000-39999; a tunnel hop of 「mk沪港ixp 666m -> US」 on port 34060 rejected the whole enable,
and the red toast vanished after 2 s.

## Checklist
- [x] Repo: one-pass port-holder load per node (forward / tunnel hop / inbound / runtime resource) with names
- [x] Repo: `EnableLandingServer` keeps 30000-39999 when free, else picks the first free aligned 1000-port block
      (30000… then 10000…), inside node.port, clear of guard / chain-node ports; chain-only → public path too
- [x] Repo: no-block error and range-edit conflict error name the holders (max 3 + 等)
- [x] Handler: enable response carries `rangeNote`; frontend toast shows the picked range
- [x] Tests: default free → 30000-39999; hop on 34060 → 30000-30999 carved; all busy → error names the tunnel
- [x] Frontend: one `toast.error` rule — max(6 s, 0.12 s/char), cap 15 s; success/info 2 s; click / ✕ to dismiss
- [x] Gates: go test ./... vs fork.116; tsc / build / lint vs base
- [x] Screenshots H5 390 + desktop → /root/mmwx-preview/landing-enable-range/
- [x] Enable latency before/after on seeded demo
- [x] Push, CI green (run 42012031878 on 794f728c), ledger row, final.md — BUILD ONLY, not released

## Results
- `go test ./...` (go-backend): all packages ok (incl. handler + contract); new repo tests also pass on PostgreSQL 18.
- Frontend: `tsc --noEmit` 0 errors; `npm run build` ok; eslint on changed files: no new warnings (3 pre-existing in enable-button.tsx).
- Demo (synthetic, loopback netns): enable latency fork.116 2.9–5.1 ms vs branch 3.2–4.9 ms (default range);
  busy 34060 → branch picks 30000-30999 in 3.2–7.4 ms (fork.116 refused in 1.4 ms).
- Screens: http://104.145.236.29:8791/landing-enable-range/
