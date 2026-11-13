# 253 — Landing protocol 「VLESS Encryption」 (`vless_enc`)

Branch `feature/3.0.27-landing-vless-enc` (worktree `flvxx-vless-enc`), base tag `3.0.27-fork.130`.
BUILD ONLY (no tag / release / prod change). Task `landing-vless-enc`, user request 2026-11-13 TW.

## Why

A landing user is used to **VLESS Encryption** (Xray 25.8+ `mlkem768x25519plus`: no TLS / Reality, no SNI or
伪装站, forward secrecy + anti-replay, post-quantum key exchange). Agreed assessment: crypto stronger than SS2022
(SS2022 has no forward secrecy), on the wire the same class as SS2022 (fully random bytes), NOT TLS camouflage.

## Agreed rules (not reopened)

- Protocol key `vless_enc`, label 「VLESS Encryption」, on **native and shared** landing servers, gated like
  AnyTLS / Snell / Mieru: the server's 协议模板 switch (default OFF on existing and new servers) + a capability
  (native: agent `landing_vless_enc ≥ 1`; shared: share `protocol_caps.protocols.vless_enc`).
- Normal tunnels incl. 专线 with 屏蔽 TLS / 屏蔽 HTTP: **allowed** (no TLS, no HTTP feature, like SS2022).
- 线路机 direct targets: **forbidden** (Reality + AnyTLS only, plan 250, same error text).
- 「我自己的节点」 chain: allowed as the **upstream** (`vless://…?encryption=mlkem768x25519plus…` links), the
  client-facing hop stays Reality / AnyTLS. Exits need the new agent (`landing_vless_enc ≥ 1`), else the create /
  link change is refused with a reason naming the exit.

## Protocol settings (verified against Core f6f58447 = mmwx Custom v1.4.83, `infra/conf/vless.go`)

- Server `decryption` = `mlkem768x25519plus.random.600s.<X25519 private key, base64url raw, 32 B>`;
  client `encryption` = `mlkem768x25519plus.random.0rtt.<X25519 public key ("password"), base64url raw, 32 B>`.
  Byte-identical to `xray vlessenc` (X25519 authentication block) apart from `native` → `random`; private key
  clamped like `genCurve25519` (`[0]&=248, [31]&=127, [31]|=64`).
- Auth X25519 (ephemeral exchange is ML-KEM-768 + X25519 either way, shorter links); mode `random` (XorMode 2:
  everything incl. the handshake looks random); server ticket `600s`; client `0rtt`.
- Flow `xtls-rprx-vision`: the Core supports Vision with encryption (inbound/outbound accept a
  `*encryption.CommonConn`; with `random` (XorConn) it never splices — `CanSpliceCopy = 3` — so all bytes go
  through the counters, limits and stats like any other node). UDP = Vision's XUDP mux over the stream.
- Transport raw TCP, `security` none. One inbound per node (decryption is per inbound), keys generated once at
  node create in pure Go, stored in `landing_node.private_key` / `public_key` (private never leaves the panel
  except in the desired state; never logged).

## Shared servers (mmwx Helper) — contract `landing-protocols-v1` addition (as implemented)

- Capability: `protocol_caps.protocols.vless_enc = {"udp": true, "versions": [1]}` (inside the existing
  `protocol_caps` with `versions` containing 1). FLVX keeps the cap when `versions` is empty or contains 1;
  a `versions` list without 1 means "not supported by this FLVX". Absent → the protocol is hidden on that share;
  an already enabled `vless_enc` stays in the server's protocol list and its nodes are **withheld** (left out of
  the desired state, port + credentials kept) until the cap returns — same as AnyTLS / Snell / Mieru.
- Desired node: `"protocol": "vless_enc"` plus

  ```json
  "vless_enc": {"uuid": "<uuid v4>", "flow": "xtls-rprx-vision", "decryption": "mlkem768x25519plus.random.600s.<b64url 32B>"}
  ```

  `flow` is `"xtls-rprx-vision"` for every node FLVX creates (`""` allowed by the contract). Omitted for other
  protocols (byte-identical payloads). Error field names `vless_enc` / `vless_enc.*` map to 「协议凭据」.
- Helper renders: VLESS inbound, `settings.decryption` = the string, one client `{id: uuid, flow, email: tag}`,
  `streamSettings {network: tcp}` (no security), same limits / counters / guards as every other FLVX node.

## Native agent (go-gost/x/landing)

- New capability `landing_vless_enc: 1` (SystemInfo) → node column `landing_vless_enc`.
- Desired node `protocol: "vless_enc"` + `vless_enc {uuid, flow, decryption}` (same object as shared) →
  inbound `vless` with `decryption`, TCP only (public TCP port, no UDP port).
- Chain / egress outbound (`EgressOutbound`): new optional `encryption` (`mlkem768x25519plus.(native|xorpub|random).
  (0rtt|1rtt).…` or empty), rendered as the VLESS user's `encryption` (else `none`, byte-identical); Vision is
  allowed on raw TCP without TLS only together with `encryption`.
- Panel sends `vless_enc` nodes / encrypted chain copies only to agents reporting `landing_vless_enc ≥ 1`
  (an older agent rejects an unknown protocol and with it the whole desired state; an older agent would also
  drop `encryption` silently). **OTA needed:** native landing servers that should offer it, and 线路机 exits of
  tunnels whose users chain to a VLESS Encryption node.

## Clients

- 复制链接: `vless://uuid@host:port?encryption=<client string>&flow=xtls-rprx-vision&security=none&type=tcp#name`
  (+ a Clash / mihomo entry with `encryption:`).
- Subscriptions: see results table below.
- Node detail hint: 「需较新的 Xray / Mihomo 客户端（v2rayN、v2rayNG、Clash Meta 等）；Surge、sing-box 暂不支持」
  (final wording per lab results).

## Not in scope (documented)

- 落地「链式代理」 (plan 177, landing node → user's 外部节点) to a VLESS Encryption external node: refused with a
  reason for now (the node can still be stored and used in subscriptions). Needs PlanOutbound + landingegress
  validation + Helper whitelist; follow-up if users ask.

## Tasks

- [x] plan doc + contract
- [x] landingcustom: `vless_enc` cap + desired object + sanitize
- [x] repo: keys (pure Go, xray format), create (native agent cap / shared cap), server switch, withheld, link,
      Clash entry, filter features (none), sub node
- [x] native sync + egress + scan guard skip vless_enc for old agents; desired `vless_enc` object
- [x] agent: `vless_enc` inbound + `encryption` outbound + capability report
- [x] linechain: parse `encryption=` links, validation, label; chain create / relink gated by exit capability;
      plan-177 chain proxy refuses encrypted upstreams with a reason
- [x] sublink: Base64 / Clash (`encryption:`) emit, sing-box / Surge / Surfboard / Loon / QX skip
- [ ] backend tests (unit + contract) + `go test ./...` SQLite + PG; go-gost/x landing tests (+ real Core)
- [x] frontend: server 协议模板 switch, create-node list, rule-target picker, chain parse label, node hint;
      `npm run build`, eslint 0 new errors
- [ ] local lab with the real Core: Xray + mihomo clients, TCP byte-exact, UDP, billing, conn cap, disable,
      gost tunnel with TLS/HTTP filter ON, chain Reality → exit → vless_enc upstream, random vs native numbers,
      Core RSS/CPU
- [ ] gallery :8791 `/landing-vless-enc/` (desktop 1366×900 + mobile 390×844, light + dark)
- [ ] push branch, CI green
- [ ] ledger row 253 (待发布), summary `runs/landing-vless-enc/summary.md`, cleanup

## Results

(filled in as tasks complete)
