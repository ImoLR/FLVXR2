# 274 — chain-only → landing conversion fixes + AnyTLS on native landings + guarded Reality warm hold

Base: `3.0.27-fork.144` (fea5c139). Branch `feature/3.0.27-native-anytls`, worktree
`/root/projects/flvxx/flvxx-native-anytls`. Run dir `/root/flvx-workers/runs/native-anytls`.

## Background (prod 11-17)
- Bestvm US (landing server 9) was a chain-only exit (线路机 tunnel 116, user 74's chain rules, chain
  listeners 1024/1025). The node page showed 「落地设置」 instead of 「启用为落地」, and the conversion
  could never pick a free 1000-port block because the chain listeners (outside every block by design)
  had to fit inside it. Worked around by hand on prod (range 30000-30999, enable).
- AnyTLS was refused on native landings (「AnyTLS / Snell / Mieru 目前只支持妙妙屋共享服务器」).
- Rule 624 「测试 US」 (tunnel 115 → Bestvm US guarded Reality :30000) shows 目标不支持预热: the Reality
  guard's TCP_DEFER_ACCEPT 5 s drops silent pooled conns after ~7 s.

## Decisions
- Chain-only exit conversion: chain-node listeners (127.0.0.1) never constrain the picked block; landing-node
  listeners still must fit. Guard ports are re-allocated into the range as before.
- Native AnyTLS only (Snell / Mieru stay shared-only), gated by a new agent capability `landing_anytls` (1);
  nodes withheld from older agents (an older agent rejects the whole desired state). Same cert/SNI handling as
  shared/chain AnyTLS (self-signed per node, SNI, sha256 pin). No new UI elements.
- `guardDeferAccept` 5 → 130 (kernel rounds to 8 SYN-ACK retransmits ≈ 247 s effective hold; lab measured 251–252 s).
  Public AnyTLS inbounds get the same deferred accept (silent pooled conns outlast the Core's 120 s handshake).
- Native AnyTLS SNI: server AnyTLS SNI > public host (if a domain) > first Reality server name (plan 250 chain rule).

## Tasks
- [x] 1. Node list exposes `landingChainOnly`; node-page button shows 「启用为落地」 for a chain-only landing.
- [x] 2. `EnableLandingServerNote` chain-only branch: own ports = landing-node listeners only; repo test
      (default block busy + chain listeners 1024/1025 → free block picked, chain rows untouched).
- [x] 3. Audit: nothing else assumes chain listeners inside PortMin..PortMax of a public server.
- [x] 4. Backend native AnyTLS: capability column/ws/report, server save + node create checks, withheld/not-sent,
      native desired state, links + client configs + subscriptions, filters (plan 224) + 线路机, scan guard, egress.
- [x] 5. Agent: public AnyTLS inbound allowed, `landing_anytls` capability reported; tests.
- [x] 6. Frontend: AnyTLS checkbox/defaults on native server settings reusing the shared UI; create dialog.
- [x] 7. Agent: `guardDeferAccept` → 130, effective hold measured on a real kernel; contract-change.md for mmwx.
- [x] 8. Audit warm pool vs Reality/TLS/AnyTLS (items 5a–d) incl. type-2 loopback dial not pooled.
- [x] 9. Tests: go-backend `go test ./... -timeout 30m` (compare with base), go-gost/x landing+warmpool, `npm run build`.
- [ ] 10. Lab e2e: migrated prod-DB copy (offline) + netns data plane: native AnyTLS node + mihomo/sing-box,
      regression SS2022/Reality/vless_enc + chain rules of a converted exit; guard hold / warm pool / late ClientHello.
- [x] 11. Gallery :8791 /native-anytls/ (desktop + H5).
- [ ] 12. Release next fork (CI, images, Latest assets); prod backup + prune + rollback script; panel upgrade; verify.
- [ ] 13. Night window (02:00–06:00 TW): OTA Bestvm US only; enable AnyTLS on server 9; throwaway AnyTLS node test
      with a real client; rule 624 tag state.
- [ ] 14. RELEASE-LEDGER row 274, memory state line, summary.md.

## Results (build)
- Unit/contract: go-backend `go test ./... -timeout 30m` (SQLite + PG 18) green after updating two tests that encoded the old
  behaviour (native AnyTLS refused; admin server-view field list). go-gost/x: green except `TestLandingRealCoreUpgradeRestart`
  (fails identically on fork.144: needs a Core download fixture) — real-Core tests run with the pinned Core.
- Guard hold (netns, Linux 6.12, RTT 40 ms): 10 conns aged 135 s alive, Core accepted 0 (SYN-RECV), 8/8 Reality handshakes
  through aged conns, pooled first byte −28.5 ms p50 / −35 ms min vs fresh; untouched silent conn held 251 s; plan 127 late
  ClientHello 0/30 failures; delay+loss run 0/500 failures.
- Lab A (netns, synthetic): native AnyTLS (mihomo TCP+UDP, sing-box/Clash/Surge/v2rayN subscriptions, wrong pin refused,
  线路机 OK, 屏蔽 TLS refused), SS2022/Reality/vless_enc regression, 入站连接数 not used by silent conns, chain-only exit
  (listener 1024) converted with a busy default block → free block, chain rule still works; warm pool 端口转发 → guarded
  Reality: warm −43 ms vs cold.
- Lab B (prod-DB copy, offline): node/tunnel/forward/landing APIs identical except new fields, links of all 11 landing nodes
  and all subscriptions identical, prod pre-workaround Bestvm US conversion → 30000-30999 (= hand fix), AnyTLS on server 9,
  fork.144 paneld runs on the migrated DB.
