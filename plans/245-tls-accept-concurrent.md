# 245 — Tunnel TLS listener: concurrent handshakes (no head-of-line accept)

Found during plan 244 E2E (2026-11-12). Build only, rides the plan 244 release (same agent OTA); one
commit, can be reverted on its own.

## Problem
`go-gost/x/listener/tls` (`silentTLSListener`, the listener of every 隧道转发 relay service on middles
and exits) runs the server TLS handshake **inside `Accept()`, one connection at a time**, with a 10 s
deadline. So:
- every new tunnel connection to a hop waits for all handshakes queued before it (≥ 1 RTT each, the
  client's Finished): in the plan 244 lab (E→M 50 ms, M→X 90 ms RTT) 10 concurrent streams took
  908 ms first byte instead of 578 ms with the pool off, and the pool could not help (its refills queue
  too);
- one TCP connection that never sends a ClientHello (port scanner, half-open probe) blocks the whole
  relay port for up to 10 s — every user stream of that tunnel through that hop stalls meanwhile.

## Fix
Accept raw conns in a background loop, handshake each in its own goroutine (same 10 s deadline, same
silent close on failure), hand finished conns to `Accept()` in completion order.
- Bounded: at most 64 handshakes in flight per listener; at the cap the loop stops accepting (kernel
  backlog, i.e. today's behaviour, but only after 64 slow clients instead of 1). Finished conns wait in
  an unbuffered hand-off, so total held conns per listener stay ≤ 64.
- Accept errors keep their meaning for the service loop (temporary → it backs off and calls again;
  fatal / closed → returned to every later `Accept()`), `Close()` releases everything waiting.
- Nothing else changes: conn / traffic limiters, admission, stats and proxy-protocol wrappers sit
  under it exactly as before; the handler still gets a handshaked `*tls.Conn`.

## Checklist
- [x] concurrent handshake in `silentTLSListener` (cap 64, close / error semantics)
- [x] unit tests: silent client does not block a real client; garbage conn closed silently; Close
      unblocks Accept + pending handshakes; fatal accept error returned; cap holds
- [x] lab: 10-concurrent first byte, pool off / on, before → after; a silent conn on the relay port
- [x] go test ./... (go-gost/x), summary in plan 244 final report

## Lab result (plan 244 netns lab, E→M 50 ms / M→X 90 ms RTT, 2-hop tunnel, first byte ms)
| case | before (serial accept) | after (plan 245) |
|---|---|---|
| 10 concurrent streams, pool off: p50 / mean | 908 / 913 | 578 / 578 |
| 10 concurrent streams, pool on: p50 / mean | 999 / 891 | 477 / 480 |
| 4 concurrent, pool on: mean | 413 | 388 |
| 1 silent TCP conn on the middle's relay port, 3 streams, pool off | 1 of 3 failed (10 s stall), others 589 | 0 errors, 579 |
| same, pool on | max 8939 (refill/miss stuck behind it) | 0 errors, max 299 |
Unit tests: `go test -race ./listener/tls/` (6 tests) pass.
