# 237 — 「自动切换门槛」保存后约 1 分钟内生效（和 mmwx Helper 统一）

Base: `maintenance/3.0.27-fork.125-paused-restart` @ bfb776f4 (= prod fork.125 e3d4bfc7 + plan docs).
Branch `feature/3.0.27-margin-apply-now`. **Build-only** (release worker ships it with the fleet OTA, fork.126).

## Why
User 2026-11-08 00:50 TW: 「自动切换门槛那个还是统一一下吧」. mmwx Helper v0.7.24 applies a changed 目标 IP margin in
~1 min; FLVX only used a changed margin at its next measurement. Wanted: after 保存, every FLVX side (panel + native
landing agents) applies the new margins within ~1 minute. (Helper 12 h DNS-gone grace vs FLVX immediate switch is a
separate, agreed difference — unchanged.)

## Trace (before this plan)
| value | who decides | when the new number reached the decision | when it could change anything |
|---|---|---|---|
| 最优线路 5 ms × 2 轮 (+ 规则最优) | panel `bestPathManager` / `ruleBestManager`, every 60 s path round reads `repo.SwitchMargins()` (in-memory, invalidated by the card save and the generic config endpoint) | next round, ≤ 60 s | next round; a switch still needs its 2 confirming rounds (the rule, unchanged) → **already immediate, left as is** |
| Reality 伪装域名 5 ms | panel `DecideRealityPick` inside `RecordLandingRealityProbe`, i.e. only when a 测速 finishes (auto: one server per 10 min tick, each server every **24 h**; or a manual 测速) | at once (cache) | **only at the next 测速 → up to 24 h** (the server view's 「差 N ms 未换」 hint already used the new number) |
| Reality 目标 IP 20 ms, native | agent `realityPinner` (`choosePin`) | panel 1-minute native reconcile push (`;pm=` in the revision signature → new revision only when the value changes) → agent `setMargin` ≤ 1 min | **only at the next full pin check: 6 h re-check, or a failed 10 min liveness connect → up to 6 h** |
| Reality 目标 IP, shared (mmwx Helper) | Helper | shared desired hash → push from the 5 s shared loop | Helper applies in ~1 min (v0.7.24) → already fine, nothing to do |

## Design
- **Agent (目标 IP)**: the pinner keeps the samples of each pin's last full check (per host: ≤ the resolved public IPv4s,
  in memory only). When `setMargin` sees the margin in force change (nil and 20 are the same → no-op), it re-runs
  `choosePin` ONCE per pinned host on those held samples with the new number — same hysteresis rules, no DNS, no
  connect, no new goroutine/timer. A move keeps the pin's `checked_at` (the samples are as old as before; the 6 h
  schedule is not shifted) and gets `move_reason: faster`; `Apply` then runs `reconcile` at once (as today), so the
  guard inbound dials the new IP in the same push and the apply reply already carries the moved pin (panel counts it).
  Hosts in a running check are skipped (that check reads the new margin itself). Raising the margin never moves.
- **Panel (伪装域名)**: when a save changes the 伪装域名 margin (card save or generic config key), re-run
  `DecideRealityPick` ONCE per active 自动 server on its stored 测速 table, apply only a margin-driven switch
  (current still pickable, challenger now beats it by ≥ the new margin; failovers stay with the next 测速), same
  update as a 测速 (`reality_dest` + `reality_server_names` = default for NEW nodes; no revision bump, no push), count
  it in 「最近 7 天自动切换」, log it. Servers in a running 测速 are skipped (that 测速 reads the new margin). No probe.
- **Same value**: no DB write besides the setting, no revision bump, no re-evaluation (panel: `prev != next` per key;
  agent: margin in force unchanged).
- **UI**: card text 「保存后马上生效」 → 「保存后约 1 分钟内生效」 (header + toast), nothing else.
- **Compatibility**: payload unchanged → fork.124/125 agents keep using the number from their next check (harmless);
  new agents on an old panel behave the same (re-evaluate when the pushed value changes). No handshake bump needed.

## Tasks
- [x] trace the 3 values end to end (table above)
- [ ] agent: held samples per pin + `setMargin` change hook → one re-evaluation (no probes), log line
- [ ] agent tests: switch when the new margin allows / no switch when not / same value no-op / running skipped /
      no lookup or dial; Apply path moves the guard address in the same apply; mutation check
- [ ] panel: 伪装域名 re-evaluation on save (card + generic config) from the stored 测速 table, counts + log
- [ ] panel tests: lower margin → switch + count, no probe issued; same value → nothing; higher → nothing;
      failover-only change not applied; mutation check
- [ ] frontend: 「保存后约 1 分钟内生效」 (header + toast); tsc / lint / build; desktop + phone screenshot
- [ ] gates vs base: go-gost + go-gost/x tests, backend build + tests (+ contract), compare with base logs
- [ ] mmwx contract doc wording check (`reality-pick-v1.md`) — change only if wording is affected
- [ ] push branch, ledger row (READY for release-1108), summary
