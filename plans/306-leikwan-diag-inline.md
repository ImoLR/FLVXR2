# 306 — 利群诊断：「端内」从 专线出口 → 落地 里拆出来

Branch `maintenance/3.0.27-fork.171-diag-inline` (from fork.170 `9cfde8b2`). Task `leikwan-diag-inline`,
coordinator 2fdace98, 2026-11-25 TW. BUILD + screenshots only; release fork.171 after the user OKs the
screenshots (`/root/flvx-workers/briefs/release-leikwan-diag-inline.md`).

## User request (11-25, voice)
In a forward row's 诊断 (web + App) 「专线出口 → 落地」 looks too high — it seems to contain the vendor's
internal delay. Show that part as its own 「端内」 label on the 专线 part; 专线出口 → 落地 = measured − 端内
= the real exit → landing delay. Sums must stay right. Item 2: rules sharing one landing showed the same
(wrong) 利群出口.

## What was measured (read-only, 2026-11-25 18:55–19:20 TW)
Per line: 全程 G = LeiKwan client UDP probe on 百度云广州 (status.json), 入口段 h = kernel rtt / TCP handshake
百度云广州 → the ingress in use, exit = the landing-side source of **our own test connection** (found on the
landing by its byte count while it ran), C = that connection's kernel minrtt on the landing, n = ICMP min
landing → exit IP (3 pings), W = real end-to-end round trip 百度云广州 ⇄ landing **through the line**: a
plain SS2022 client on 阿里广州 → 百度云广州 主入口 port → line → landing → `1.1.1.1:80`, 8–12 HTTP
keep-alive round trips per connection, minus 阿里广州 → 百度云广州 (TCP connect min) and landing → 1.1.1.1
(direct keep-alive min). Client validated against a local xray SS2022 server first (no bad handshakes on
the line). Temp files on 阿里广州 removed afterwards.

| line (fwd) | landing | G 全程 | h 入口段 | exit (this line) | C conn | n ping | C − n | W real | today G + C | new |
|---|---|---|---|---|---|---|---|---|---|---|
| 广港 (263) | 99U :30000 | 12–13 | 2.7 | 43.154.110.97 | 6.3–6.9 | 1.7 | 4.8 | 10.9–14.3 | 18.5–19.5 | **13.7–14.7** |
| 沪日 (264) | JP B :31004 | 60–62 | 29.4 | 210.171.236.72 | 27.6–27.9 | 1.7 | 26 | 59.2–62.1 | 88–90 | **62.7** |
| 广台 Hinet (280) | 马年 :12311 (shared) | 22–23 | 2.7 | 103.167.50.69 | 37.2–42.4 | 21.6 | 18.4 | 39.7–47.2 | 59–65 | **44.1** |
| 广台 Seednet (281) | Fachost :30000 (shared) | 23 | 2.7 | 210.61.104.139 | 24.0–24.4 | 7.3 | 16.9 | 28.9–33.0 | 47 | **30.3** |
| 福台 Hinet (282) | 马年 :12311 (shared) | 24–29 | 16.4 | 154.203.147.74 | 9.9–10.9 | 10.4 | ≈0 | 32.5–33.3 | 34–40 | **34.4–39.4** |
| 福台 Seednet (615) | Fachost :30000 (shared) | 25–39 | 16.4 | 202.160.77.49 | 10.6–10.9 | 10.9 | ≈0 | 35.2–37.8 | 36–50 | **35.9–49.9** |

(福台 G moved 24→29 and 25→39 within 15 min — the vendor's UDP probe is noisy; W did not move.)

Round 2 (~20:00–20:25 TW, exits had rotated again): the **prod fork.170 诊断** of the six rules (admin API,
read-only) vs **plan 306 on exactly those inputs** (lab replay + the landing's ping), and fresh real round trips:

| line | 全程 | 入口段 | 专线内 | 端内 (new) | 出口→落地 before / after | 到落地 before (prod) / after | W real |
|---|---|---|---|---|---|---|---|
| 广港 263 | 15 (tcp) | 3 | 12 | 4.6 | 8.1 / 3.5 | 23.1 / **18.5** | 15.2 |
| 沪日 264 | 63 | 31.9 | 31.1 | 27.9 | 29.7 / 1.5 | 92.7 / **64.5** | 63.2 |
| 广台 Hinet 280 (shared) | 22 | 3.8 | 18.2 | 0–16.2 | 22.4 / 6.5–9.5 (range) | 44.4 / **28.5–31.5** | 25.7 |
| 广台 Seednet 281 (shared) | 22 | 3.9 | 18.1 | ≈0 (only source) | 11.2 / 11.5 | 33.2 / **33.5** | 28.9–33.0 (round 1) |
| 福台 Hinet 282 (shared) | 35 | 24.4 | 10.6 | ≈0 (2 广台 sources dropped) | 22.4 (广台's exit!) / 9.5 | 57.4 / **44.5** | 28.2 |
| 福台 Seednet 615 (shared) | 27 | 22.3 | 4.7 | ≈0 (only source) | 11.2 / 11.5 | 38.2 / **38.5** | 35.2–37.8 (round 1) |

福台's after values stay a few ms high because its 全程 (the vendor's UDP probe) jumped to 35 / 27; the diagnosis
can only use the vendor's own number there (before was the same or worse). The exit-does-not-answer-ping run
(`FLVX_LEIKWAN_LAB_NOPING=1`) gives exactly the fork.170 numbers for all six rules.

### Finding — 全程 already contains 端内
* On 广港 / 沪日 / 广台 the landing's TCP is held by a vendor relay right behind the 专线入口 (in China) and
  carried inside the vendor to the exit IP, which is only a front (pings answer from the front, port 5438
  RSTs). So the landing's connection rtt C = 专线 + exit→landing: C − n ≈ 全程 − 入口段 (18.4 vs 19.8,
  16.9 vs 20.3, 26 vs 31.6). The vendor probe (全程) ends at the exit front.
* On 福台 the exit **is** the relay (port 5438 open, C ≈ ping) → C − n ≈ 0.
* So the brief's draft sum 「全程 + 端内 + 出口→落地」 (= today's 全程 + C) counts the 专线 twice: 沪日 88–90
  vs real 59–62, 广台 Hinet 59–65 vs 40–47. That double count is exactly why 「专线出口 → 落地」 looked too
  high.
* What fits all six lines (within ≈3 ms, except 福台's own noisy G): **W ≈ max(全程 + n, 入口段 + C)**, i.e.
  入口段 + 专线 + n with 专线 = max(全程 − 入口段, 端内) and **端内 = C − n is a part of the 专线**, not an
  extra on top.

### Display (decision)
* 「专线出口 → 落地」 = n (ping from the landing to the exit IP) — the real network delay.
* 「端内」 = C − n, shown as a tag 「含端内 18 ms」 on the 「利群专线入口 → 专线出口」 row and on the 专线内 arrow
  of the path strip. It is *contained* in the 专线 number (「含」), so nothing is added twice. < 3 ms (ping is
  whole ms, G is the vendor's rounded probe, h ±1) → users see nothing, admins a grey 「端内 ≈0」.
* 专线内 = max(全程 − 入口段, 端内) (the TCP's own measurement wins if it is longer than the vendor probe).
* 到落地 (端到端) = 入口段 + 专线内 + 出口→落地 = max(全程 + n, 入口段 + C).
* No agent OTA: n uses the existing `ServiceMonitorCheck` (type icmp, every agent has it; whole ms → +0.5),
  3 pings, one at a time per exit. Every exit seen (12 IPs on 4 landings) answers ICMP; TCP connect only
  works on relay-type exits (5438), so ICMP is the method.
* Fallbacks: exit does not answer ping → today's numbers (C as 出口→落地, 到落地 = 全程 + C) with the note
  「出口不回 ping，分不出端内」; landing offline / old agent / no exit connection → unchanged texts.

### Item 2 — shared landings (280 + 282 on 马年 :12311, 281 + 615 on Fachost :30000)
No robust per-line signal exists: the vendor rotates exits (11-24 103.98.16.130 / 154.206.93.x, 11-25
103.167.50.69 / 154.203.147.74 / 210.61.104.139 …), one exit IP can serve several lines (210.61.104.139
on both landings), the vendor API has no exit list, and a marker connection through the line would hit the
entry's switch triggers (zero_close / eof1s). So:
* Landing used by one 利群 rule → busiest source (as before) + its ping.
* Landing shared by several 利群 rules → ping up to 4 sources, drop sources whose 端内 is longer than this
  line's whole 专线 (C − n > 全程 − 入口段 + 5 ms — 端内 is part of the 专线, so such a source belongs to a
  longer line); one left (or all left agree within 3 ms) → that one; else **show the ambiguity**: a range
  (e.g. 出口→落地 5–22 ms, 端内 0–18, 到落地 28–44) and a note 「这台落地机还接了别的利群线路，分不清…」;
  admins also get one row per remaining source (①②③, IP · 连接 · 端内 · 条数). Today 282 resolves to
  154.203.147.74 (correct), 280 stays ambiguous (4 plausible sources).
* Exact fix = give each 利群 rule its own landing port (open point for the user; not done here).

## Tasks
- [x] Worktree + branch from fork.170 (`9cfde8b2`)
- [x] Live read-only measurement on the 6 lines (G, h, exit, C, n, W) + finding above
- [x] Backend: exit ping (ServiceMonitorCheck icmp), 端内 split, compose() sums, shared-landing ranges
      (`leikwan_diag.go`, repo `LeiKwanRulesOnLanding`)
- [x] Backend unit tests `leikwan_diag_endin_test.go` (广台-like 24 vs 6.5, 福台-like ≈ equal, 端内 longer than the
      probe, no ping, unknown command, shared: resolved / range / alone / none fits, pick rules); old tests unchanged
- [x] Frontend: 「含端内」 tags (row + path strip), ranges, landing / vendor texts (desktop + H5); tsc + eslint clean
- [x] Lab (`runs/leikwan-diag-inline/lab/`, replay hook `FLVX_LEIKWAN_LAB_LIVE` in `leikwan_lab_diag.go`, lab-only
      build tag): before (fork.170 + the same replay) / after / no-ping numbers via the real API
- [x] Screenshot gallery desktop / H5 390 / App WebView 360, admin + normal user (lab user 「demo」), 广台 Hinet +
      福台 Hinet (+ 沪日, 广港), before vs after → http://104.145.236.29:8791/leikwan-diag-inline/
- [x] `go test -timeout 40m ./...` (SQLite) all packages ok, no failures (handler 276 s, contract 229 s); go vet;
      `npm run build` (tsc + vite) + eslint clean; pushed 31a021ad, CI Build Check 45611590248 green (backend,
      agent, frontend, PostgreSQL contract)
- [x] Release brief `/root/flvx-workers/briefs/release-leikwan-diag-inline.md` (fork.171, panel only, no OTA, no DB
      change); ledger row 306 → 待发布 (screens)
- [x] Summary `/root/flvx-workers/runs/leikwan-diag-inline/summary.md`
- [ ] Release — user approved the round-2 screens (「可以发布」, 11-25 night); released as **fork.173** (fork.171/172
      taken by plan 307) — see 「Release (fork.173)」 below

## Open points (user decides)
* Give each 利群 rule its own landing port so the exit can always be told apart (today 280/282 and 281/615 share).
* ~~「端内 ≈0」 admin only~~ — round 2: 「端内 X ms」 for everyone, any value.
* Read-only side effects of the measurement: 6 prod 诊断 calls, a few pings / TCP handshakes per exit IP from the
  four landings, a dozen HTTP requests through each line (imolr's rules); temp files on 阿里广州 removed.

## Resource efficiency
On click only (no poller): +1 `LandingPeerRtt` (as before) + 3 ICMP echoes per exit source (1 source; ≤ 4 on
a shared landing), one at a time per source, 150 ms apart, 2 s timeout each, all inside the 40 s budget. No
new connections on the line, nothing kept on the nodes.

## Round 2 — 「端内」 wording (user 11-25 night, after the round-1 screens)
Task `leikwan-diag-inline-r2` (coordinator 2fdace98). Still BUILD + screens only, no tag / release / prod change.
User decisions (voice, paraphrased by the coordinator):
1. 「端内」 = the whole 利群专线入口 → 专线出口 segment (industry wording: the line's own entry → exit delay). Value =
   round-1 专线内 = max(全程 − 入口段, landing conn rtt − ping), clamp ≥ 0 (in practice 全程 − 入口段).
2. No 「估算」 / 「≈」 / 「含端内」 for it: a small tag 「端内 X ms」, just the number. The round-1 conn − ping 「含端内」 tag
   is gone from the UI (admin debug sub-text keeps the plain ping / 连接 numbers only).
3. Shown to every user (admin + normal) whatever the value: 「端内 0.4 ms」 (one decimal under 10 ms); no < 3 ms hiding,
   no grey 「端内 ≈0」.
4. Other rows keep their honest reason texts (landing offline / old agent / no exit connection / no 全程 → skipped),
   but no 「估算」 on a 端内 or 出口 → 落地 number. Path strip 「专线内」 → 「端内」 with the same number.
5. 到落地 = 入口段 + 端内 + 出口 → 落地 (unchanged from round 1).
6. Shared-landing exit pick unchanged (no port split this round); its UI text = one short plain line.

### Tasks (round 2)
- [x] Backend: 端内 tag from the 专线 value (all users, any value, 1 decimal < 10 ms), vendor row not 估算, texts
      without 估算 / 含端内, shared-landing one-liner; tag also when the exit does not answer ping
- [x] Frontend: no 估算 / ≈ on the 端内 row + path hop 「端内」, section title / desc / path note reworded,
      到落地 without ≈ when 出口 → 落地 is the ping
- [x] Unit tests: tag text, tiny values (0.4), normal user sees it, no 估算 / 含端内 in 端内 / 出口 → 落地 rows
- [x] go vet + go test ./... (SQLite) all ok (handler 269 s, contract 225 s); npm run build + eslint/prettier clean
- [x] Lab re-shoot: desktop / H5 390 / App 360 × admin / normal user × 广港 / 沪日 / 广台 Hinet / 福台 Hinet
      (+ 广台 / 福台 Seednet desktop admin, + no-ping fallback) → gallery (改前 kept)
- [x] Push 83edb77a, CI Build Check 45619880482 green (backend, agent, frontend, PostgreSQL contract);
      trial merge onto 3.0.27-fork.172 (prod since 11-25 20:46 TW) clean → release as fork.173
- [x] Release brief + summary `runs/leikwan-diag-inline-r2/summary.md`

### What changed (round 2)
* Backend `leikwan_diag.go`: the 利群专线入口 → 专线出口 row is 「端内」 = max(全程 − 入口段, C − n) (as round 1's 专线内),
  `success` instead of `estimate`, sub 「全程 X ms − 入口段 Y ms」; tag `endIn` = 「端内 X ms」 (amber) on the row and in
  the summary for **every** user and any value — `lkTagMs`: one decimal under 10 ms (「端内 0.4 ms」), whole above;
  also when the exit does not answer ping (then 出口 → 落地 = conn rtt as before). `summary.endInMs` = the 端内 (=
  `vendorMs`); new `summary.landingPing` (出口 → 落地 is the ping). Removed: the conn − ping 「含端内」 tag, the < 3 ms
  hiding and the admin grey 「端内 ≈0」; admin source lines keep 「ping X ms · 连接 Y ms」 only. Skips on that row say
  「…，算不出端内」 instead of 「估算不了」. Shared landing: range sub = 「这台落地机还接了别的利群线路，分不清是哪个出口，
  显示范围」 (admin: 「还接着「name」」), alone = 「…，不一定是这条线的出口」, dropped = 「排除了 N 个别的线路的来源」.
  Exit pick logic unchanged (C − n vs the line's 端内 + 5 ms).
* Frontend `diagnose-modal.tsx`: section 「🔗 利群专线入口 → 专线出口」 (no 「（估算）」), desc 「端内：利群专线自己的一段，
  全程（到专线出口）减入口段」; landing desc 「落地机上实测（ping 专线出口）」; path hop 「专线内 · 估算」 → 「端内」 with the
  same number (one decimal under 10 ms); 到落地 pill without 「≈」 when `landingPing` (kept only in the no-ping fallback);
  path note 「端内 = 全程减入口段，到落地 = 入口段 + 端内 + 出口 → 落地」. Unused round-1 `extra` prop removed.
* Lab replay (same 11-25 inputs), shown values: 广港 端内 12 / 出口→落地 3.5 / 到落地 18.5 (19); 沪日 31.1 (31) / 1.5 /
  64.5 (65); 广台 Hinet 18.2 (18) / 6.5–9.5 / 28.5–31.5 (29–32); 广台 Seednet 18.1 (18) / 11.5 / 33.5 (34); 福台 Hinet
  10.6 (11) / 9.5 / 44.5 (45); 福台 Seednet 4.7 / 11.5 / 38.5 (39). No-ping fallback: 端内 same, 出口→落地 = conn rtt,
  到落地 ≈ as fork.170.
* Lab: `/root/flvx-workers/runs/leikwan-diag-inline-r2/lab/` (copy of round 1's; demo user also owns 广港 + 沪日; sets
  d/h/a × admin/user; `chain.sh`; gallery keeps round 1's 改前 PNGs, adds the missing ones from a fork.170 re-run).
  Chrome was OOM-killed once by host memory pressure (another worker's docker build) → the App sets were re-shot.

## Release (fork.173)
User OK on the round-2 screens (11-25 night). fork.171 / fork.172 went to plan 307 → branch
`maintenance/3.0.27-fork.173-diag-inline` = fork.172 branch head d90a60a3 + merge of the 306 branch (caff19e9), no conflicts.
Panel only (no agent OTA, no DB change). Rollback = image swap back to fork.172.
- [x] Branch + merge onto fork.172 (43e83d5e)
- [x] go vet; `go test -timeout 40m ./...` (SQLite) all packages ok (handler 260 s, contract 222 s), incl.
      TestLeiKwanDiagEndIn / SharedLanding / PickExitFor / Diagnose / DiagnoseV2; `npm run build` ok
- [ ] Push, tag 3.0.27-fork.173, CI + Images green, 10 assets, Latest, gost sha256
- [ ] Prod backup `pre-fork173-<UTC>` + prune (keep 2)
- [ ] Wait for the plan 309 ops worker to finish, then swap backend + frontend, FLUX_VERSION
- [ ] Verify: health, nodes, tables unchanged, log 15 min, 6 × 诊断 via admin API, ICMP checks on landings, UI smoke
- [ ] Cleanup flvxx-306 worktree, /root/lk306-lab, /root/lk306-base; ledger, memory
