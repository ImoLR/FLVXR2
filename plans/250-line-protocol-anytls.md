# 250 — 线路机 + 「我自己的节点」: AnyTLS besides VLESS Reality Vision (Surge users)

User 2026-11-12 ~22:30 TW: a user runs **Surge** (no VLESS at all), so 线路机 tunnels (plan 108: every target
must be VLESS Reality Vision) and 「我自己的节点」 chain rules (loopback inbound = VLESS Reality) give that user
nothing; the Surge subscription skips those lines. 线路机 traffic crosses the GFW directly and the entry IP is
shared by everyone, so only TLS-camouflaged protocols are allowed. **Decision (user, final): exactly two —
VLESS Reality Vision (default, recommended) + AnyTLS. Snell / SS2022 / Mieru / anything else stay forbidden.**

BUILD ONLY (no tag / release / prod change). Base `3.0.27-fork.129` (af408eee), branch
`feature/3.0.27-line-protocol-anytls`, worktree `/root/projects/flvxx/flvxx-line-proto`.
Gallery :8791 `/line-protocol-anytls/`; release waits for the user's screen approval.

## Findings before the build

- The exit's landing Core (mmwx Custom `v1.4.83` = Core `f6f58447`, an Xray fork) **has an `anytls` inbound**
  (`proxy/anytls`, conf `{"users":[{"password","email"}]}` + `streamSettings.security=tls` with inline
  certificates — exactly what mmwx Custom renders for shared AnyTLS nodes).
- The FLVX **agent** however only renders `ss2022` / `vless_reality_vision` inbounds and `validateChain`
  requires chain nodes to be `vless_reality_vision`; an unknown protocol fails the **whole** desired state.
  → **An agent change is unavoidable** (small: one inbound case + the chain check + capability bump).
  Old agents keep working: the panel sends AnyTLS chain copies only to agents reporting `landing_chain ≥ 2`;
  an exit with an older agent shows the AnyTLS option disabled with a reason (like `chainUnavailableReason`).
- Native FLVX landing servers still cannot host public AnyTLS landing nodes (plan 121: AnyTLS / Snell / Mieru
  only on mmwx-shared servers). Not changed here (scope); 线路机 rules can target shared AnyTLS nodes.

## Design

- **线路机 targets** (backend authoritative, admins too): a landing node target must be `vless_reality_vision`
  or `anytls`; `landingServerId` creates default to Reality, accept `anytls`, reject anything else with
  「线路机隧道只能使用 VLESS Reality Vision 或 AnyTLS 落地」. Compliance views / ack / messages updated.
  Existing Reality rules + nodes untouched.
- **Chain inbound protocol**: `forward_chain.inbound_protocol` (`''` = Reality, old rows), plus `password`,
  `tls_cert_pem`, `tls_key_pem`, `cert_sha256` (additive columns, defaults `''`). AnyTLS chain = same per-rule
  loopback model (one inbound per chain rule per exit, same port allocation), random 24-char password,
  self-signed ECDSA cert from the existing `selfSignedLandingCert` (generated once at create / protocol
  switch, never per request). SNI = the chain's Reality-style server name (already picked per exit, a real
  CN-reachable site, so the ClientHello looks like the Reality chain on the same entry); cert SAN = SNI so
  sing-box (`certificate` trust) verifies; every client output pins `cert_sha256`.
- **Client outputs**: 复制链接 = `anytls://` link + Clash (fingerprint pin) + **Surge line**
  (`anytls, host, port, password=…, sni=…, server-cert-fingerprint-sha256=…`, per manual.nssurge.com
  policies/anytls + policies/tls); subscriptions build an AnyTLS `sublink.Extra` (Clash / sing-box /
  Surge / Surfboard / Loon / QX all already emit pinned AnyTLS; Base64 = the link).
- **Edit**: protocol switch on an existing chain rule = a persisted, rollback-able inbound change
  (`prev_inbound`), same driver as a link change; the rule's 127.0.0.1 target never changes.
- **TLS block** (plan 224): AnyTLS is TLS → same rule as Reality (chain unavailable when the tunnel blocks TLS).
- **Reality guard** (plan 127): AnyTLS chain copies get no guard port.

## Checklist

- [x] read memory / brief / code (line_machine, line_chain, landing protocols, sublink, agent landing types)
- [x] verify Core supports AnyTLS inbound; verify Surge anytls syntax in the manual
- [ ] agent: AnyTLS chain inbound (`anytls` object, inline cert, validation), `ChainVersion` 2, unit tests
- [ ] panel: model columns + chain create with `chainInbound` (Reality default / AnyTLS), credentials + cert
- [ ] panel: desired state (AnyTLS copy only to `landing_chain ≥ 2`), egress tags consistent, guard skip
- [ ] panel: capability gating (tunnel list `chainAnyTLSAvailable` + reason, create / attach / move checks)
- [ ] panel: 线路机 targets accept AnyTLS landing (pre-check, resolve, filter pre-check, target error,
      compliance view/ack, messages)
- [ ] panel: links — 复制链接 (URL + Clash + Surge), subscription node (Extra AnyTLS, pinned), landing
      AnyTLS Surge line in 节点链接
- [ ] panel: edit protocol switch (begin / commit / revert, reconciler)
- [ ] backend tests (unit + contract) + `go test ./...` SQLite + PG
- [ ] frontend: landing picker lists AnyTLS on 线路机; create-node AnyTLS on 线路机; chain protocol pills +
      hint; row badge; chain link dialog for AnyTLS; texts; `npm run build`, eslint 0 new errors
- [ ] local lab: real AnyTLS chain end-to-end (client → entry → exit loopback AnyTLS → upstream), mihomo /
      sing-box with the generated config; Surge line syntax check
- [ ] gallery :8791 `/line-protocol-anytls/` (desktop 1366×900 + mobile 390×844, light + dark)
- [ ] push branch, CI green
- [ ] ledger row 250 (待发布), summary `runs/line-protocol-anytls/summary.md`, cleanup
