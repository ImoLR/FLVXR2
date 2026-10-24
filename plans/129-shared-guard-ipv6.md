# 129 — shared-guard-ipv6: shared-server Reality 防盗 (FLVX side) + Core pin with uTP fix + IPv6 /64 online IPs

Approval: user 2026-10-22 night 「按照你推荐的修就可以了」; screenshot gate waived 2026-10-24 (release worker
`release-shared-guard-ipv6` ships this build). This plan = build + tests + E2E + screenshots only (no tag, no prod,
no OTA, no mmwx code).

Base: `maintenance/3.0.27-fork.86-landing-conn-mode` (a7fff31b, newest release). Branch
`feature/3.0.27-shared-guard-ipv6`.

## Findings before the change
- mmwx Custom v1.4.75 / Helper v0.7.13 (released 10-23, all 13 servers) implement `landing-reality-guard-v1`:
  `GET /shares` → per share `"reality_guard": {"versions": [1]}` (only when that server's Helper renders it);
  `PUT .../desired` top-level `"reality_guard": true|false` (absent = off); `GET .../status` →
  `"reality_guard": {"state": "on|restart_required|error", "nodes": n}`, omitted while off / no Reality node.
  Enabling reloads the share's FLVX Reality nodes once on the mmwx side (live connections drop once).
- Core pins: FLVX native agent pins Custom v1.4.59 = Core `8f5b6145` (uTP sniffer broken). Custom v1.4.74 (= v1.4.75
  = v1.4.76, byte-identical Core) = Core `b01adef0`: 8f5b614..b01adef = Snell UDP per-packet target, AnyTLS downlink
  counter, `/v1/features` (additive), uTP sniffer fix (#6667) + tests. Snapshot stays v7. FLVX native renders only
  SS2022 / VLESS Reality, so the AnyTLS / Snell changes do not apply to it.
- The agent never restarts a running Core: after an OTA with a new pin it downloads the new binary (atomic rename),
  but the running process keeps the old one until it restarts. So a pin bump alone does not upgrade running Cores.
- IPv6 online IPs: the Core itself keys IPv6 sources per /64 (`normalizedSourceIP`, both native and shared Cores),
  so native landings count IPv6 per /64 too (consistent with shared). The panel's list filter
  (`netip.ParseAddr`) drops every `x::/64` entry → count 1, list empty, for native AND shared nodes.

## Design
### A. Shared Reality guard
- `landingcustom.Share.RealityGuard *ShareRealityGuard` (versions), sanitized like conn_modes.
- `Desired.RealityGuard *bool` sent only when the share advertises version 1 (value = server switch, default on);
  hash input unchanged for shares without it (no revision bump on panel upgrade).
- `Status.RealityGuard` → stored in the existing `landing_server.guard_state/guard_nodes` (cleared when absent).
- View: shared + advertised → same states as native (off / idle / on / restart / error) with shared wording;
  not advertised → today's 「需妙妙屋支持后才能开启」 hint (state `shared`). Switch saved for shared servers too.
### B. Core pin + stale Core
- `CoreVersion = v1.4.74`, SHA256 amd64 `488803a4…`, arm64 `12259219…` (verified against checksums.txt and files).
- Agent reports `core_update: "restart_required"` when the running Core started before the current verified binary
  was written (binary mtime > Core started_at); desired `upgrade_core: true` lets it restart the Core once (same
  restart path as the confirmed egress restart). Panel: add-only columns `landing_server.core_update`,
  `core_upgrade_ok`; admin confirms in 落地服务器设置 (checkbox, like plan 127's restart); one-shot, cleared once the
  agent reports a current Core. Old agents never report it → nothing changes for them.
- UI hint for 屏蔽 BT: TCP 明文 BT + UDP uTP recognised, encrypted BT / DHT not.
### C. IPv6 /64
- Accept IPv6 prefixes (bits 1–128, masked, no zone) next to addresses; shown as `2408:8748:a100:59::/64`;
  counted as one IP (the count comes from the Core). Small add-only note in the IP list.

## Tasks
- [x] A backend: landingcustom types + sanitize, desired gating + hash, status record, view, switch save
- [x] A frontend: settings guard section for shared (supported / not supported / states)
- [x] B agent: pin bump, stale detection, `upgrade_core` restart, status field
- [x] B panel: columns, desired flag + revision, record, view, confirm save; frontend confirm block + card hint
- [x] B text: 屏蔽 BT hint; contracts landing-egress-v1 note + landing-reality-guard-v1 status
- [x] C backend: prefix accept + tests (unit + contract with /64 entry); frontend note
- [x] Unit/contract tests for A/B/C
- [x] Backend full suite SQLite + PostgreSQL vs base; agent landing/socket/service; frontend tsc/lint/build
- [x] Real Core E2E: uTP blocked by default BT rule on new Core (old Core passes), DNS/QUIC/WireGuard UDP pass;
      full landing real-Core set (plan 107 netns, plan 127 abuse) on new Core; IPv6 /64 online IP on native Core
- [x] Guard E2E against mmwx v1.4.75 Custom + Helper + real Core (netns lab)
- [x] Resource numbers: agent + Core RSS / fd / threads old vs new pin
- [x] Screenshots desktop + H5, light + dark, before/after; gallery
- [x] Push branch, CI green
- [x] final.md + summary.md (rollout plan, rollback, 还没解决)

## Result (2026-10-24, build only — no tag, no release, no prod, no OTA, no mmwx code)
- Commit 3e4f8f94 on `feature/3.0.27-shared-guard-ipv6` (base fork.86 a7fff31b); CI Build Check 40074036308 green
  (backend, frontend, agent, PostgreSQL contract).
- Late fix found in self-review: an upgrade download that cannot run (mirrors down / no headroom) must not block the
  landing reconcile → previous pin (v1.4.59 hashes) accepted as fallback while the new one retries; verify cache keyed by
  path + hash.
- Backend `go test ./...`: SQLite 17 ok / 15 FAIL, PostgreSQL 17 ok / 15 FAIL — both failure lists identical to the
  unmodified base (fork.86) run the same way. New: `TestSharedLandingRealityGuard` (sqlite+pg),
  `TestLandingCoreUpgradeConfirm`, `TestLandingOnlineIPsIPv6Prefix`, contract `TestSharedLandingRealityGuardIPv6Contract`
  (sqlite+pg), lab fixture `TestSharedGuardLabFixtures`.
- Agent: `landing` 48 pass with the real new Core (incl. `TestLandingRealCoreUpgradeRestart` old 8f5b614 → confirmed
  restart → b01adef, `/v1/features` 404 → 200), `socket` + `service` ok, build ok.
- netns E2E on the new Core: plan 127 abuse/guard, plan 141 conn mode, plan 107 egress data plane, resource profile,
  new `TestUTPNetnsE2E` (old Core control: uTP download 4 MiB done, 3/3 real SYNs answered; new Core: 0 bytes, 0/3, for
  SS2022 and VLESS Reality; DNS dig, kernel WireGuard, QUIC/STUN/NTP/plain first packets pass on both) and
  `TestOnlineIPv6NetnsE2E` (native Core: 2 clients in one /64 + 1 in another + IPv4 → 3 online IPs, /64 entries).
- Guard lab with mmwx v1.4.75 (scratch copy, uncommitted test): FLVX's exact desired bodies pass Custom's strict decode +
  ValidateDesiredWith + ValidateEgress; real Helper reconcile on Core b01adef: 2 guard inbounds, report {on, 2};
  wrong/other-group/no SNI and non-TLS probes closed, right SNI gets the dest cert, SS2022 + 2 Reality users pass; off →
  0 guards, probes relayed, report omitted; FLVX view from the real report = {enabled, on, 2}.
- Resources: Core idle RSS 35.2 (old) vs 34.8 MiB (new), threads 13/13, fds 16/16; plan-127 perf run: throughput
  ≈480–550 vs ≈400–531 MiB/s, new-connection p50 2.35–3.0 vs 2.46–3.4 ms (noise level). Agent: +1 stat per 10 s tick,
  one-time ≈33 MB Core download on landing nodes. Bundle +2 kB.
- Screenshots: 60 (40 after / 20 before), 68/68 checks, 0 page errors —
  http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/shared-guard-ipv6/
- Rollout + rollback: `/root/flvx-workers/runs/shared-guard-ipv6/final.md`.

## Release (fork.87, task `release-shared-guard-ipv6`, 2026-10-24)
Approval: user 2026-10-24 ~04:40 TW waived the screenshot gate for this queue; the release worker is the gate.
Branch `maintenance/3.0.27-fork.87-shared-guard-ipv6` from the feature head fb73de16 (= fork.86 a7fff31b + plan 129,
fast-forward, no conflicts, code identical to the build). No new scope.

Pre-release prod read-only check: native servers 1 (node 21, 0 landing nodes) and 5 (node 55, 1 SS2022 node); shared
2/3/4/6 = mmwx 7/5/10/16, all four advertise `reality_guard {"versions":[1]}` and run Core b01adef (16 included);
**0 Reality landing nodes on prod** (all 4 landing nodes are SS2022) → enabling the guard reloads no node anywhere.

- [x] Gallery gate (release worker opened 11 of the 40 after-shots — Core upgrade block desktop light / H5 dark, guard
      toggle H5 dark, unsupported desktop dark, on H5 light, restart desktop light, IPv6 /64 IP list desktop light /
      H5 dark, card +N H5 light, BT hint H5 dark, full servers page desktop dark — plus the build's 64/64 automated
      checks; no real defect → no code change, no re-shoot; the Core-upgrade box uses the same `bg-warning-50` style as
      the existing plan-127 restart box)
- [x] Branch, `go build ./...`, backend tests SQLite + PostgreSQL (only the 15 known failures), agent tests, frontend
      tsc/build/lint — go-backend + go-gost build ok; `go test ./...` SQLite 17 ok / 15 FAIL and PostgreSQL 17 ok /
      15 FAIL, both lists identical to the fork.86 release and to the build's fork.86 base; `TestSharedLandingRealityGuard`,
      `TestSharedLandingRealityGuardIPv6Contract` sqlite+postgres PASS, `TestLandingCoreUpgradeConfirm`,
      `TestLandingOnlineIPsIPv6Prefix` PASS; go-gost/x landing (38 pass, real-Core/netns ones opt-in skipped — run by the
      build) + socket + service ok (landing needs a short TMPDIR for its unix sockets); tsc 0 errors; build ok (index
      2,993.22 kB, = build round); eslint 96 errors / 3754 warnings (= fork.86)
- [x] Equivalence gate on a fresh read-only prod DB copy (fork.86 vs fork.87 code), copy deleted
      — native payloads servers 1/5 (105 / 1303 B) byte-identical; shared desired 2/3/4/6 byte-identical with the caps
      as stored today; with the guard capability stored (= after the first /shares refresh) each shared desired only
      gains `"reality_guard":true` (nodes + egress identical, revision +1: 230→231, 182→183, 55→56, 36→37); fork.86 code
      ignores the capability (identical); views: 0 changed values, new keys only (server coreUpgradeRequired/
      coreUpgradeConfirmed; guard state shared→pending until mmwx applies); list p50 unchanged (server/list 2.24 → 2.13 ms);
      Core upgrade flow on the copy: stale report → no flag/no revision bump until confirmed → one payload with
      upgrade_core_epoch (rev +1) → cleared on the current-Core report (rev +1)
