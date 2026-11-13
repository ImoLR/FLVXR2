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
- [x] local lab with the real Core: Xray + mihomo clients, TCP byte-exact, UDP, billing, conn cap, disable,
      gost tunnel with TLS/HTTP filter ON, chain Reality → exit → vless_enc upstream, random vs native numbers,
      Core RSS/CPU
- [ ] gallery :8791 `/landing-vless-enc/` (desktop 1366×900 + mobile 390×844, light + dark)
- [ ] push branch, CI green
- [ ] ledger row 253 (待发布), summary `runs/landing-vless-enc/summary.md`, cleanup

## Results

### Local lab (netns `ve253*`, no internet, synthetic DB; scripts in `/root/flvx-workers/runs/landing-vless-enc/lab/`)

Panel + agents built from this branch; exit / native landing X runs the pinned landing Core (Custom v1.4.83 =
Core f6f58447); entry E = normal tunnel 「广州专线 屏蔽TLS」 (TLS + HTTP filter on), entry L = 线路机; the user's own
node = a standalone Core with VLESS Encryption on 11.253.0.20:9443. `e2e.py`: **36/36 passed**:

- Native node created by the user through the API; keys made by the panel; private key never in a response.
  复制链接 `vless://…?encryption=mlkem768x25519plus.random.0rtt.<pub>&flow=xtls-rprx-vision&security=none&type=tcp`.
- **Xray 26.3.27** (link) and **mihomo 1.19.32** (generated Clash entry): TCP, 32 MiB download byte-exact, UDP
  (Vision XUDP) end to end. sing-box 1.14.3 rejects the `encryption` field (`unknown field`) → left out.
- Billing: counters grew by both downloads (billed 134 MB). Connection cap 入站 2: two sessions up, the third
  refused; panel shows the live inbound count. Disable cuts the live session and refuses new ones.
- **0-RTT note**: after the node is re-enabled (inbound re-added) or the Core restarts, each client's first
  connection with its old 0-RTT ticket is answered with noise and fails once; the client then re-handshakes
  (1-RTT) and the next connection works (Xray design; mihomo the same). Limit changes did not cause it.
- **专线 屏蔽 TLS + HTTP**: the rule to the vless_enc node passes (TCP, 32 MiB byte-exact, UDP); Reality on the
  same tunnel is refused by the panel; control: a TLS handshake through the same entry is cut (filter active).
- **线路机**: vless_enc node refused for user and admin (plan-250 text). 「我自己的节点」 with the VLESS Encryption
  link: chain active, client → L → X (Reality) → VLESS Encryption upstream → target: TCP, 32 MiB byte-exact, UDP;
  the exit's Core config carries the upstream `encryption`.

Throughput / CPU, `random` vs `native` (standalone Core server ↔ Xray client on the lab bridge, Vision on,
4 × 128 MiB sequential + 4 parallel, 2 runs each; server CPU per GiB moved):

| target | mode | 1 stream | 4 parallel | server CPU s/GiB | client CPU s/GiB |
|---|---|---|---|---|---|
| plain HTTP | random | 471–502 MiB/s | 1276–1415 MiB/s | 0.86–0.98 | 2.08–2.22 |
| plain HTTP | native | 478–485 MiB/s | 1172–1316 MiB/s | 0.88–0.94 | 2.14–2.18 |
| HTTPS (inner TLS 1.3) | random | 644–860 MiB/s | 1438–1809 MiB/s | 0.56–0.90 | 1.12–1.50 |
| HTTPS (inner TLS 1.3) | native | 1079–1094 MiB/s | 1732–1810 MiB/s | 0.58–0.68 | 0.50–0.56 |

With inner TLS (most real traffic) `native` lets Vision splice, so `random` costs **≈ 20–40 % single-stream
throughput** and about 2–3× client CPU in this CPU-bound lab; parallel throughput and server CPU per GiB are
close. Kept `random` as agreed (all bytes look random, best chance through 专线 protocol filters); on real nodes
(100 Mbit–1 Gbit) the link, not the CPU, is the limit (1 Gbit/s ≈ 0.12 GiB/s ≈ 0.1 CPU core on the server).

Resources: per inbound ~14 KiB RSS (50 vless_enc inbounds +0.7 MiB) vs ~31 KiB for Reality (+1.5 MiB); per idle
session ~52 KiB (random) / 57 KiB (native) on a standalone Core, landing Core 27–85 KiB vs Reality 15–152 KiB
(GC noise; same order); handshake CPU ~0.7 ms per new session (0-RTT). Keys only at node create; no new loops,
probes or goroutines (the agent renders one more protocol case; the panel adds one column read).
