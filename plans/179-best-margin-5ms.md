# 179 — 「最优」 switch margin 15 ms → 5 ms

User 2026-10-31 (TW night): 15 ms is too large; switch the 「最优」 first place when a challenger is ≥ **5 ms** faster
(still 2 consecutive 60 s rounds; failover on an unreachable first stays immediate). "试一试" — watch for flapping.

Scope: panel only (`bestPathSwitchMarginMS` in `go-backend/internal/http/handler/tunnel_best_path.go`), admin detail hint
text in `vite-frontend/src/pages/tunnel/best-path-chips.tsx`. No agent change, no DB change. Ships with the next batch
release (cherry-pick onto the release branch).

Resource note: a switch = one `UpdateChains` push to the owner node only; the existing storm guards stay
(`bestPathMaxPushRound` 40 / `bestPathMaxPushNode` 10 per round). A lower margin can only add pushes when two candidates
sit 5–15 ms apart for 2 rounds in a row; check `[best-path] switch` log lines per hour after release.

## Tasks
- [x] Margin constant 15 → 5 ms
- [x] Tests: `TestBestPathMarginIs5ms` (4.9 no / 5 yes / 40 yes); spike fixture in `TestBestPathNeedsTwoConsecutiveRounds` → gain 3 ms
- [x] UI hint 「更快的不到 5 ms 或还没连续 2 分钟，先不换」
- [x] `go test ./internal/http/handler/` green
- [ ] Ship in next batch release
- [ ] After release: count `[best-path] switch` lines / h for a day; revert to a higher margin if tunnels flap
