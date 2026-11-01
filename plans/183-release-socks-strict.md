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
- [ ] Tag 3.0.27-fork.104, CI + Images + release assets (amd64 + arm64, sha256) verified
- [ ] Panel backup (validated DB) + rollback dir; upgrade /opt/flvx-svc; health + version; prune backups
- [ ] OTA the 8 filter entries one at a time: 2 → 23 → 15 → 50 → 51 → 54 → 48 → 32 (never 36); 30 min watch after 23
- [ ] Prod proof: temp admin rule on a filter entry — real SOCKS5/4 still cut, 200 SS connections → 0 SOCKS blocks; temp rule deleted
- [ ] Part B proof: rule-less 最优 tunnels push nothing for 1 h, tunnel 4 as before; 协议拦截 line screenshots 390 + 1440
- [ ] Resources after the last OTA (panel CPU/RSS, agent RSS/CPU on 3 entries incl. 23, log lines, tunnel connect latency)
- [ ] Summary (final.md / summary.md) + plan complete

## Results (pre-release, 2026-11-01 TW)
- Preconditions: build r1 reviewer passed (runs/socks-strict/final.md), CI Build Check green on b3f26128 (41290152283),
  prod panel + all 8 filter entries on fork.103, latest tag fork.103 → this release = fork.104.
- Gates: backend build ok, go vet = the same 5 pre-existing `unkeyed fields` notes as fork.103; backend tests SQLite 20/20 ok,
  PostgreSQL 20/20 ok (0 failing); go-gost + x build ok, x tests ok; tsc 0 errors; vite build ok; eslint 94 errors = fork.103 94.
- Baselines (16:57 TW, journal 24 h): BLOCK SOCKS node 23 = 213 (3 h: 32), 48 = 24, 32 = 13, 15 = 3, 2 = 1; nodes 50/51/54
  have no SSH (no journal baseline; counter line after OTA). 最优 switches last 3 h: 49 `[best-path]`, all on rule-less
  tunnels (27×14, 30×10, 97×8, 6×7, 94×4, 67×4, 96×1, 29×1); `[rule-best]` tunnel 4: 9 since fork.103.
- Pre-OTA proof (temp rule 253 on tunnel 25 / entry 23, deleted): 200/200 SS2022 conns ok, p50 1256 ms; curl socks5/5h/4/4a all cut.
