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
- [x] Gates: go test (agent x/landing, backend all), contract SQLite + PostgreSQL, tsc/build/lint, CI green (r2: full suite on release head 57c5bb41 exit 0; CI 42225641280)
- [x] Lab: netns pin move (pinned IP made unreachable) with the real Core, PID unchanged (fast 12.0.9.11 2.4–3.3 ms → cut → 12.0.9.10 83 ms, same PID/epoch, live VLESS conn kept)
- [x] Screens phone 390 + desktop 1440 → /root/mmwx-preview/reality-domain-pick/
- [x] Wait for mmwx helper-reality-pick final.md + Custom/Helper live (r2 final 20:45 TW: 马年 / Dmit T1 / 158 / PQS TW on Helper v0.7.22; Breadcloud US 年付 not upgraded)
- [x] Release fork.121 (+225b, 226), CI + images + assets, ledger (tag → 57c5bb41, Images 42233865983, Latest, 10 assets)
- [x] Baselines, prod backup, panel upgrade, OTA NexKR + Bestvm US (user 11-06 18:40 TW: skip all 4 Breadcloud machines — vendor maintenance, no OTA/SSH/probes)
- [x] Proof per online landing server (independent SSH probe, rp- temp VLESS node, a/b/c/d, e in lab), cleanup 0 leftovers
- [x] Resources (probe cost/day, agent RSS/CPU NexKR (Breadcloud skipped), TTFB before/after), final.md + summary.md

## r2 additions (11-06 evening)
- Shared client on the real Custom v1.4.88 API (a37f135d): `targets`, float ms, async job + poll (request waits ≤ 60 s,
  UI polls `server/reality-probe/status`), pool `error` / `refreshing` (an empty pool while refreshing keeps our rows).
- Agent (6fac94f5): **10-min liveness connect** to every pinned IP (1 TCP connect, no DNS); a failed one runs the full
  check at once → a dead pinned IP moves within ≤ 10 min (+ ≤ 10 s reconcile) instead of waiting for the 6 h re-check.
  Lab (e) re-run through this path (324416b5): 12.0.9.11 cut → 12.0.9.10, same Core PID/epoch, live VLESS conn kept.
- `realityMode` hidden from the user server view; admin field-set contract updated.

## Rollout (fork.121, 11-06 TW)
- 22:25 tag 3.0.27-fork.121 → 57c5bb41; Images 42233865983; release Latest, 10 assets = fork.120 set.
- 22:43 panel fork.120 → fork.121 (rollback `/opt/flvx-svc/rollback/pre-fork121-20261106T144305Z/rollback.sh`).
- 22:47 OTA NexKR (back in 6 s), 22:48 OTA Bestvm US (5 s); landing Core PIDs unchanged on both.
- Breadcloud JP / JP2 / US / us年付 skipped (vendor maintenance, user 11-06 18:40 TW).

## Proof (independent probe ON each box: TCP to every IP ×2 ≥ 3 s apart + one TLS handshake, metric = TCP + ClientHello→ServerHello)
| Server | Picked (panel ms) | Independent: picked ms / rank | Independent #1 (its panel ms) | Pinned IP vs fastest IP |
|---|---|---|---|---|
| NexKR (native) | www.tesla.com (5) | 4.95 ms / 28 of 60 (26 within 2 ms of #1) | booking.com 2.83 (7) | 23.49.204.61 1.39 ms = fastest |
| Boil Hinet 马年 | www.amd.com (7) | 8.74 / 3 of 60 | download.gfe.nvidia.com 4.71 (14) | 184.25.120.122 2.31 vs 1.73 (in hysteresis) |
| Boil Hinet 158 | download.gfe.nvidia.com (7) | 5.67 / **1** of 60 | same | 210.61.249.41 1.59 vs 1.54 |
| Dmit T1 | download-installer.cdn.mozilla.net (3) | 2.77 / 23 of 60 (32 within 2 ms) | nus.edu.sg 1.37 (3) | 2a04:4e42::787 0.64 vs 0.46 (Helper pins IPv6 too) |
| PQS TW | www.airbnb.com (10) | 7.49 / 4 of 57 | www.samsung.com 5.79 (12) | 210.61.249.42 3.58 vs 3.37 (DNS also returns dead .25) |
| Bestvm US (chain-only) | portal.citygrainla.com (4) | **20.28 / 48 of 60** — pool site, TCP 1–31 ms jitter | www.hult.edu 1.93 (7) | no VLESS node possible (chain-only) |
- (c) ClientHello→ServerHello on the server (tcpdump, 10 paced new conns): 马年 169.5 ms (old far pin) → **10.1 ms**
  (TTFB 766 → 624 ms); 158 15.0 ms (near lovelive) → **7.6 ms** (TTFB 665 → 658 ms).
- (d) block the winner → 测速 → never probed, next pick taken, new temp node carries it: NexKR tesla → www.amd.com (node
  151), Bestvm US citygrainla → ftp.debian.org; both unblocked afterwards.
- Churn seen in the first hour: Dmit T1 addons.mozilla.org → download-installer.cdn.mozilla.net (8 min apart), Bestvm US
  files.pythonhosted.org → portal.citygrainla.com (3 min apart) — new-node default only, existing links untouched.
- Known weakness: one sample per candidate. Ties at 1 ms resolution and jittery small pool sites (citygrainla) can win.
  Follow-up (panel only): confirm round for the top 5 (2 more samples), pick by the worst sample.

