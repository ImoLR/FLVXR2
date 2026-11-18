# 283 — 「屏蔽 TLS」 also blocks QUIC over UDP (hy2 / TUIC / Juicity / HTTP3 / Xray QUIC)

Task `quic-block` (Claude xhigh), branch `feature/3.0.27-quic-block` from `3.0.27-fork.149`.

## Why
专线 tunnels block TLS/SOCKS/HTTP so only feature-less protocols (SS / SS2022, VLESS Encryption) cross them. The
filter only looked at TCP: `detectProtocol` in `go-gost/x/service/service.go` returned false for every UDP conn, so
plain hysteria2 / TUIC / HTTP3 over UDP crossed a 专线 forward untouched. QUIC = TLS 1.3 inside UDP; the Initial
packet header is plaintext and its ClientHello (SNI) can be decrypted by anyone (Initial keys derive from the DCID).

## Decisions
- No new checkbox, no new DB column, no new metadata: the panel already sends `protocolFilter.tls` to the **UDP**
  service of every forward (`buildForwardServiceConfigs`). New agents read it for UDP too; old agents keep ignoring it
  (no block until OTA'd — harmless).
- Detect = QUIC long-header **Initial** with a known version, plain byte compares, no decryption.
- Every datagram of a filtered UDP session is checked (a few ns, 0 allocs) — catches junk-first-then-QUIC.
- On hit: datagram dropped, the session ends (target conn closed), and the client address stays blacklisted on that
  listener while it keeps sending (idle TTL) — no new session / dial per retransmitted Initial.
- Not blocked by design: SS / SS2022 UDP, VLESS Encryption (UDP inside TCP), hy2 + Salamander, mieru, DNS,
  games/voice, WireGuard (no QUIC; not in scope).
- Counter kind `quic` next to http/tls/socks/other; node card 「协议拦截」 shows `QUIC n` only for agents that report it.

## Checklist
- [x] Worktree + branch from fork.149, plan doc
- [x] Agent: QUIC Initial detector (`service/quic_detect.go`) + per-datagram guard on filtered UDP sessions
- [x] Agent: listener-level blacklist of a blocked client address (`internal/net/udp`)
- [x] Agent: `quic` counter + rate-limited `🚫 BLOCK QUIC (hy2/TUIC/HTTP3)` log + report field
- [x] Unit tests: v1/v2 Initial (captured bytes + quic-go live), short header, junk-first, SS2022 UDP, fuzz ≥ 1e8,
      flag off; benchmark ns/op + allocs
- [x] Panel: ws ProtocolBlocks.QUIC (optional), node card QUIC chip, tunnel page note
- [x] go test go-gost/x (all) + go-backend (SQLite + PG) + npm build
- [x] Lab: real binaries matrix (hy2 plain / salamander, TUIC, curl --http3 / xray QUIC, SS2022 TCP+UDP, VLESS Enc UDP,
      plain UDP, TLS-allowed tunnel) on type 1 + type 2; before/after RTT / iperf3 / CPU
- [x] Ledger row 283
- [x] Release next fork (rebase onto newest tag), CI + assets, prod panel upgrade + rollback
- [ ] Night OTA of the entry nodes of tls=1 tunnels (canary first, per-host backup)
- [ ] Prod tests (temp forward → own hy2 / SS2022 server), our protocols unaffected, temp objects deleted
- [ ] 1–3 h QUIC counter report
- [ ] Cleanup (worktree, caches), memory, summary

## Where the check sits (and why it covers every UDP entry path)
Client datagrams of every gost forward enter through one place: the panel builds each forward as a `<id>_udp`
service with listener `udp` + handler `udp` (`buildForwardServiceConfigs`, used for type 1 端口转发, type 2 隧道转发
entries — the chain only changes the handler's dial — multi-entry tunnels, bind-IP fallbacks and landing 添加转发,
which are ordinary forwards). `listener/udp` hands one `*udp.conn` per client address to `service.Serve`, which wraps
it in `wrapProtocolDetection` before the handler reads it. That wrapper now recognises the datagram session
(`quicSession` = `Read` + `Block`) and, when the service's `protocolFilter.tls` (or the legacy global flag) is 1,
returns `quicGuardConn`; every datagram the handler reads passes its `Read`. Other paths: nftables forwards (kernel
DNAT, admin-only since plan 282 — no filter in any mode), WG tunnels (admin-only), 利群 (vendor client) — not gost.
QUIC listener streams (tunnel hops) also have a UDP RemoteAddr but are not `quicSession`s → unchanged.

On a hit: the datagram is not forwarded, `Block()` marks the session and closes it (the handler ends, target conn
closed), the listener keeps the entry and drops that address's datagrams until the idle check removes it (1–2 TTLs:
forward UDP TTL is 10 s when the tunnel's hop protocol is TLS, else the 5 s default → 5–20 s); drops do not refresh
it, so a false hit could cost a flow at most ~20 s, and a QUIC client costs at most one session (one dial) per TTL. One
counter increment per blocked session (lab: 16 blocks = 16 client attempts, retransmits absorbed).

## Detection
`isQUICInitial(b)`: `len ≥ 1200` (RFC 9000 §14.1 client padding), `b0 & 0xC0 == 0xC0` (long header + fixed bit), long
type bits (not header-protected) = Initial for the version: v1 / drafts 27–34 type 0, v2 (0x6b3343cf) type 1;
DCID len ≤ 20, SCID len ≤ 20, token varint fits, Length varint ≥ 20 and fits. gQUIC Q046 (nibble CID byte 0x50) and
Q050/T050/T051 (DCID 8, SCID 0) exact. No decryption, no copy, no allocation.

### False-hit math (random datagram ≥ 1200 bytes: SS / SS2022 / Salamander / mieru / KCP)
- first byte 0xC0–0xCF (long+fixed, Initial type): 1/16
- version ∈ {v1, v2, draft-27..34} (type for v2 is a different 1/16 band, same probability): 10 / 2^32 = 2.3e-9
- DCID ≤ 20 × SCID ≤ 20: (21/256)^2 = 6.7e-3
- token varint fits: 1-byte varint (1/4) always, 2-byte (1/4) ≈ 0.08 → ≈ 0.27
- Length ≥ 20 and fits: 1-byte (1/4) 44/64, 2-byte (1/4) ≈ 0.08 → ≈ 0.19
- IETF total ≈ 1/16 × 2.3e-9 × 6.7e-3 × 0.27 × 0.19 ≈ 5e-14; gQUIC Q046 1/16 × 2^-32 × 1/256 ≈ 5.7e-14,
  Q050/T050/T051 ≈ 2e-16 → **≈ 1.1e-13 per datagram** (smaller ones: 0).
- Measured: 1e9 random datagrams → 0 hits; with the version forced to v1 the structure checks accept 2.49e-5
  (model 2.2e-5) → 2.49e-5 × 10/2^32 = 5.8e-14 + 5.7e-14. 1e8 SS2022-style packets (AES-ECB session/packet-ID header,
  random 24-byte nonce / salt) → 0 hits.
- Scale: 100 Mbit/s of 1300-byte SS UDP ≈ 10k pkt/s → one false hit per ~30 years per such flow.
- Never: DNS (version bytes = ID low / flags / QDCOUNT high), WireGuard (type 1–4 + zero bytes), RTP (0x80/0x90),
  STUN, DTLS (0x14–0x17), OpenVPN, QUIC short header.

### Benchmarks (go1.24, this host)
| check | ns/op | allocs |
|---|---|---|
| random 1200–1500 B datagram (full inline gate) | 0.98 | 0 |
| long-header-looking, wrong version | 2.2 | 0 |
| real Initial (full parse) | 5.3 | 0 |
| session Read 1350 B raw → guarded | 12.6 → 14.2 | 0 |
| session Read 200 B raw → guarded | 5.8 → 8.8 | 0 |
Agent cost per forwarded datagram in the lab is ~30–60 µs (syscalls, queue, goroutine hand-off) → the check is
< 0.01 %. The UDP session also no longer gets the TCP detector's bufio.Reader (4 KB per session) when only HTTP/SOCKS
are blocked — less memory than before.

## Tests
- go-gost/x `go test ./... -count=1`: 27 packages ok, 0 failures (incl. service, internal/net/udp, config/parsing/service
  e2e, socket); `go build ./...` in go-gost ok; go vet on the touched packages ok.
- go-backend `go vet ./internal/ws/` ok; `go test -p 1 ./... -count=1` with `FLVX_POSTGRES_TEST_DSN` (flvx-pg269):
  28/28 ok (handler 478 s, repo 816 s, contract 848 s), 0 failures. Rebased onto fork.150 before the run.
- vite-frontend `npm run build` ok. Lab screen: node card row 「协议拦截 SOCKS 0 · HTTP 0 · TLS 0 · QUIC 1（自 agent
  启动）」 with the QUIC tooltip (runs/quic-block/lab/shots/node-card.png).
- GitHub CI Build Check on the branch: green.

## Lab (runs/quic-block/lab, netns, no internet)
Lab panel + agents built from this branch (E = 专线入口, X = exit), base = fork.149 agent (release binary for the
matrix, same-toolchain local build `lbase` for performance). Tunnels: 专线端口转发 屏蔽TLS (type 1), 专线 E→X 屏蔽TLS
(type 2, tls hop), 线路机端口转发 TLS放行 (type 1). Real binaries: hysteria 2.14.0 (apernet), sing-box 1.14.3 (TUIC v5,
VLESS + QUIC transport, SS2022 client/server), Xray 26.3.27 (VLESS Encryption mlkem768x25519plus), curl 8.14.1
`--http3-only` (OpenSSL QUIC) against the hy2 masquerade, iperf3, a small DNS responder.

| case | 专线 type 1 | 专线 type 2 | TLS 放行 |
|---|---|---|---|
| hysteria2 plain | blocked | blocked | passes |
| curl --http3 | blocked | blocked | passes |
| TUIC v5 (sing-box) | blocked | blocked | passes |
| VLESS + QUIC transport | blocked | blocked | – |
| hysteria2 + Salamander | passes | passes | – |
| SS2022 TCP / UDP DNS / UDP iperf3 20M | passes | passes | – |
| VLESS Encryption TCP / UDP DNS | passes | passes | – |
| plain UDP DNS / iperf3 -u 50M | passes | passes | – |
New agent 27/27 as expected. fork.149 agent: all 8 QUIC cases pass through the 专线 (the bug), everything else
passes (one iperf3 run failed with "server busy" = lab tool; rerun 20 Mbit/s 0 % loss). Node card counter after two
matrix runs: E `quic 16` (2 runs × 2 tunnels × 4 QUIC clients — one per client attempt), X 0; agent log one
`🚫 BLOCK QUIC (hy2/TUIC/HTTP3)` line (rate-limited).

### Before / after (lbase = fork.149 code, same toolchain; medians)
UDP RTT via echo, agents pinned to one core, 10 alternating rounds × 5000 pings (payload first byte 0xC3, so the
guard runs its full check):
| path | lbase p50 / p99 µs | new p50 / p99 µs |
|---|---|---|
| type 1, 100 B | 110.9 / 958 | 105.2 / 595 |
| type 1, 1300 B | 111.2 / 870 | 111.3 / 536 |
| type 2, 100 B | 227.6 / 1847 | 220.2 / 1552 |
| type 2, 1300 B | 235.6 / 1982 | 224.9 / 1623 |
iperf3 (3 alternating rounds, unpinned; this host also runs prod panel + other workers, so spread is wide):
| test | lbase | new |
|---|---|---|
| UDP type 1 400 Mbit/s, 1300 B | 400 Mbit/s, loss 1.75 % (0.8–5.7), agent 34.6 µs/pkt | 400, loss 3.0 % (2.0–5.3), 31.3 µs/pkt |
| UDP type 2 200 Mbit/s | 200, loss 0.58 % (0.2–1.5), 54.6 µs/pkt | 200, loss 1.55 % (0.5–1.7), 57.4 µs/pkt |
| TCP type 1 | 8063 Mbit/s, CPU 112 % | 8155, 110 % |
| TCP type 2 | 3787 Mbit/s, CPU 49 % | 3976, 49 % |
Same "recv queue full" drop counts in both (42/44, 43/44, 28/22 per round) — the loss at 38k pkt/s is the existing
per-session queue, not the check. No measurable difference in latency, throughput or CPU.

## Release (fork.151)
- Rebased onto fork.150 (plan 280) before the full test run; tag `3.0.27-fork.151` → 827977d4, branch
  `maintenance/3.0.27-fork.151-quic-block`. CI Build Check green, Images 44312272578 green, Latest, 10 assets,
  gost-amd64 sha256 ok, `-V` = 3.0.27-fork.151, decompressed binary contains the QUIC block.
- Prod panel swap 11-18 21:31:00–21:31:11 TW (runs/quic-block/work/deploy-151.sh), no DB change. 30/30 nodes back,
  state snapshot identical before/after (57 forwards, 利群 tables), public domain serves fork.151, 0 panel errors.
- Rollback: `/opt/flvx-svc/rollback/pre-fork151-20261118T133058Z/rollback.sh` (image swap → fork.150). Backups kept:
  pre-fork150 + pre-fork151 (prune-backups.sh).
- go-gost changed → node/upgrade hands out fork.151 agents; plan 278 (11-19 02:00 OTA of 13 old agents) brief got a
  note (its hosts are not entries of 屏蔽 TLS tunnels — nothing is blocked there).

## fork.152 follow-up (same night, before any agent OTA)
The fork.151 log line said only 「🚫 BLOCK QUIC (hy2/TUIC/HTTP3)」 once per 10 min for the whole agent, so step 9
(“which tunnels / users get QUIC blocked”) could not be answered. fork.152 keys the summary per forward service and
names it: 「🚫 BLOCK QUIC (hy2/TUIC/HTTP3) <forwardID>_<userID>_<tunnelID>_udp」 — first block of each forward at once,
then a count at most every 10 min (EventSummary, ≤ 4096 keys, overflow merged). String built only on a block. No other
change; panel images identical apart from the version. Released as fork.152 before the 11-19 OTA so no node ever runs
fork.151 (except via plan 278 if it had started — it had not).
