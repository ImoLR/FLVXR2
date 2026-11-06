# 227 — Reality 伪装域名 测速选最快 + 屏蔽 + 妙妙屋域名池 + 原生落地最快 IP 钉选

Branch: `feature/3.0.27-reality-domain-pick` (from prod `3.0.27-fork.120`, maintenance head 2c6345f3) → release branch
`maintenance/3.0.27-fork.121-reality-domain-pick` (+ ledger rows 225b, 226), tag `3.0.27-fork.121`.
Run dir: `/root/flvx-workers/runs/reality-domain-pick/`. Contract: `/root/flvx-workers/contracts/reality-pick-v1.md`
(mmwx side = worker `helper-reality-pick`).
Approval: user 2026-11-06 ~15:00 TW (3x-ui style target pick, admin 屏蔽 + default-blocked set, mmwx 共享域名池,
fastest IP pinned dynamically, detailed proof that NEW nodes use the fastest domain + IP; existing links unchanged).

## Findings (before any change)
- Every landing_server row (9 on prod) has `reality_dest = www.lovelive-anime.jp:443`; nodes copy dest/serverNames at
  create (`createLandingNode`), later server saves never touch existing nodes. Links carry only `sni` = serverNames[0].
- Native guard (plan 127): dokodemo `address = <dest host>` + freedom `UseIP` → the Core resolves the domain for every
  new Reality connection = DNS lottery (马年: near 65.9.180.x 4 ms vs far 54.230.70.x 52–56 ms; +100–150 ms/conn).
- Only one free-text dest field (落地服务器设置); no probe, no candidate list.
- Agent commands run in their own goroutine (`go routeCommand`) → a probe of ≤ 30 s blocks nothing.

## Design
- **Agent (landing_reality = 1)**: `RealityProbe` command (≤ 64 candidates, ≤ 4 at once; per candidate: A records →
  ≤ 8 public IPv4 → one TCP connect each (1 s) → one TLS handshake SNI=host to the fastest IP, offering only X25519 +
  h2/http1.1; feasible = TLS 1.3 + X25519 + h2 + cert valid for host (system roots); serverNames = host + usable SANs).
  Pin: guard dokodemo address = fastest public IP of the dest host (≤ 8, 1 s); first pin synchronous at reconcile
  (bounded ~3 s, new hosts only), re-check every 6 h; re-pin only if pinned IP fails / leaves DNS / is ≥ 20 ms slower
  than the best; all fail → host (old behaviour). Only the loopback guard inbound is re-added: no Core restart, node
  inbounds (user connections) untouched. Status.reality_pins reported with every ApplyLandingDesired reply.
- **Panel**: table `landing_reality_domain` (domain, source builtin/custom/pool, in_pool, blocked, note); seeded 10
  usable built-ins + default-blocked set (below). Server columns `reality_mode` (auto default / manual),
  `reality_probe` (last table JSON) + `reality_probed_at` + `reality_probe_error`, `reality_pins` (JSON).
  Auto = after each probe set server `reality_dest`/`reality_server_names` to the fastest feasible non-blocked
  candidate (serverNames = host + ≤ 4 SANs) → NEW nodes only. Daily re-probe (auto, online, capable; one server per
  10-min tick). Shared servers: Custom probe/pin/pool per contract, gated by share capability; without it
  「共享服务器需要妙妙屋升级后才能测速」 and today's behaviour.
- **UI**: 全局设置 gets a 「Reality 伪装域名」 list (tags 内置/自定义/妙妙屋域名池, 屏蔽/解除屏蔽, add/remove custom);
  落地服务器设置: 「伪装域名」 自动（最快）/手动, 测速 table (domain, 可用/原因, 握手 ms, 最快 IP), pin 「当前 IP / 延迟」.

## Default candidate list (built-in)
(filled in after the one-time feasibility check, see below)

## Tasks
- [ ] Agent: RealityProbe + pin + status + capability; unit tests (pin choice, hysteresis, private filter, probe)
- [ ] Panel: model/migration/seed, candidate API, server mode/probe API, auto pick, daily loop, pin status, shared
- [ ] Frontend: global list, server 伪装域名 + 测速 table + pin display
- [ ] Gates: go test (agent x/landing, backend all), contract SQLite + PostgreSQL, tsc/build/lint, CI green
- [ ] Lab: netns pin move (pinned IP made unreachable) with the real Core, PID unchanged
- [ ] Screens phone 390 + desktop 1440 → /root/mmwx-preview/reality-domain-pick/
- [ ] Wait for mmwx helper-reality-pick final.md + Custom/Helper live (or 6 h → native only)
- [ ] Release fork.121 (+225b, 226), CI + images + assets, ledger
- [ ] Baselines, prod backup, panel upgrade, OTA Breadcloud JP / NexKR / Breadcloud US (if online) / Bestvm US
- [ ] Proof per online landing server (independent SSH probe, rp- temp VLESS node, a/b/c/d, e in lab), cleanup 0 leftovers
- [ ] Resources (probe cost/day, agent RSS/CPU Breadcloud JP, TTFB before/after), final.md + summary.md
