# 229 — Reality 测速多测几次 + 不留 TIME_WAIT + 妙妙屋池小网站「稳定才用」

Branch: `feature/3.0.27-reality-probe-stable` (from prod `3.0.27-fork.121`, tag 57c5bb41). BUILD ONLY — rides the
next batch release (panel + agent OTA of the native landings later).
Run dir: `/root/flvx-workers/runs/reality-probe-stable/`. Contract: `/root/flvx-workers/contracts/reality-pick-v1.md`.
mmwx side (Helper linger + concurrency 2) = separate worker `helper-probe-quiet`; FLVX works with old and new Helpers.
Approval: user 2026-11-06 ~23:40 TW (answers to plan 227 「需要你决定」 1 + 2).

## Findings (fork.121, before any change)
- Agent probe: ≤ 4 candidates at once and, per candidate, every IP (≤ 8) pinged **in parallel** → up to 32 sockets
  open at once. Every socket closed with a normal FIN → our side keeps TIME_WAIT 60 s per socket (~170 per probe).
- Pin: first pin / 6 h re-check pings every IP in parallel (≤ 8 at once), 10-min liveness = 1 connect; FIN close.
- Pick = the single fastest sample (Bestvm US picked portal.citygrainla.com: one lucky sample, really rank 48).
- Pool (prod 11-06): 81 pool rows, 22 auto-blocked; 59 usable, most of them are not big vendors.

## Design

### 1. Multi-sample, few sockets at once (agent `go-gost/x/landing/reality_probe.go`, panel handler)
- **Pass 1** = every candidate once (≤ 64), **2 candidates at once** (was 4). Per candidate: resolve, one TCP connect
  per public IPv4 (≤ 8 IPs, 1 s each, **≤ 2 at once**), then one TLS handshake to the fastest IP (3 s budget).
- **Machine-wide cap**: every Reality socket (probe pings, TLS, pin liveness, pin re-check) takes a slot from one
  shared semaphore of **2** → never more than 2 of our sockets open at once on the machine. The wait for a slot is not
  part of the 1 s / 3 s budgets or of the measured time (measured from when the slot is ours). No nesting (pings are
  closed before the TLS socket is taken) → no deadlock.
- **Re-sample passes** (panel): after pass 1 the panel picks the **5 fastest usable** domains that may be auto-picked
  **+ ≤ 2 「试用」 contenders** (not yet proven, at least as fast as the slowest of the 5) and sends them **2 more
  times** with `concurrency: 1` (one domain at a time), each pass started **5 s after** the previous pass ended → the
  same domain is measured again ≥ 5 s later.
- **Ranking**: re-sampled rows rank by the **worst of their 3 samples** (tie → median), ahead of single-sample rows; a
  row that failed any re-sample is not usable. The auto-pick = first usable, not blocked, not an unproven 试用 row.
- 90 s rule: measured whole probes are 17–36 s (below) → the cap stays at 2, no need for 3.
- Old agents (landing_reality 1) ignore `concurrency` (≤ 4 at once, FIN close) and send no stats; the panel still
  re-samples and the UI says 「节点程序升级后才会限制为最多同时 2 个连接、不留 TIME_WAIT」. If a re-sample pass fails,
  pass 1 is still recorded and picked from (old behaviour).
- Shared (mmwx Helper): the same 3 passes via the existing async probe API with a `targets` subset — works with old
  and new Helpers (the Helper sets its own concurrency / close; `helper-probe-quiet` makes it 2 + RST).

### 2. No TIME_WAIT on our side
- Every probe / ping / pin socket is closed with `SO_LINGER {on, 0}` (`SetLinger(0)`): the kernel sends RST, the
  socket is gone at once, no TIME_WAIT on the landing machine. The TLS socket first sends close_notify (like a
  browser, 1 s write deadline), then the RST close. Only our measuring sockets — user connections are never touched.
- Pin guard (plan 227): the 10-min liveness check and the 6-h re-check use the same `connectAll` → also RST + cap.
- Agent reports `landing_reality: 2` and `stats {sockets, max_open, duration_ms, linger0}` per probe; the panel
  sums them over the 3 passes and shows the last 测速's cost under the table.

### 3. Pool small sites = 「试用」 until stable on that machine (panel `repository_landing_reality_samples.go`)
- **试用** = a 妙妙屋池 domain whose brand is not one of the built-in vendors' (amd / sony / mozilla …) and that is not
  in an auto-block kind (microsoft / apple / amazon / cloudflare / google …). Built-in and admin custom domains are
  never 试用.
- **History**: `landing_server.reality_samples` (TEXT JSON `{v, stats, d: {domain: [[unix_s, ms], …]}}`), last **8**
  samples per domain, dropped after **7 days**, one row per server (no per-request rows). Kept on server edits.
- **Stable** (auto-pickable) when the machine's history has ≥ **3** samples spanning ≥ **6 h** (so ≥ 2 different
  测速), every sample usable, jitter (max − min) ≤ **max(10 ms, 30 % of the median)**, and the median is not more than
  **5 ms** slower than the fastest non-试用 domain of this 测速. Otherwise the row says why (「最近 3 次抖动 12 ms」 /
  「最近 8 次里有 1 次不可用」 / 「比非试用里最快的慢 63 ms」 = unstable, orange) or 「积累中：还差 2 次测速 / 6 小时后再测一次」
  (pending, grey) and stays manual-pick only. Admins can still block or pick any row by hand.
- Threshold check against the one-time report: see 「Pool report」 below.

### 4. UI (`vite-frontend/src/pages/landing/reality-domains.tsx`)
- Probe table: new column 「次数 · 抖动」 (e.g. 「3 次 · 2 ms」; title = every sample, worst, the machine's history),
  latency shows the median, 「试用」 / 「试用 · 稳定」 tag + reason line.
- Global list: 「试用」 pill on pool small sites, count 「N 个试用」 next to 已屏蔽.
- Help text says the exact pacing (pass 1, top 5 + ≤ 2 试用 × 2 more, one domain at a time, 5 s apart, worst of 3,
  ≤ 2 connections at once, RST close) and the last 测速's cost (passes, sockets, max open, seconds).

## Measured (one-time read-only probe, 6 machines × 3 runs ~1 h apart, 11-07 00:10–02:19 TW)
Tool = `TestRealityLive` (the agent code itself, `go test -c`) over SSH, 69 domains (10 built-in + 59 pool) +
top 10 × 2 re-samples (the panel re-samples ≤ 7, so prod is a bit less). `ss` sampled every 0.2 s.

| Machine | Sockets / probe | Max open (self / ss) | Whole probe | RSS of the tool | Added TIME_WAIT to probe IPs |
|---|---|---|---|---|---|
| NexKR | 276 / 298 / 289 | 2 / 2 | 19 s | 15 MB | 0 / 0 / 0 |
| Bestvm US | 311 / 334 / 351 | 2 / 2 | 17–19 s | 14 MB | 0 / 0 / 0 |
| Boil Hinet 马年 | 303–310 | 2 / 2 | 23–25 s | 15–16 MB | 0 / 0 / 0 |
| Dmit T1 | 345–354 | 2 / 2 | 17–18 s | 14–15 MB | 0 / 0 / 0 |
| Boil Hinet 158 | 317–328 | 2 / 2 | 23–27 s | 14–15 MB | 0 / 0 / 0 |
| PQS TW | 260–269 | 2 / 2 | 29–36 s | 14 MB | 0 / 0 / 0 |

- Pass 1 (69 domains, 2 at once) = 6.5–21 s, ~210–260 sockets (every IP pinged once + 1 TLS per domain);
  a re-sample pass of 10 domains one at a time = 25–45 sockets, 0.1–4 s; + 2 × 5 s gaps.
- Before / after on NexKR (10 built-ins): fork.121 way (FIN close) → TIME_WAIT 0 → 12; new way (RST) 16 → 16
  (+0); pin full check + liveness (www.amd.com, 2 sockets, max 1 open, 5 ms) +0.
- Whole probe ≤ 36 s ≪ 90 s → cap 2 kept. Per server per day: 1 scheduled 测速 ≈ 250–350 connections spread over
  20–40 s, 0 TIME_WAIT; pin: 1 connect per pinned IP / 10 min + ≤ 8 / 6 h, 0 TIME_WAIT.
- History size: measured 5.2–5.4 KB per landing_server row after 3 测速 (69 domains, preview with the real
  report data); full (64 domains × 8 samples) ≤ ~10 KB. One UPDATE per 测速, no extra rows.

## Pool report → threshold (full table: run dir `report/pool-table.md`)
52 pool domains per machine are 「试用」 (the rest = built-in vendor brands / auto-block kinds).

| Machine | Fastest non-试用 median | 可用 (stable + fast) | 不稳定 | 偏慢 (> +5 ms) | 不可用 |
|---|---|---|---|---|---|
| NexKR | 4 ms | 30 | 3 | 15 | 4 |
| Bestvm US | 6 ms | 26 | 7 | 15 | 4 |
| Boil Hinet 马年 | 12 ms | 12 | 15 | 21 | 4 |
| Dmit T1 | 3 ms | 32 | 3 | 13 | 4 |
| Boil Hinet 158 | 10 ms | 7 | 21 | 20 | 4 |
| PQS TW | 10 ms | 7 | 16 | 23 | 6 |

- Threshold kept at **max(10 ms, 30 % of median)**: 8 / 10 / 15 ms change only 0–2 sites per machine (truly unstable
  sites jitter 30–220 ms); 10 ms is the largest value that still rejects the 227 counter-example
  (portal.citygrainla.com on Bestvm US: 4 / 16 / 5 ms, jitter 12 ms). Margin 5 ms over the fastest non-试用 domain.
- Today's prod picks pass the rule on their machine: Bestvm US ftp.debian.org (6 / 7 / 6 ms) and PQS TW
  www.airbnb.com (10 / 14 / 10 ms) = 可用 (existing nodes keep their dest anyway).

## Tasks
- [x] Plan doc + branch
- [x] Agent: linger-0 close on every probe / ping / pin socket, ≤ 2 sockets open at once per machine, per-request
      `concurrency` (1 = one domain at a time), landing_reality 2; unit tests (639fe75d)
- [x] One-time pool small-site report (6 machines, 3 runs ≥ 1 h apart) → threshold max(10 ms, 30 %) kept
- [x] Panel: pass 1 + 2 re-sample passes (top 5 + ≤ 2 试用 contenders, ≥ 5 s apart), worst-of-3 ranking (077cba0e)
- [x] Panel: per-server sample history (rolling), 试用 stability rule, reasons (077cba0e, 658b34f1 wording)
- [x] UI: 次数 / 抖动 columns, 「试用」 tag, help text; screens phone 390 + desktop 1440 (27923570;
      `/root/mmwx-preview/reality-probe-stable/preview-*.png`, real report data as history)
- [x] Tests: go test + contract SQLite + PostgreSQL, agent tests, tsc / build / lint (see 「Tests」)
- [x] TIME_WAIT proof (lab unit test `TestProbeLeavesNoTimeWait` + NexKR before/after + 18 fleet runs), resource numbers
- [ ] Push, CI green, ledger row ready, final.md

## Tests (r2, 11-07 TW)
- Backend `go test ./...` with `FLVX_POSTGRES_TEST_DSN` (SQLite + PostgreSQL): 22 packages ok; handler / repo /
  contract need > 10 min each on the dev box → re-run with `-timeout 90m`: handler ok (905 s), repo ok (1804 s),
  contract ok (295 s). The contract run caught a real miss: `TestLandingUserServerViewContract` pins the admin
  field set → added `realityProbeStats` / `realityTrialRule` (admin only; also in the user-forbidden list).
- PG test DB `flvx-linechain-pg` was slow because 35 leftover schemas of killed / timed-out runs (10-25 → today,
  ~13 k relations) made every AutoMigrate catalog lookup slow → dropped (list in run dir `pg-drop.txt`); contract
  went from > 10 min to 5 min.
- Agent `go-gost/x` landing + socket ok (needs a short TMPDIR: unix socket path ≤ 108 chars), vet ok, `go-gost`
  build ok. Frontend eslint (changed files) / tsc / `npm run build` ok.

## Screens (`/root/mmwx-preview/reality-probe-stable/`)
- `preview-{phone,desktop}-1-global-list.png`, `-2-global-pool.png` — 全局 Reality 域名 list, 妙妙屋池 header
  「81 个（22 个已屏蔽，52 个试用）」 + the 试用 rule text.
- `preview-{phone,desktop}-3/4-server-{NexKR,Bestvm_US,Boil_Hinet_158}*.png` — server 测速 table (次数 · 抖动,
  试用 · 稳定 / 试用 + reason, worst-of-3 order).
- `preview-{phone,desktop}-6-help-{NexKR,Boil_Hinet_158}.png` — pacing help text + last cost (native: 257 个连接,
  最多同时 2 个, 19 秒; shared: 由 Helper 控制).
