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
- [x] Night OTA of the entry nodes of tls=1 tunnels (canary first, per-host backup)
- [x] Prod tests (temp forward → own hy2 / SS2022 server), our protocols unaffected, temp objects deleted
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
- Released: tag `3.0.27-fork.152` → 6f8a433a (branch `maintenance/3.0.27-fork.152-quic-block`), go-gost/x 27 packages
  green again, CI Build Check + Images 44323021058 green, Latest, 10 assets, gost sha256 ok, decompressed binary has
  the new line. Prod panel swap 11-18 22:37:00–22:37:09 TW, 30/30 back, public domain fork.152, 0 panel errors.
  Rollback `/opt/flvx-svc/rollback/pre-fork152-20261118T143659Z/rollback.sh` (→ fork.151); backups kept pre-fork151 +
  pre-fork152. Plan 278 brief updated (OTA target fork.152).

## OTA night 1 (11-19) and the Datawave HK 2 false alarm
- Plan 278 (13 old agents → fork.154) finished 03:04 TW, final.md 03:09. Prod panel by then fork.154 (plans 285 + 284,
  panel only; go-gost = fork.152) → OTA target fork.154.
- vmsilo 沪美 (canary, 0 forwards / 0 landings) → fork.154 03:09:49 TW, 11 s back online, 0 error lines, quic counter
  present. OK.
- Datawave HK 2 → fork.154 03:14 TW: diagnose / entry RTT unchanged (e.g. #63 70.6 → 70.9 ms, #91 119.7 → 119.7 ms),
  but the speed test fell 43.6 → 1.9 / 2.0 Mbit/s → ota_host.sh rolled it back to fork.138 (restore-agent.sh), the
  roll stopped; the coordinator finished the re-check (41.9 Mbit/s) and sent the TG alert 06:05 TW.
- **Cause = the test, not the agent.** The speed source on the dev box streamed 40 MB as soon as a TCP conn arrived
  (server speaks first). The entry's warm pool (plan 244/264, on for every 专线 tunnel) pre-dials idle conns to the
  forward target; each got filled with the stream, the source timed the send out after 30 s and closed it. The pool's
  idle watcher treats "the peer spoke first" as alive (SSH-banner rule) and stops watching, so the next client got such
  a conn: ~0.4 MB of buffered data, then EOF. Before the OTA (and at the 06:02 re-check) the pool key happened to be
  asleep (2 h idle) → cold dial → full speed; right after a restart the key is awake → dirty conns.
  - Same result on **fork.138** at 06:15 TW (prod, after the rollback, pool awake): 402544 B in 1.67 s, twice.
  - Lab A/B (fork.154 panel, type 1 屏蔽 HTTP/TLS/SOCKS tunnel, warm pool on): server-first stream, 2nd pull 60 s later
    → fork.154 508840 B, fork.138 508672 B (both truncated); client-first stream → 40 MB both pulls on both versions.
    iperf3 TCP (3 rounds, retries for the iperf3 "busy" bug): type 1 median 7815 vs 8059 Mbit/s, type 2 3703 vs 4110
    (plus an earlier round 4214/2919/4395 vs 3716/3910/3758) — noise, ±10–20 % within each version. Idle CPU / RSS
    equal (E 30 vs 32 ticks/60 s, 64 vs 63 MB). The only TCP-path diff 138..154 is none: the plan-283 wrapper only
    matches `quicSession` (Read + Block), implemented solely by the UDP listener's session.
  - Fixed test: the source waits for the client's first 32 random bytes (≤ 300 s; first byte 0x8d, not HTTP/TLS/SOCKS),
    like every proxy protocol our forwards carry → Datawave HK 2 (fork.138, pool awake, after the old dirty conns aged
    out): 40 MB in 7.0 / 7.1 / 7.2 s ≈ 47 Mbit/s, three times in a row.
  - Real traffic is not affected (SS / VLESS / Reality / hy2 / AnyTLS clients speak first). Pre-existing edge (since
    plan 244, not changed here): a server-speaks-first target (SSH, SMTP, FTP, MySQL) that later closes an idle pre-auth
    conn can hand a client a dead pooled conn — follow-up for the warm-pool owner.
  - TG correction sent 06:3x TW (one message). No agent fix needed → no new release; the 6 remaining hosts go to the
    panel's version in the 11-20 02:00–06:00 TW window with the fixed check (runs/quic-block/work/ota_host.sh).

## OTA night 2 (11-20) — all 6 remaining entries → fork.155 (agent code = fork.152)
Prod panel fork.155 by then (plan 286, panel only). One host at a time with runs/quic-block/work/ota_host.sh (before-check
→ backup + panel OTA → 60 s warm-up → after-check → compare; re-test on a possible regression), fixed client-first
speed probe. Boil freeze respected (first host 04:20). 7–8 s back online each, 0 regressions.

| 节点名 | 升级时间 (TW) | 诊断延迟中位 前/后 ms | 入口往返中位 前/后 ms | 速度 前/后 Mbit/s | 转发诊断 通过 前/后 | 结论 |
|---|---|---|---|---|---|---|
| vmsilo 沪美 | 11-19 03:09 | — / — | — / — | — / — | 0/0 / 0/0 | ✅ |
| Datawave HK 2 | 11-20 04:22 | 46.8 / 47.5 | 4.2 / 4.2 | 34.1 / 35.4 | 10/10 / 10/10 | ✅ |
| Mkcloud 沪日ixp 440 | 11-20 04:30 | 26.2 / 26.1 | 33.0 / 33.0 | 70.8 / 80.1 | 7/10 / 7/10 | ✅ |
| AWS HK | 11-20 04:38 | 45.4 / 45.9 | 209.8 / 208.1 | 65.0 / 51.1 | 10/10 / 10/10 | ✅ |
| Leikwanhost 沪台IPLC | 11-20 04:45 | 31.6 / 32.0 | 217.1 / 212.5 | 10.7 / 9.4 | 6/6 / 6/6 | ✅ |
| Mkcloud 深港ixp 1 | 11-20 04:51 | 28.2 / 27.6 | 4.0 / 4.2 | 65.4 / 65.0 | 10/10 / 10/10 | ✅ |
| vmsilo 沪日首发 | 11-20 04:58 | 32.6 / 32.6 | 203.8 / 207.7 | 30.6 / 14.7 (复测 25.1) | 10/10 / 10/10 | ✅ |

Notes: 诊断 = median over the host's ≤ 10 busiest forwards (3 runs each); 往返 = TCP connect to the entry port
(Mkcloud ×2 from 百度云广州, the rest from the dev box — IPv6-only / HK entries). Mkcloud 沪日ixp 440 7/10 both
before and after: clot88's 学长软银WG (WireGuard, UDP only) and two jp.okonfu.com:6666 forwards never pass a TCP
diagnose (pre-existing). vmsilo 沪日首发 speed 30.6 → 14.7 → re-test 25.1 (−18 %, inside the rule) and extra pulls
35.2 / 42.4 Mbit/s; AWS HK 65.0 → 51.1 (−21 %) and extra pulls 54.5 / 59.4 — long-haul dev-box paths, noise.
Fleet after: 30/30 online, 64 forwards active, versions 14×fork.154, 6×fork.155, 4×fork.143, 3×fork.138, 2×fork.126,
1×fork.145. Per-host rollback: `/root/flvxx-backup-<ts>-pre-fork154/restore-agent.sh` on each host
(runs/quic-block/work/backups.txt).

### Prod QUIC test (Mkcloud 沪日ixp 440, client 百度云广州, own servers on the dev box, temp admin forwards)
| check | before OTA (fork.138) | after OTA (fork.155) |
|---|---|---|
| hysteria2 plain, 端口转发 (tunnel 39) | passes (204) | **blocked** |
| curl --http3 (hy2 masquerade) | passes | **blocked** |
| hysteria2 plain, 隧道转发 (tunnel 25 → Bestvm JP B) | passes | **blocked** |
| hysteria2 + Salamander | passes | passes |
| SS2022 TCP / UDP DNS, 端口转发 | passes / passes | passes / passes |
| SS2022 TCP / UDP DNS, 隧道转发 | passes / passes | passes / passes |
| native landing SS2022 (NexKR 「自己用」) TCP / UDP DNS | passes / passes | passes / passes |
| UDP echo 1200 B p50/p99, 端口转发 | 229.8 / 233.1 ms | 227.8 / 231.5 ms |
| UDP echo 1200 B p50/p99, 隧道转发 | 220.3 / 235.0 ms | 210.3 / 215.8 ms |
Node card after: Mkcloud 沪日ixp 440 `quic 3` = these tests only; the agent log named the temp forwards
(「🚫 BLOCK QUIC (hy2/TUIC/HTTP3) 661_1_39_udp」 …). VLESS Encryption: no prod landing offers it (vless_enc off on every
server) → covered by the lab only. 线路机 Reality / AnyTLS entries (Dmit Nathan / Dmit Echo) were not changed.
All 14 temp forwards and the dev-box test servers deleted 05:10 TW; 百度云广州 test dir removed.
