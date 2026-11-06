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
One-time check 11-06 (one TLS handshake per domain, 4 s apart) from the dev box (all 28 TLS 1.3 + h2 + X25519 OK) and,
SNI-wise, from Shanghai (leikwan-sh, ClientHello with that SNI to a foreign edge IP): only dl.google.com timed out.

Usable (10, probed, default on): www.lovelive-anime.jp (old default, CloudFront), www.tesla.com, www.nvidia.com,
www.amd.com, www.intel.com, www.sony.com, www.samsung.com, www.dell.com (Akamai), www.logitech.com (CloudFront),
addons.mozilla.org (Fastly).

Default-blocked (built-in, admin can 解除屏蔽; never probed, never auto-picked):
| Domain | Why (one line, shown in the UI) |
|---|---|
| www.microsoft.com | 教程和各家面板最常用的目标，用的人太多；微软在大陆有本地节点，海外 IP 冒充它容易露馅 |
| www.bing.com | 微软，同上 |
| www.apple.com | 苹果：热门目标；大陆走本地 CDN，海外落地冒充它特征明显 |
| www.icloud.com | iCloud 大陆由云上贵州运营，海外 IP 冒充它特征明显 |
| www.amazon.com | 热门默认目标；亚马逊自有 IP 段（ASN）特征明显 |
| aws.amazon.com | AWS 官网，同上 |
| www.cloudflare.com | 3x-ui 第一个默认目标，用的人极多；Cloudflare 自有 ASN 13335 |
| dl.google.com | 大陆连谷歌 IP 不通（上海实测超时）；谷歌域名在大陆解析到北京服务器，冒充明显 |

妙妙屋域名池 entries of the same kinds are blocked when they first arrive (微软系 / 苹果系 / 亚马逊·AWS / Cloudflare /
谷歌系 / 国内网站 .cn, bilibili, ixigua, kwai …), each with its one-line reason. With the current 96-domain pool:
99 candidates, 30 blocked, 64 probed (cap).

## 妙妙屋 side (Custom v1.4.88 / Helper v0.7.22, worker helper-reality-pick)
- `GET /shares`: `reality_probe` / `reality_pin` `{versions:[1]}`.
- `POST /shares/{id}/reality-probe {targets, timeout_ms 500–5000}` → job `{probe_id, state queued}`; the Helper picks it
  up with its next upload; `GET /shares/{id}/reality-probe/{probe_id}` → `state done|error, results[]` (float ms).
  FLVX waits ≤ 60 s in the request, then finishes in the background (UI polls `server/reality-probe/status`).
- `GET /shares/{id}/status` → `reality_pins[{host, pinned_ip, pinned_ms, best_ip, best_ms, checked_at, nodes, fallback}]`.
- `GET /reality-pool` → `{domains:[{domain,…}], fetched_at, error, refreshing}` (empty + not fetched = keep ours).

## Tasks
- [x] Agent: RealityProbe + pin + status + capability; unit tests (pin choice, hysteresis, private filter, probe)
- [x] Panel: model/migration/seed, candidate API, server mode/probe API, auto pick, daily loop, pin status, shared (r2: shared client on the real Custom v1.4.88 API — async job + poll, float ms, pool error/refreshing)
- [x] Frontend: global list, server 伪装域名 + 测速 table + pin display
- [ ] Gates: go test (agent x/landing, backend all), contract SQLite + PostgreSQL, tsc/build/lint, CI green
- [x] Lab: netns pin move (pinned IP made unreachable) with the real Core, PID unchanged (fast 12.0.9.11 2.4–3.3 ms → cut → 12.0.9.10 83 ms, same PID/epoch, live VLESS conn kept)
- [x] Screens phone 390 + desktop 1440 → /root/mmwx-preview/reality-domain-pick/
- [ ] Wait for mmwx helper-reality-pick final.md + Custom/Helper live (or 6 h → native only)
- [ ] Release fork.121 (+225b, 226), CI + images + assets, ledger
- [ ] Baselines, prod backup, panel upgrade, OTA NexKR + Bestvm US (user 11-06 18:40 TW: skip all 4 Breadcloud machines — vendor maintenance, no OTA/SSH/probes)
- [ ] Proof per online landing server (independent SSH probe, rp- temp VLESS node, a/b/c/d, e in lab), cleanup 0 leftovers
- [ ] Resources (probe cost/day, agent RSS/CPU NexKR (Breadcloud skipped), TTFB before/after), final.md + summary.md
