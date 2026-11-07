# 234 — 「自动切换门槛」设置（最优线路 / Reality 伪装域名 / Reality 目标 IP）

Base: `feature/3.0.27-reality-pick-margin` @ 74e78271 (plan 233, unreleased; 233 ships inside 234).
Branch `feature/3.0.27-switch-margin-settings`. **Build-only** (screens first; release = panel + native landing agent OTA).

## Why
User 2026-11-07 ~15:30 TW: domain switch margin 5 ms and IP pin margin 20 ms are hard-coded; wants one place to set
them (one number each), compare them, and see whether a value flaps.

## Today (hard-coded)
| what | where | value |
|---|---|---|
| 「最优」 exit/中转 switch (+ 「规则最优」 per destination) | panel `bestPathSwitchMarginMS` (tunnel_best_path.go, tunnel_rule_best.go), 2 rounds | 5 ms |
| Reality 伪装域名 auto pick | panel `RealityPickSwitchMarginMs` (plan 233) | 5 ms |
| Reality 目标 IP pin, native | agent `realityPinHysteresis` (go-gost/x/landing/reality_pin.go), re-check 6 h | 20 ms |
| Reality 目标 IP pin, shared (mmwx Helper) | mmwx-custom `cmd/mmwxc-helper/flvx_reality_pin.go` `flvxPinHysteresis = 20.0` | 20 ms |

## Design
- **Card**: 网站配置 (`/config`, admin only) — the page that already holds the global cards (基本设置, 快捷开关,
  探针, 公告, Telegram, 备份, DNS). New card 「自动切换门槛」 right after 快捷开关. Three rows, each: ms input,
  one-line explanation, grey 「推荐 X ms」 + 「恢复推荐值」, 「最近 7 天自动切换 N 次」 (+ 故障切换 M 次 when known)
  and a hint 「切换较频繁，可调大」 naming the busiest tunnel/server when one of them switched ≥ 7 times in 7 days
  (≥ 1/day) because it was faster (failovers excluded). One save button.
  1. 最优线路：新出口/中转快 ≥ X ms（连续 2 轮）才换 — default 5 (rounds fixed at 2). Includes 「规则最优」.
  2. Reality 伪装域名：新域名快 ≥ X ms 才换 — default 5.
  3. Reality 目标 IP：同一域名下新 IP 快 ≥ X ms 才换 — default 20.
  Range 0–200 integer ms; 0 = any gain switches (ties keep current). Failover (current dead / blocked / gone) always
  switches at once, whatever the number.
- **Storage**: existing key-value table `vite_config` (no new table): `switch_margin_best_ms`,
  `switch_margin_reality_domain_ms`, `switch_margin_reality_ip_ms` (missing = default → behaviour unchanged), and
  `auto_switch_counts` (JSON, per UTC day per kind: total, failovers, per tunnel/server counts; only the last 8 days are
  kept → bounded by days × units).
- **Reads**: repo caches the three margins in memory (atomic, 5 min TTL as a safety net for backup import; saving
  through the card or the generic config endpoint invalidates at once) → no DB hit per round / probe.
- **Counts** (no new probes / timers): 最优 + 规则最优 = each applied switch (faster / failover; first-rule activation
  not counted), flushed once per 60 s path round; 伪装域名 = an auto pick that changed the server's dest (forced when the
  current one was not pickable); 目标 IP = a stored pin whose pinned IP changed between two reports (native agent and
  Helper status) — no agent support needed; agents ≥ this build add `move_reason` (faster/failover).
- **Propagation**:
  - 1 + 2: panel reads the cached setting on every round / 测速 → live, no restart.
  - 3 native: `reality_pin_margin_ms` in the landing desired, only to agents reporting `landing_reality >= 3` and only
    when ≠ 20 (old agents / default → payload byte for byte as before; the value joins the revision signature).
    The existing 1-minute native reconcile push carries it; the agent uses it from the next pin check (6 h recheck
    or a failed 10 min liveness check; the change itself never forces a re-check or a move).
  - 3 shared: Custom rejects unknown desired fields (`DisallowUnknownFields`), so FLVX sends
    `reality_pin_margin_ms` only to shares whose `reality_pin` capability lists version 2 (none today) and only when
    ≠ 20; included in the shared desired hash. Helper follow-up (mmwx worker) written in the summary.

## Tasks
- [x] repo: margins (parse/get/set/cache) + bounded switch counter + summary
- [x] panel: 最优 / 规则最优 margin from settings (0 = any gain; ties keep) + counts
- [x] panel: Reality 伪装域名 margin from settings (DecideRealityPick margin arg) + counts (forced vs faster)
- [x] panel: IP pin move counts from stored pins (native + shared) + move_reason
- [x] panel: desired `reality_pin_margin_ms` (native ≥ landing_reality 3, shared reality_pin v2); no explicit re-sync needed
      (native: 1-minute reconcile push; shared: hash → new revision → next shared push)
- [x] panel: API `/config/switch-margins` + `/save` (admin) + generic config endpoint validation/invalidation, counts hidden from config/list
- [x] agent: `reality_pin_margin_ms` in Desired (validate 0–200, absent = 20), choosePin uses it, `move_reason`,
      handshake `landing_reality` 3 (2 was taken by plan 229)
- [x] frontend: card on 网站配置 + 最优 detail text uses the setting
- [x] unit tests: margins read from settings, defaults 5/5/20, 0 = fastest, failover ignores margin, agent pushed margin
      / fallback 20, counter bounded
- [x] contract tests SQLite + PostgreSQL
- [x] go test ./... (backend, go-gost/x landing) + tsc / build / lint
- [x] screens phone 390 + desktop 1440 light + dark card → /root/mmwx-preview/switch-margin-settings/
- [ ] push, CI green, ledger 234 (+ 233 included-in-234)

## Results (2026-11-07)
- Backend `go test ./...` with `FLVX_POSTGRES_TEST_DSN` (temporary postgres:18-alpine container): 26 packages ok, 0 failures;
  new tests: handler `switch_margins_test.go` (7), repo `repository_switch_margins_test.go` (5) + `DecideRealityPick`
  margin cases, contract `TestSwitchMarginsContract` (SQLite + PostgreSQL), extended `landing_reality_contract_test.go`.
- Agent `go test ./landing/ ./socket/` ok (needs a short TMPDIR: unix socket paths ≤ 108 bytes); `go build ./...` ok.
- Frontend: `tsc --noEmit`, `vite build` ok; eslint on changed files clean except 2 prettier warnings in config.tsx that
  exist on the base too (lines not touched).
- Preview (sanitized read-only prod DB copy, deleted afterwards): API view, save, invalid input, dirty state, tunnel list
  `bestPathState.marginMs` follows the setting. Screens: `/root/mmwx-preview/switch-margin-settings/`.
- Resource use: no new probes / sockets / timers; margins read from an in-memory cache (DB at most every 5 min or after
  a save); counts = one vite_config row (≤ 8 days × 3 kinds × units that switched), written at most once per 60 s round
  when something switched, plus once per Reality pick / pin move.
