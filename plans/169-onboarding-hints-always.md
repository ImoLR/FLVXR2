# 169 — 落地 hint ring: no trigger, faster laps

User (2026-10-30): the plan-146 gold ring on 「用于新规则」 / 「目标类型」 / 「复制链接」 only played after a
node / rule create, so existing users never saw it. Change: every feature the user has not clicked yet
rings, and the comet runs faster.

Decisions (user):
- No trigger: a not-yet-clicked spot rings on every page visit, 15 s each time, until clicked once.
- Same three spots only.
- Faster: 1.2 s lap + 0.8 s rest (was 2.4 s + 2.6 s).

Design:
- `utils/onboarding-hints.ts`: state is `done` only (same `flvx.hint.v1.<uid>` key, old 146 entries keep
  working — `done: true` stays quiet, played-but-not-done plays again). Arm/played helpers removed.
- `components/onboarding-ring.tsx`: one element per hint plays per visit (module-level claim, released on
  unmount), so the many rule rows ring only one 「复制链接」 and the rest are neither observed nor re-rendered.
- Frontend only, no API / DB change.

## Tasks
- [x] Drop trigger arming (node create, rule create) and `match`
- [x] Claim-per-visit hook + done-only state
- [x] Lap 1.2 s, rest 0.8 s
- [x] tsc + build; lint: no new errors
- [x] Headless check on demo panel (runs/onboarding-hints-always): old user rings without trigger, replays
      next visit not same visit, click → done, one 复制链接 ring among many rows, legacy 146 state OK
- [ ] Ship in next batch release (panel only)
