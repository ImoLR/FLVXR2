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
- [x] `go test ./...` go-backend (+ PG), go-gost/x, go-gost build; `npm run build`; eslint 0 new errors
- [x] local lab: pool drains after idle (short timer) and wakes on the next stream; resource numbers
- [x] prod read-only estimate: rules / keys with no streams in 30 min → idle conns saved
- [x] gallery :8791 /warm-idle-rule-tag/ (rules page user + admin, tunnel page 数据不足, desktop + mobile,
      light + dark, close-up 出口最优 + 预热)
- [x] ledger row 249, push branch, CI green, summary

## Results (2026-11-12, build only)

### Tests
- go-gost/x `go test ./...` exit 0 (warmpool incl. 6 new sleep tests, also `-race`; chain, handler/forward, socket);
  go-gost root build + vet exit 0; `go list` confirms `idle_lab.go` only with `-tags warmlab`.
- go-backend `go test ./...` with `FLVX_POSTGRES_TEST_DSN` (own throwaway postgres:18-alpine container, PG variants
  ran): every package ok. The first full run hit Go's default 10 min per-package timeout in `internal/store/repo`
  (PG on disk, 498 s alone; the test named in the panic was 6 s in, not hung) — re-run with `-timeout 45m`: ok.
  The very first run's PG failures were my test container's 512 MB tmpfs filling up (connection refused), not code.
- New panel tests: `TestWarmPoolViewSleepAndWarming`, `TestSummarizeWarmPathsSkipsSleepingPaths`,
  `TestForwardWarmPoolRuleView` (SQLite + PG: entry-bound vs unbound, port rules, admin vs user, no leaks, sleeping
  entry, paused / nft / switch off), `TestForwardListCarriesWarmTag`, contract `TestForwardWarmTagContract` (router +
  real agent WebSocket report → admin 数据不足 / user nothing → green both, user without names / targets / counts →
  sleeping → nothing; tunnel list 数据不足). Old test `TestWarmPoolView` "9 warm streams → nothing" now expects
  `warming` (the intended change).
- Frontend: `npm run build` ok (tsc + vite); eslint: new files 0 errors / 0 warnings; forward.tsx 16 errors = base
  d2da306b (same pre-existing a11y errors), warnings 644 → 646 (`import/order` from the two new imports).

### Local lab (netns, throwaway: panel + E → M → X + target; RTT E↔M 50 ms, M↔X 90 ms; `warmlab` agents with
`FLVX_WARM_IDLE_SLEEP=60s`; scripts `/root/flvx-workers/runs/warm-idle-rule-tag/lab/` (`idle.py`), log `logs/idle.jsonl`)
| step | result |
|---|---|
| warm | E→M 2, M→X 2, E→T:9001 2 pooled conns; 15 + 15 streams, 0 errors, tunnel p50 302 ms |
| tags while active | tunnel 「预热 −287 ms」, rule lab-rule −287 (2 hops), port rule −0.8 (no RTT on that leg) |
| idle | all pools 0 within 65 s of the last stream (60 s timer + ≤ 5 s tick); E and M both (middle sleeps too) |
| asleep 90 s | pooled conns sampled every 10 s: always 0; SYNs E 3 / M 3 = the existing 60 s path-latency probe (connect + RST), no pool refill; panel: no tag on the tunnel or the rules |
| wake | next stream cold 581 ms (= no pool), pools refilled 0.05 s later on E and M, the stream after warm 300 ms; port rule 11.9 → 10.2 ms, refilled 0.03 s; tags back at the next report; asleep again 60 s later |
| resources (E / M) | awake: fds 22 / 17, goroutines 59 / 43; asleep: fds 17 / 12, goroutines 53 / 37 (−1 fd and −1 watcher goroutine per pooled conn); RSS unchanged by sleep (45.7 → 52 MB came from the 30 streams' heap) |
| normal-user view | not checkable in the lab (JWT role must match the DB: role-1 token for the admin = 401); covered by the tests and the gallery (real user) |

### Prod estimate (read-only: two snapshots of `forward.in_flow + out_flow` from the prod SQLite, `mode=ro`,
14:50 → 15:20 UTC = 22:50 → 23:20 TW, evening; scripts `runs/warm-idle-rule-tag/work/prod/`)
33 of 43 active warm-eligible rules carried no traffic in the 30 min → 31 of 43 pool keys would sleep (a 隧道转发
entry key sleeps when every rule on that entry is idle, a middle key when the whole tunnel is idle): **pooled
conns 86 → 24 (−62)**, refill dials ≈ −37 / min fleet-wide (−1.2 / min per key, age 90–110 s). Per host: Mkcloud
深港ixp 1 16 → 0, Mkcloud 沪日ixp 440 16 → 4, Leikwanhost 沪台IPLC 12 → 2, Mkcloud 沪港ixp 新春版 8 → 4, AWS HK 8 → 0,
vmsilo 沪日首发 8 → 8, Datawave HK 2 6 → 2, Bestvm HK 年付 4 → 2, Bestvm JP B / Dmit Nathan / vmsilo 沪美 2 → 0,
Dmit Echo 2 → 2. Lower bound: a rule with a long stream still carrying bytes counts as active.

### Release notes (rides with 247 + 248 as fork.130, after the user approves the screens)
- No DB change. Panel + agents (OTA entries and middles; exits unchanged by 249).
- Mixed versions: old agent (fork.129) under the new panel = never sleeps, no `sleep` field → awake → shows
  数据不足 / numbers as reported; new agent under an old panel = sleeps, the old panel ignores `sleep` (its tag then
  just follows the hour window as before).
- Rollback: panel image swap (no schema change); agents back by OTA if needed (no state is persisted).

### Gallery
http://104.145.236.29:8791/warm-idle-rule-tag/ — 28 screens (desktop 1366×900 + mobile 390×844, light + dark): rules page
as a normal user (demo_user, 6 own rules: only 3 green tags) and as admin (all 11 rules: green ×4, red, 不支持 ×2,
数据不足 ×2; list / grouped tree / cards / H5), close-ups of 「出口最优」 + 「预热」, the popovers (user one sentence, admin
数据不足 counts per hop, admin two-hop green), tunnel page with 「预热中 · 数据不足」 and a sleeping tunnel without tag.
Synthetic seeded panel (names as on prod, documentation addresses) in a loopback-only netns with simulated agents
that send real warm reports over the agent WebSocket — the tags are what the panel itself returns per viewer; only
the 最优 data (60 s probe round) is added in the browser. 94/94 automated checks (states per viewer, line placement
under the 最优 line, rules-page size, ms tag computed style == the 最优 ms tag, user API without hops / names /
targets / counts), 0 page errors. Scripts (not committed): `runs/warm-idle-rule-tag/demo/`.

### Ledger / CI
Ledger row 249 added (waits for screen approval; rides with 247 + 248 as fork.130). Branch pushed; CI Build Check
43246709140 green on 2f5e1519 (the code head; later commits are docs only).
