# 233 — Reality 自动挑选加 5 ms 换选门槛（和「最优」统一）

Base: tag `3.0.27-fork.123`. Branch `feature/3.0.27-reality-pick-margin`. **Build-only** (next batch release, panel only).

## Why
fork.123 check (plan 229): on Bestvm US the auto-picked 伪装域名 flips between several big sites at 6–7 ms that
differ by 1 ms. User 2026-11-07: 「加一个 5 ms 统一一下」 — same margin as 「最优」 (`bestPathSwitchMarginMS = 5`).

## Rule
Per landing server in 自动（最快） mode (native agent probe and mmwx shared Helper probe — both go through
`RecordLandingRealityProbe`), after a 测速 the current server default (`landing_server.reality_dest`) stays unless a
challenger's ranking value is **≥ 5 ms lower** (`RealityPickSwitchMarginMs = 5`).

- Ranking value = the plan-229 metric: re-sampled rows → worst of the samples; single-sample rows → that sample.
  The best challenger is still chosen with the plan-229 order (re-sampled first, worst, tie → median).
- Ties / gap < 5 ms → keep current (server names of the kept domain still refresh as before).
- Switch **at once** (no margin) when the current pick is not pickable any more: missing from this 测速's candidates,
  handshake failed / not usable, blocked (admin or auto-block rules — both end in `Blocked`), or a 试用 site that is
  not 「稳定」 any more.
- The current row may have only its pass-1 sample (it was not among the 5 re-sampled): its single sample is compared
  with the challenger's worst. Adding it to the re-sample set would cost extra handshakes; not done (no extra probes).
- A row marked 「已取消」 for the current pick (probe cut short, nothing measured) leaves dest and serverNames unchanged: nothing measured.
- No 2-round confirmation (unlike 最优): the probe runs once a day per server, a second round would delay every real
  switch by a day; the 3-sample worst-case ranking already filters single lucky samples.
- Manual mode untouched (never auto-picks). Existing landing nodes keep dest / serverNames / links (only new nodes use
  the server default, as before). No agent change, no DB schema change, no extra probes/sockets/timers.

## UI
落地 Reality 测速 table: on the 当前 row, when a faster pickable domain is within the margin, a grey note
「保持（差 < 5 ms）」 (title: which domain, how many ms faster). Computed in the server view from the stored table.

## Tasks
- [x] repo: `RealityPickSwitchMarginMs`, `DecideRealityPick` (hold / switch / immediate), used by `RecordLandingRealityProbe`
- [x] handler view: `pickHeld*` fields on the current row + `switchMarginMs` in the rule view
- [x] frontend: grey note on the current row, help text mentions the margin
- [x] unit tests (keep within margin, switch at ≥ 5, immediate on infeasible / blocked / dropped / 试用 lost, cancelled, manual)
- [x] contract tests SQLite + PostgreSQL (hold across probes, existing nodes unchanged)
- [x] go test ./... + contract; tsc / build / lint
- [x] replay Bestvm US stored samples (prod DB read-only copy)
- [x] screens phone 390 + desktop 1440 light → /root/mmwx-preview/reality-pick-margin/
- [ ] commit, push, CI green
- [ ] RELEASE-LEDGER row (待发布, ready, panel only)

## Replay — Bestvm US (prod DB read-only copy taken 11-07 ~14:40 TW)
Stored history = 2 probes since fork.123 (09:03, 09:09 TW). Previous dest before 09:03 = ftp.debian.org:443 (fork.121).
| Probe | Old rule (fork.123) | New rule |
|---|---|---|
| 09:03 | ftp.debian.org → www.lovelive-anime.jp (worst 6) | same switch: ftp.debian.org is a 试用 site, not yet 稳定 (only 1–2 samples) → not pickable → immediate |
| 09:09 | www.lovelive-anime.jp → www.samsung.com (worst 7) | **kept** www.lovelive-anime.jp (6 ms single sample vs samsung worst 7, gap 0 < 5) → 保持 |
Old: 2 changes in 6 minutes; new: 1 (the forced one), then stable.

## Resource impact
Pure in-memory comparison over the ≤ 64 stored rows per 测速 / per view render; no probes, sockets, timers, columns.
Panel CPU / memory: no measurable change.

## Test results
- `go test ./...` with PostgreSQL DSN (temp postgres:18 container): all packages ok except
  `TestChainUserPolicyPausesWithoutDeleting` once (line-chain timing flake); same test also fails on the unmodified
  fork.123 base (1 of 2 runs) → pre-existing; handler package re-run on the branch: ok.
- New: `repository_landing_reality_pick_test.go` (margin table, immediate switch, cancelled, daily-flip shape),
  `TestLandingRealityPickSwitchMargin` (native, view `pickHeld*`, block, manual), shared margin check inside
  `TestLandingRealityShared`, contract `TestLandingRealityPickMarginContract` (SQLite + PostgreSQL).
- Frontend: tsc, eslint (changed files), prettier, `npm run build` ok.
- Screens (local preview on a sanitized prod copy, Bestvm US dest set to the replay result):
  `/root/mmwx-preview/reality-pick-margin/` — note 「保持（差 < 5 ms）」, title 「排名第一的 www.samsung.com 7 ms，当前 6 ms，差不到 5 ms 不换」;
  no horizontal overflow (390 / 1440).
