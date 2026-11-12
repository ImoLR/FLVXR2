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
- [x] agent: AnyTLS chain inbound (`anytls` object, inline cert, validation), `ChainVersion` 2, unit tests
- [x] panel: model columns + chain create with `chainInbound` (Reality default / AnyTLS), credentials + cert
- [x] panel: desired state (AnyTLS copy only to `landing_chain ≥ 2`), egress tags consistent, guard skip
- [x] panel: capability gating (tunnel list `chainAnyTLSAvailable` + reason, create / attach / move checks)
- [x] panel: 线路机 targets accept AnyTLS landing (pre-check, resolve, filter pre-check, target error,
      compliance view/ack, messages)
- [x] panel: links — 复制链接 (URL + Clash + Surge), subscription node (Extra AnyTLS, pinned), landing
      AnyTLS Surge line in 节点链接
- [x] panel: edit protocol switch (begin / commit / revert, reconciler)
- [x] backend tests (unit + contract) + `go test ./...` SQLite + PG
- [x] frontend: landing picker lists AnyTLS on 线路机; create-node AnyTLS on 线路机; chain protocol pills +
      hint; row badge; chain link dialog for AnyTLS; texts; `npm run build`, eslint 0 new errors
- [x] local lab: real AnyTLS chain end-to-end (client → entry → exit loopback AnyTLS → upstream), mihomo /
      sing-box with the generated config; Surge line syntax check
- [x] gallery :8791 `/line-protocol-anytls/` (desktop 1366×900 + mobile 390×844, light + dark)
- [x] push branch, CI green
- [x] ledger row 250 (待发布), summary `runs/line-protocol-anytls/summary.md`, cleanup

## Results

### Local lab (netns `ap250*`, no internet, synthetic DB; scripts in `/root/flvx-workers/runs/line-protocol-anytls/lab/`)

client (mihomo 1.19.32 / sing-box 1.14.3 with the **generated** configs) → entry E (线路机, gost) → tunnel (tls
relay) → exit X → `127.0.0.1:<port>` AnyTLS inbound (pinned landing Core v1.4.83) → chain outbound SOCKS5 →
the user's node (mihomo SOCKS5) → target. `e2e.py`: **26/26 passed** —
AnyTLS TCP + UDP (UoT) end to end; wrong certificate pin refused; 复制链接 = anytls:// + Clash + Surge; Surge line
matches the documented syntax (`anytls, host, port, password=…, sni=…, server-cert-fingerprint-sha256=<64 hex>`,
manual.nssurge.com policies/anytls + policies/tls; quoted values per profile/format); subscription Surge /
Surfboard / Clash / Loon / QX all carry the pinned AnyTLS node, Base64 = the link, sing-box outbound (certificate
+ SNI) works end to end; a Reality chain to the same node next to it works (local TLS 1.3 stand-in as the
伪装目标, the lab has no internet); switching to Reality is refused while that twin exists; after deleting it
AnyTLS → Reality → AnyTLS on the same rule works and the old AnyTLS credentials stop working.

Resources: one loopback inbound per chain rule per exit (`applied.json` on the exit: exactly the rule's
`flvxl-<id>`); landing Core RSS 69.0 MB → 68.8 MB after adding the AnyTLS chain (no measurable cost), +16 MB
for the Reality chain (guard + REALITY). Certificate generated once at create / protocol switch; nothing per
request; no new loops, probes or goroutines.

### Pre-existing issues found (not changed here)

- `POST /api/v1/forward/list` fails on **PostgreSQL** on the base too (fork.129): `ListForwards` joins the
  reserved word `user` unquoted (`LEFT JOIN user ON user.id = …` → `syntax error at or near "."`). Prod runs
  SQLite, so prod is not affected; the new contract test asserts the list part on SQLite only.
- Agent `TestLandingRealCoreUpgradeRestart` and `TestEgressRealCoreAcceptsRenderedConfig` fail with the pinned
  Core on this box on the unmodified base too (environment), unrelated to plan 250.

### Tests

- go-backend `go test ./...` (SQLite + PostgreSQL via `FLVX_POSTGRES_TEST_DSN`): all 26 packages pass
  (`internal/store/repo` 796 s and `tests/contract` 656 s need `-timeout 90m`; default 10 min times out).
  New: `line_chain_anytls_test.go` (create / links / subscription, old-agent gating, switch + revert +
  reconciler + attach, compliance) and contract `line_protocol_anytls_contract_test.go` (线路机 targets via the
  router, SQLite + PG; forward_chain AnyTLS columns round trip on both). Updated for the intentional
  behaviour change: `TestLineMachineTargetEnforcement`, `TestLandingTargetLineMachineProtocolAndPortImmutable`
  (ss2022 on 线路机 is now refused instead of silently switched), `TestSnellSurgeLine` /
  `TestSharedExtraProtocolsLifecycle` (AnyTLS now has a Surge line).
- go-gost/x `landing` with the pinned Core (`FLVX_LANDING_TEST_BINARY`): new `chain_anytls_test.go` passes
  (validation, render, real Core config check, hot add/remove, pinned TLS handshake on loopback only).
- Frontend: `tsc` clean, `npm run build` OK, eslint on the changed files 0 new errors / 0 new warnings.
- CI 43252667220 green on f106f02e (backend, frontend, agent, PG contract).
- Gallery http://104.145.236.29:8791/line-protocol-anytls/ — 30 screens (desktop 1366×900 + H5 390×844,
  light + dark), 16/16 checks, 0 console errors.

### Release notes (for the release worker)

- Panel: image swap; DB auto-migrate adds 7 `forward_chain` columns (defaults `''` = Reality) → rollback =
  image swap, no `--restore-db` (older panels ignore the columns; AnyTLS chains created meanwhile would be
  served as Reality by an older panel → delete/recreate them after a rollback).
- Agent OTA needed **only for AnyTLS chains**: exits of 线路机 tunnels (prod read-only 11-13: Datawave HK 2,
  AWS HK, Bestvm HK 年付, Bestvm TW, Datawave TW 1, Bestvm US, Bestvm JP B, Bestvm sg 年付; all
  `landing_chain 1` today). Until then the AnyTLS pill shows 「出口节点「…」的节点程序版本过低，升级后才能使用
  AnyTLS」 and nothing changes for Reality chains. AnyTLS **landing nodes** (shared mmwx servers) on 线路机
  tunnels need no OTA.
