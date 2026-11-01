# 183 — release-socks-strict: plan 182 as 3.0.27-fork.104 (panel + OTA of the 8 filter entries)

Approval: user 2026-11-01 ~14:40 TW (session 1ca872e8) — ship both fixes of plan 182 once built (2nd release of the day).
Efficiency rule: no added latency, no log risk, no extra CPU / RAM — prove with before/after numbers. No screen review.
Branch `maintenance/3.0.27-fork.104-socks-strict` = `feature/3.0.27-socks-strict` HEAD b3f26128 (fork.103 + plan 182,
reviewer-passed r1, CI green 41290152283). Run dir `/root/flvx-workers/runs/release-socks-strict`.

## Tasks
- [x] Preconditions: build final.md reviewer-passed, CI green on b3f26128, prod = fork.103, next tag = fork.104
- [x] Gates on the release head: go build (backend, go-gost, x), backend tests SQLite + PostgreSQL, agent tests, frontend tsc / build / lint
- [x] Baselines: BLOCK SOCKS 24 h per filter entry (2, 15, 23, 32, 48, 50, 51, 54), 最优 switch/push counts per tunnel (3 h),
      client checks of real rules, panel CPU/RSS, agent RSS/CPU on the entries
- [x] Tag 3.0.27-fork.104, CI + Images + release assets (amd64 + arm64, sha256) verified
- [x] Panel backup (validated DB) + rollback dir; upgrade /opt/flvx-svc; health + version; prune backups
- [x] OTA the 8 filter entries one at a time: 2 → 23 → 15 → 50 → 51 → 54 → 48 → 32 (never 36); 30 min watch after 23
- [x] Prod proof: temp admin rule on a filter entry — real SOCKS5/4 still cut, 200 SS connections → 0 SOCKS blocks; temp rule deleted
- [x] Part B proof: rule-less 最优 tunnels push nothing for 1 h, tunnel 4 as before; 协议拦截 line screenshots 390 + 1440
- [x] Resources after the last OTA (panel CPU/RSS, agent RSS/CPU on 3 entries incl. 23, log lines, tunnel connect latency)
- [x] Summary (final.md / summary.md) + plan complete

## Results (pre-release, 2026-11-01 TW)
- Preconditions: build r1 reviewer passed (runs/socks-strict/final.md), CI Build Check green on b3f26128 (41290152283),
  prod panel + all 8 filter entries on fork.103, latest tag fork.103 → this release = fork.104.
- Gates: backend build ok, go vet = the same 5 pre-existing `unkeyed fields` notes as fork.103; backend tests SQLite 20/20 ok,
  PostgreSQL 20/20 ok (0 failing); go-gost + x build ok, x tests ok; tsc 0 errors; vite build ok; eslint 94 errors = fork.103 94.
- Baselines (16:57 TW, journal 24 h): BLOCK SOCKS node 23 = 213 (3 h: 32), 48 = 24, 32 = 13, 15 = 3, 2 = 1; nodes 50/51/54
  have no SSH (no journal baseline; counter line after OTA). 最优 switches last 3 h: 49 `[best-path]`, all on rule-less
  tunnels (27×14, 30×10, 97×8, 6×7, 94×4, 67×4, 96×1, 29×1); `[rule-best]` tunnel 4: 9 since fork.103.
- Pre-OTA proof (temp rule 253 on tunnel 25 / entry 23, deleted): 200/200 SS2022 conns ok, p50 1256 ms; curl socks5/5h/4/4a all cut.

## Results (release, 2026-11-01 TW)
- Tag `3.0.27-fork.104` → f8f1c1ca; CI Build Check 41292626202 green; Build and Push Images 41292627030 green; release Latest,
  10 assets = fork.103 set, pins + sha256 (amd64 0d5533d9…, arm64 f636bb75…) + ghcr amd64/arm64 verified.
- Panel: backup `/opt/flvx-svc/rollback/pre-fork104-20261101T093000Z` (validated DB, quick/integrity ok), upgrade 17:30 TW,
  healthy in 8 s; 29/29 online nodes redeployed diff-only (58 × failed=0); forward_port 38 / runtime objects 245 identical;
  backups kept: pre-fork103 + pre-fork104 (pre-fork102 + fork.101 images pruned).
- OTA 17:32–18:25 TW, order 2 → 23 → 15 → 50 → 51 → 54 → 48 → 32, all verified first try (reconnect 4–6 s, diagnose identical,
  scheme + panel sockets unchanged); node backups `/root/flvxx-backup-20261101T093213Z-pre-fork104` on 2/15/23/32/48 (each
  node keeps 2). Other nodes stay fork.103; 36 fork.95 untouched; 31 offline (fork.98).
- Entry 23 after OTA: 200/200 SS2022 conns, 0 SOCKS blocks, p50 1254 ms (before 1256 ms); curl socks5/5h/4/4a cut → counter
  socks 0→4; plain HTTP + TLS ClientHello cut → http 0→1, tls 0→1. 30-min watch: counter stayed at the 4 test blocks (old
  agent ≈ 5 false blocks / 30 min on 23). All 8 entries: 0 SOCKS blocks from real traffic since OTA. Temp rules 253/254/255 deleted.
- Part B: 1 h after the upgrade 0 `[best-path] switch` lines (before: 61 / 3 h, all on rule-less tunnels); hourly round
  `best_idle_tunnels=19 best_switches=0`; `[rule-best]` on tunnels 4 / 28 continues (8 lines, incl. one failover + switch-back).
- Resources: panel 10.05 → 11.0–11.2 CPU-s / 5 min (fresh process; fork.103 fresh was 10.99), RSS 39.0 → 40.8–41.5 MiB,
  non-request log lines 0–2 / 10 min; agents RSS 23 74→80 MB (traffic 20→56 conns), 2 72→70, 32 95→63, 48 92→60, 15 61→58;
  CPU same (23 2.1→2.1 %, 2 2.4→2.5 %, 32 0.40→0.40 %); journal 0–4 lines / 10 min in steady state.
- Screens: runs/release-socks-strict/shots/prod/node-23-card-{390,1440}.png (「协议拦截 SOCKS 4 · HTTP 0 · TLS 0（自 agent 启动）」).
