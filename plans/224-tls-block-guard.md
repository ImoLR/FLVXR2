# 224 — 「屏蔽 TLS」隧道上的落地协议防呆 (tls-block-guard)

Branch `feature/3.0.27-tls-block-guard` from `3.0.27-fork.118`. Build only (next batch release).

## Why
ab-bench P1-1: on a tunnel with 屏蔽 TLS (`tunnel.tls = 1`) a rule to a landing
node whose client traffic starts with a TLS ClientHello (VLESS Reality,
AnyTLS …) is accepted, but the entry's protocol filter closes every
connection — the rule never works and nothing tells the user. User 11-06:
「你看着修，补齐这个逻辑」. Wording: 「此隧道屏蔽了 TLS 协议特征」 /
「该节点协议带 TLS 特征，此隧道无法使用」 (never 「只能接 SS」).

## Lab result (real clients → real entry filter)
Entry filter = go-gost `service.isProtocolBlocked` (detectTLS: first read
starts `16 03 01..04`; detectHTTP: first read starts with an HTTP method).
First client packet of each protocol captured from mihomo v1.19.15 against a
TCP sink, then fed through `isProtocolBlocked` with the flags prod tunnels
use (`tls=1` and `http=1,tls=1,socks=1`):

| Landing protocol (client) | first bytes | TLS filter | HTTP filter |
|---|---|---|---|
| ss2022 (2022-blake3-aes-128-gcm) | random salt | pass | pass |
| vless_reality_vision | `16 03 01` ClientHello | **blocked** | pass |
| anytls | `16 03 01` ClientHello | **blocked** | pass |
| snell obfs none | random salt | pass | pass |
| snell obfs http | `GET / HTTP/1.1` | pass | **blocked** |
| snell obfs tls | `16 03 01` ClientHello | **blocked** | pass |
| mieru TCP | random | pass | pass |
| (mieru UDP / UDP of any protocol) | — | not inspected (filter skips UDP) | — |
| (ref) trojan / vless+tls | `16 03 01` | blocked | pass |
| (ref) vmess / vless plain | random / `00`+uuid | pass | pass |

So the feature set is per node, not per protocol name:
- TLS feature: `vless_reality_vision`, `anytls`, `snell` with obfs `tls`.
- HTTP feature: `snell` with obfs `http` (same bug class on 屏蔽 HTTP
  tunnels — every tls=1 prod tunnel also has http=1; included so the logic is
  complete; no such node exists on prod today).
- Everything else: none.

## Design
- One backend source: `repo.LandingNodeFilterFeatures(node)` /
  `repo.LandingProtocolFilterFeatures(server, protocol)` +
  `repo.TunnelBlockedFeature(tunnel flags, features)` and the two messages.
- API carries the result (frontend never re-derives it):
  - `landing/node/list` item `filterFeatures: ["tls"]` (in-memory, no query);
  - `landing/server/list` item `protocolFilterFeatures: {protocol: [...]}`;
  - rule-dialog tunnel list (`tunnel/user/tunnel` visible list) item
    `blockedFeatures: ["tls","http"]` (columns added to the existing select);
  - `landing/node/links` forward links carry `blocked` reason (legacy rules).
- Backend reject on forward create / update (target or tunnel change) /
  batch change tunnel; chain (「我自己的节点」) create / target change on
  a TLS-blocking tunnel refused (client speaks VLESS Reality through it).
  Unchanged targets stay editable (existing rules never broken).
- Tunnel edit: turning 屏蔽 TLS / 屏蔽 HTTP on shows count + names of the
  rules that would stop working; admin can still save.

## Tasks
- [x] Lab table (above) — done before code
- [x] Backend helpers + messages (one place) + unit test
- [x] Forward create/update/batch-change-tunnel/chain guards (backup import left alone: restoring old data must not fail)
- [x] API fields: node list / server list / tunnel list / node links
- [x] Tunnel edit warning API (affected rules for a tunnel + flags)
- [x] Frontend: picker disabled reasons + tunnel hint; create-node dialog; link card; tunnel edit warning
- [x] Contract tests (SQLite + PostgreSQL): reject path; fwd 156/347-like SS on tls=1 pass; Dmit Nathan-like VLESS on tls=0 pass; unchanged-target edit pass
- [x] go test ./... + contract; tsc/build/lint no new errors
- [x] Landing list API latency before/after
- [x] Screens 390 + 1440 → /root/mmwx-preview/tls-block-guard/
- [ ] Commit + push, CI green
- [ ] Ledger row (ready) + summary

## Results
- go test ./... (SQLite + PostgreSQL contract, -timeout 45m): all packages ok. Two pinned
  field-list contract tests updated for the new API fields (`protocolFilterFeatures`,
  `blockedFeatures`). The first run's repo/contract "timeout" = default 10 m with -p=1 + PG, not a failure.
- New: `TestTLSBlockGuardContract` (sqlite + postgres), `TestLandingFilterFeaturesLabTable`.
- Frontend: build ok; eslint errors 94 → 94, warnings per file unchanged (tunnel.tsx 1351 → 1351).
- Latency (prod DB copy, loopback netns, median of 30, ms, base → new): node/list 2.3–2.8 → 2.6–2.8,
  server/list 3.9–4.2 → 3.9–4.1, tunnel/user/tunnel 2.2 → 2.5. Query count of node/list unchanged
  (TestLandingTargetListQueryCount: 9).
- Screens + 16 automated UI checks: /root/mmwx-preview/tls-block-guard/.
- Release check (after the batch upgrade): forwards 156 「TW」 / 347 「沪台iplc中转」 (SS2022 on
  tls=1) still forward; forward 346 「Dmit中转」 (VLESS on Dmit Nathan tls=0) still works; creating a
  VLESS rule on a tls=1 tunnel is refused with 「此隧道屏蔽了 TLS 协议特征，该节点协议带 TLS 特征，无法使用」.
