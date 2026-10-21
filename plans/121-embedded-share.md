# 121 — AnyTLS / Snell / Mieru on FLVX shared landings (embedded-share)

User decision 2026-10-21 (「那件模式也把它做了吧」, trial machine 「Dmit T1」(10)): mmwx servers that run official
**Embedded** mode can be shared to FLVX, and FLVX creates and fully manages AnyTLS / Snell / Mieru inbounds in its own
shared Core (`mmwxc-flvx-core`, our forked Xray, branch `custom-connection-control`) — users, limits, online IPs,
traffic, machine protection — so users can later move off official's unmanaged inbounds. Official inbounds stay as
they are.

Base: tag `3.0.27-fork.64` (92caf0a8, = prod). Branch: `feature/3.0.27-embedded-share`.
**Build + screenshots only** — no tag, no release, no prod upgrade, no OTA, no writes on any real server / prod
panel / Custom. No mmwx code: the later `embedded-share-mmwx` worker implements Custom + Helper against the contract
`/root/flvx-workers/contracts/landing-protocols-v1.md`.

## Checklist
- [x] 1. Read-only facts: official modes + inbounds of the Embedded servers, 「Dmit T1」(10) ports / RAM / disk /
      Helper / FLVX node mapping
- [x] 2. Core proof (local lab): build the fork Core, AnyTLS / Snell / Mieru inbounds in the Helper's config shape,
      real clients (mihomo, sing-box), TCP + UDP
- [x] 3. Core proof: per-node traffic stats, user attribution, connection cap, online-IP cap, block-on-disable
- [x] 4. Core proof: resource numbers (~200 idle + 20 active clients per protocol vs VLESS)
- [x] 5. Contract `landing-protocols-v1.md` (capability flag, busy ports, server mode, per-protocol node fields,
      client fields, old-Custom behaviour)
- [x] 6. Backend: contract types (`landingcustom`), capability gating, server protocol validation, sync keeps
      advertised protocols
- [x] 7. Backend: node model columns + credential generation for the three protocols
- [x] 8. Backend: desired encoding; nodes whose protocol the share stopped advertising are withheld (not sent) and
      marked, so the rest of the share keeps syncing
- [x] 9. Backend: port picking skips Custom-reported busy ports
- [x] 10. Backend: client links / Clash (mihomo) YAML / Surge line for the three protocols
- [x] 11. Backend: native landings unchanged (new protocols refused there, reason documented)
- [x] 12. Embedded servers: sharing allowed end to end on the FLVX side; server mode shown as info only
- [x] 13. Frontend (add only): protocol labels, server protocol checkboxes gated by capability, create-node
      protocol options, client config view, Embedded chip
- [x] 14. Tests: new backend tests; `go test ./...` vs fork.64 baseline by name; go-gost/x tests; frontend
      tsc / eslint / build (< 5 MiB)
- [ ] 15. Push branch, CI green
- [x] 16. Screenshots (desktop + H5, light + dark main ones) with a local demo backend + fake Custom, index page
- [ ] 17. Summary + release-round plan (`/root/flvx-workers/runs/embedded-share/summary.md`)

## Decisions and findings (lab: /root/flvx-workers/runs/embedded-share/)
- Core (released `mmwxc-core` v1.4.69 = fork 8f5b614) with real clients (mihomo 1.19.32, sing-box 1.12): all three
  protocols work over TCP; limits (online-IP cap, connection cap, block → rmi, re-add) behave like SS2022 / VLESS.
  Two Core defects found, fixed in a local build and proven:
  - AnyTLS download bytes are **not counted** (server writes bypass the inbound counter) → one-line fix in
    `proxy/anytls/inbound.go` (`buf.SequentialWriter{Writer: conn}`); without it AnyTLS downloads would be free.
  - Snell UDP sends every datagram of a session to the first destination → upstream fix `3f844db6`.
  The contract makes Custom advertise AnyTLS (and Snell UDP) only with a Core that has the fixes.
- AnyTLS uses TLS with a panel-generated self-signed ECDSA P-256 certificate that Clash/mihomo pins
  (`fingerprint`); REALITY was rejected (mihomo's AnyTLS client ignores `reality-opts`). Share links carry
  `insecure=1` (the AnyTLS URI has no pin parameter).
- Snell v4 / v5 (obfs none/http/tls); v6 left out (mihomo cannot use it). Mieru: one inbound listens on TCP and UDP of
  the same port; the client transport (TCP default) is a server default. Mieru over UDP costs ~6× the CPU of VLESS for
  the same throughput.
- Extra protocols are opt-in per shared server; the v1 `protocols` list from Custom is unchanged so older FLVX keeps
  working. If a share stops advertising a protocol, its nodes are withheld (not sent, shown with the reason) and come
  back unchanged later.
- Ports: Custom's `busy_ports` (stored in `landing_server.shared_busy_ports`, outside the caps blob) plus ports FLVX
  uses on the same machine via an FLVX node with the same address are skipped; forward ports on that FLVX node skip the
  shared landing ports.
- Native FLVX landings unchanged (SS2022 / VLESS): supporting the three there needs an agent change + OTA and the fixed
  Core on every native node; the feature targets mmwx Embedded machines, which are shared, not native.
- 「Dmit T1」(10) is also FLVX node 34 (dual-installed, idle). Official mode Embedded with only one official inbound
  (VLESS Reality :10015); x-ui listens 2096 / 34509 / 51501 / 52796 (TCP+UDP); 964 MB RAM (~190 MB available),
  1 vCPU, 11 GB disk free; Helper v0.7.9 present, `mmwxc-flvx-core` not installed. Of the six Embedded servers only
  「Nobrand 沪港IPLC」(17) actually has Mieru inbounds today.

## Results (build round)
- Backend `go test ./...` vs the full fork.64 tree: the same 15 known failures by name, no new ones; `go vet` output
  identical. New tests: `landingcustom/protocols_test.go`, `repo/repository_landing_protocols_test.go`,
  `handler/landing_protocols_test.go` (PostgreSQL variants skipped: no test DSN on this host, as before).
- go-gost/x: unchanged by this branch; all packages pass (the landing package needs a short TMPDIR for its unix
  socket paths).
- Frontend: tsc OK, eslint warnings per changed file equal to fork.64, build OK (largest asset 2.89 MB < 5 MiB).
- Local landing acceptance incl. the new P1: 14 scenarios, 118 checks, all passed.
- End to end (panel → fake Custom rendering contract §6 → real Core → mihomo / sing-box from the panel's own
  configs): 24/24 with the patched Core; 22/24 with the released Core (Snell UDP; AnyTLS download unbilled).
- Screenshots: 25 (desktop + H5, light + dark) in `/root/flvx-workers/runs/embedded-share/screens/index.html`,
  preview `/embedded-share/` on the mmwx-preview server.
