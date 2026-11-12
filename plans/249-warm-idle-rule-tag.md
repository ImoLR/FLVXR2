# 249 — 预热池 idle sleep + 「预热中 · 数据不足」 + 预热 tag on the rules page

User 2026-11-12 ~21:40 TW, after fork.129 (plan 244/246) was live: of 52 prod tunnels 30 have no rules
(no pool, nothing shown — correct), 1 is a LeiKwan pipe (never warms), ~11 have active rules but
< 10 warm streams / h, so they show nothing either — looks the same as "no rules", confusing.

Base: `feature/3.0.27-warm-dial-quiet` d2da306b (= fork.129 + plan 247). Branch
`feature/3.0.27-warm-idle-rule-tag`, worktree `/root/projects/flvxx/flvxx-warm-idle`. **Build only** — no tag,
no release, no prod change; rides with 247 + 248 in one release (fork.130) after the user approves the screens.

## Decisions (user)
1. **Idle sleep (agent)**: a pool key (chain × next hop / 端口转发 rule × target) with no new stream for
   30 min drains to 0 and stops refilling; the next stream on it is cold and wakes it (refill at once).
   Local agent state only (SetWarmPools stays the "allowed" set), reported as one field in the per-minute
   warm report. Middles sleep by the same rule.
2. **Tunnel page tag**: no rules / all keys sleeping → nothing; active (streams in the last hour) but < 10
   warm streams → light-grey 「预热中 · 数据不足」 (popover with the counts); enough data → green / red /
   grey 不支持 (unchanged).
3. **Rules page tag** (add-only, under the 最优 line, or where it would be): users see only green
   `预热 −N ms`; admins also red / grey 不支持 / 数据不足. 隧道转发 rule = the path it really uses (its
   entries: bound entry or all), 端口转发 rule = its own pool. User response carries no node internals.
4. Everything else from 244 / 247 unchanged.

## Build decisions (worker)
- **Agent** (`go-gost/x/warmpool`): per key `sleeping`; checked on the existing 5 s tick (`sleepCheckLocked`,
  no timer / goroutine per key): `now − lastUse ≥ 30 min` → idle conns closed, `refillLocked` skips it, a refill
  dial in flight is closed on arrival. A stream (`Take` / `NoteMiss` → `noteUseLocked`) wakes the key and starts
  the refill at once; that stream itself found nothing and dialed cold (same as no pool). The owner keeps
  `lastStream`: a pool created later (最优 switch, new head) starts from it, so an idle owner's new head is born
  asleep instead of warming for 30 min. A fresh owner (SetWarmPools adds it, rule resumed, service re-created,
  agent restart) counts as "a stream now" → awake for its first 30 min. `SetConfig` with the same / resized
  state and `OwnerChanged` do not wake anything. Report: `sleep: true` on the key (omitted when awake, so an old
  panel ignores it and an old agent = awake). Sleep/wake are debug log lines only. Lab-only override: build tag
  `warmlab` + `FLVX_WARM_IDLE_SLEEP` (file `idle_lab.go`, not in release builds).
- **Panel**: a path counts only while its first hop is awake (every stream of the path passes the first hop, so
  first hop asleep ⇔ no stream on that path for 30 min); a sleeping middle alone does not hide the path.
  Priority, per the user's list: every path asleep → nothing (even with enough data in the hour — the pool is
  not working now; it comes back with the first stream); enough data → green / red (unchanged); all awake hops
  不支持 → grey (unchanged); streams in the hour but < 10 warm → `warming` 「预热中 · 数据不足」 with `streams`,
  `warmN`, `minWarm`; awake but no stream yet (fresh pool) → nothing (it would only flicker after a release).
- **Rules page**: `warmPoolState` per rule on `/forward/list`, from the same window (no new request, no probe;
  one `ListWarmPoolTunnels` + one chain query per warm tunnel per list call, nothing while no pool reports).
  隧道转发 rule = its forward-port entries (an entry-bound rule has ports only on its bound entry, plan 152; else
  every entry = the tunnel's weighted value); 端口转发 rule = its own keys. Admin = full view; user = only
  `status: faster` with `savedMs ≥ 1`, `kind`, `windowMin`, empty `hops` (the tunnel window also holds other
  users' streams, so no counts / names / targets). Frontend: `RuleWarmPoolLine` right after `RuleBestPickLine`
  in the 3 renderers (list row, grouped/tree row, card incl. H5) = own line under the 最优 line, or in its place;
  the pill uses the rules-page 最优 size (`size="pick"`, ms tag size `pick` = the rules-page 最优 ms tag).
  「预热中 · 数据不足」 = lighter slate than the 不支持 grey, no ms tag; the user popover is one sentence.

## Checklist
- [x] agent: per-key idle sleep (30 min, piggyback on the 5 s tick, no goroutine per key), wake on stream,
      no refill while asleep, in-flight dial dropped, new pools of an idle owner start asleep, `sleep` field
- [x] agent: test-only idle timer override (build tag `warmlab`, not in release builds)
- [x] agent tests: sleep after 30 min (fake clock), wake on stream, no refill while sleeping, reconcile
      interplay (SetConfig same / re-add, OwnerChanged), report field
- [x] panel: ingest `sleep` (old agent: absent → awake), view states incl. `warming` (数据不足) and sleeping
- [x] panel: rule-level view on the forward list (admin full / user green-only minimal), entry-bound rules
- [x] panel tests: view states, old-agent compat, rule view user vs admin, entry-bound rule, contract test
- [x] frontend: 「预热中 · 数据不足」 pill + popover; rules-page tag (tree rows, list rows, cards, H5)
- [ ] `go test ./...` go-backend (+ PG), go-gost/x, go-gost build; `npm run build`; eslint 0 new errors
- [ ] local lab: pool drains after idle (short timer) and wakes on the next stream; resource numbers
- [ ] prod read-only estimate: rules / keys with no streams in 30 min → idle conns saved
- [ ] gallery :8791 /warm-idle-rule-tag/ (rules page user + admin, tunnel page 数据不足, desktop + mobile,
      light + dark, close-up 出口最优 + 预热)
- [ ] ledger row 249, push branch, CI green, summary
