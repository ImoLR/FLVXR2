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
- [ ] Agent: QUIC Initial detector (`service/quic_detect.go`) + per-datagram guard on filtered UDP sessions
- [ ] Agent: listener-level blacklist of a blocked client address (`internal/net/udp`)
- [ ] Agent: `quic` counter + rate-limited `🚫 BLOCK QUIC (hy2/TUIC/HTTP3)` log + report field
- [ ] Unit tests: v1/v2 Initial (captured bytes + quic-go live), short header, junk-first, SS2022 UDP, fuzz ≥ 1e8,
      flag off; benchmark ns/op + allocs
- [ ] Panel: ws ProtocolBlocks.QUIC (optional), node card QUIC chip, tunnel page note
- [ ] go test go-gost/x (all) + go-backend (SQLite + PG) + npm build
- [ ] Lab: real binaries matrix (hy2 plain / salamander, TUIC, curl --http3 / xray QUIC, SS2022 TCP+UDP, VLESS Enc UDP,
      plain UDP, TLS-allowed tunnel) on type 1 + type 2; before/after RTT / iperf3 / CPU
- [ ] Ledger row 283
- [ ] Release next fork (rebase onto newest tag), CI + assets, prod panel upgrade + rollback
- [ ] Night OTA of the entry nodes of tls=1 tunnels (canary first, per-host backup)
- [ ] Prod tests (temp forward → own hy2 / SS2022 server), our protocols unaffected, temp objects deleted
- [ ] 1–3 h QUIC counter report
- [ ] Cleanup (worktree, caches), memory, summary

## Design notes
(filled in below as built)
