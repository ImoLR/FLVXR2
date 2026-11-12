# 247 — 预热池补拨失败不再记 error 日志（端口转发目标已死时）

Found 11-12 18:28 TW during the fork.129 canary (plan 246) on Mkcloud 沪日ixp 440. Build only; rides the next
release (agent-only change, no panel / DB change).

## Problem
端口转发 rules keep a warm pool per target (plan 244). The pool's refill dial goes through the rule's router,
and `chain.Router` logs every failed attempt at error level (`route(retry=0) dial tcp …: connection refused`,
`sid` empty = no user stream). Rules 77 学长软银WG (8866.sytes.net:603) and 95 火山2-展开0082-第三个
(45.192.241.60:30888) point at dead targets, so 440 wrote 3–5 error lines per pool round (rest 60 s → 5 min cap)
≈ 35 lines/h per dead target, forever; before fork.129 those rules only logged when a user connected.

## Fix
- `ctx.ContextWithQuietDial` / `QuietDialFromContext`: marks a dial without a user stream.
- `chain.Router.Dial`: on a quiet ctx, warnings/errors of the dial become debug lines (`quietLogger`, also passed
  into the route's dialers via `LoggerDialOption`).
- `handler/forward/local` `WarmDial` (pool refill of 端口转发) dials with the quiet ctx. User streams unchanged.
- The pool itself still counts failures, rests 60 s → 5 min and logs one info line per failing streak.
- Tunnel (chain) pool dials do not go through the router and already log nothing per attempt.

## Tasks
- [x] quiet dial ctx + router quietLogger + WarmDial uses it
- [x] test `TestRouterQuietDialLogsFailureAtDebug` (quiet: 0 error lines, debug kept; normal: 1 error line)
- [x] go-gost/x chain, handler/forward, warmpool, ctx tests pass; go-gost build
- [ ] release (next agent release) — ledger row added
