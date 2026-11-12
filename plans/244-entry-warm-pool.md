# 244 — Entry warm pool (预热池) for 线路机

User 2026-11-11: give every FLVX rule's entry machine (线路机) the LeiKwan GZ-front style warm pool
(pre-opened upstream connections) so the first bytes of a new stream skip the dial.

- Step 1 (done, report only): measure per real line how much warm saves vs today's cold dial and
  whether idle pooled conns survive. Prod read-only, nothing built or changed.
- Step 2: build (user 11-12: switch per tunnel, default ON, follows the rules). Step 3 (A/B) dropped by the user.

Run dir (raw jsonl, scripts, full tables): `/root/flvx-workers/runs/entry-warm-measure/`
(`warmprobe.py` = stdlib probe, `orchestrate.py` = scheduler, `analyze.py` → `results.md/json`).

## Checklist

- [x] Step 1: measurement (report only, prod read-only) — 2026-11-11 23:42 → 11-12 00:48 TW
  - [x] map active tunnels → entry / middle / exit nodes (panel DB copy, gost.json read-only)
  - [x] passive burst sampling on 9 entries (12 min, 23:42–23:58 TW)
  - [x] type 2 cold vs warm through the real relay: 17 entry→next-hop pairs + 8 middle→exit pairs
  - [x] type 1 cold TCP connect to 31 rule targets
  - [x] idle lifetime: exit/middle relay, 25 type 1 targets
  - [x] cleanup of /root/flvx-warmtest-1111 on all 11 hosts (verified: no dir, no process)
  - [x] results + recommendation + step-2 design notes in this doc
- [ ] Step 2: build (user decisions 11-12: switch per tunnel, **default ON**, pool only while the
      tunnel has ≥ 1 active rule, whole path incl. middles, port forwards per rule, speed tag; A/B dropped)
      — BUILD ONLY + preview gallery, release after the user approves the screens
  - [x] design decisions written down (§ Step 2 build below)
  - [x] agent `warmpool` package: per owner (chain / rule service) × next-hop addr pools, size 2,
        agent cap 64, age 90–110 s, idle watcher per conn (peer close seen at once), refill
        concurrency 1 per key, dial backoff, rest 60 s after 3 dead in a row, young-close (< 40 s, three generations)
        detection → 「目标不支持预热」 30 min, counters only
  - [x] agent: type 2 hook in `chain/route.go` (pooled TLS conn → relay Connect; dead conn →
        cold redial in the same stream, no node fail mark), Bind stays cold
  - [x] agent: type 1 hook in `handler/forward/local` (pooled TCP conn per rule target)
  - [x] agent: handshake `warmPool=1`, command `SetWarmPools` (full state per node, hot), report
        `warm_pool` once per 60 s on the existing metric message
  - [x] agent: relay handler — conn closed before any request byte is not an error (no log line)
  - [x] agent: FLVX landing core `handshake` 4 → 120 s (+ one-time core restart only at the OTA)
  - [x] panel: `tunnel.warm_pool` (NOT NULL DEFAULT 1 → existing rows 1, new rows 1), create /
        update / light toggle endpoint, admin only
  - [x] panel: relay services `readTimeout: 130s` (type 2)
  - [x] panel: warm reconcile → `SetWarmPools` per capable node (rule create/delete/pause/resume/
        expiry, tunnel save/toggle/delete, node reconnect, 30 s safety pass)
  - [x] panel: stats — 1 h in-memory window, per hop saved = cold avg − warm extra avg, ≥ 10 warm
        streams, tunnel value over the current path; `warmPoolState` on admin tunnel list
  - [x] frontend: 「预热池」 switch in tunnel dialog; tag 预热 −N ms / +N ms / 目标不支持预热 +
        detail (desktop list + card/H5, light + dark)
  - [ ] tests: agent unit, panel unit/contract, old-agent compatibility, go test ./... both modules
  - [ ] local E2E lab (netns: panel + entry + middle + exit + target), resource numbers
  - [ ] preview gallery :8791 /entry-warm-pool/ (desktop 1366×900 + mobile 390×844, light + dark)
  - [ ] summary + release notes (DB migration, panel, OTA list, restarts, rollback)
- ~~Step 3: live A/B~~ — dropped by the user 11-12 (the speed tag replaces it)

## Method

- Probe = python3 stdlib on each host, dials exactly like the agent: TCP + TLS 1.3 (no verify, no
  SNI for IP next hops) to the next hop's relay port, then go-gost relay v1 CONNECT (tunnel credential
  read on the host from `/etc/flvxx/gost.json`, never printed) to an HTTP check site, `GET
  /generate_204`, first response byte.
  - **cold** = t(first byte) − t(start of TCP connect).
  - **warm1 / warm10** = conn TCP+TLS'd 1 s / 10 s earlier, idle; t(first byte) − t(relay request
    sent) — i.e. "client request arrives" → first byte.
  - Modes shuffled inside each block; ≥ 4 s between requests through one exit to one site; per
    (exit, site) lock across hosts, 4 sites rotated (cp.cloudflare.com, www.gstatic.com,
    connectivitycheck.gstatic.com, www.google.com). 30 blocks on primary pairs, 15 on others.
- Middles: the same probe run on the middle node with the middle's own chain credential → per-hop
  cost middle → exit.
- Type 1: cold getaddrinfo + TCP connect to each rule target, round-robin, ≥ 5 s per target, 20–30
  rounds; SSH (:22) targets skipped (fail2ban risk for the entry IP). No inner request driven (the
  targets are SS2022/Reality/user servers; stdlib has no AEAD), so the type-1 saving = measured TCP
  connect (+ DNS, cached by the local dnsmasq).
- Idle: 1 conn per target, no bytes, until the peer closes (cap 310 s); ≤ 3 conns per target.
- Burst: `/proc/net/tcp{,6}` polled at 5 Hz for 12 min, new ESTABLISHED/SYN_SENT conns to each
  next hop / rule target per 1 s window (conns < 200 ms can be missed).
- Load: ≤ 2 probe conns per relay job at a time, peak extra conns per host ≈ 4–14 (incl. inbound on
  shared exits Datawave HK 2 / Bestvm HK 年付 / Bestvm JP B), AWS HK ≤ ~8. 0 errors in 1,000+ relay
  rounds. Nobrand hosts untouched; Dmit Echo read-only.

## Results

### Type 2 (隧道转发), entry → first hop, ms (p50 / p90 / p99)

Saving ≈ 2 × hop RTT (TCP 1 RTT + TLS 1.3 1 RTT). warm1 and warm10 are identical (within the 15 s
exit limit), so pool age < 15 s costs nothing today.

| Entry | Tunnel → first hop (→ exit) | RTT | cold | warm | saved p50 / p90 | window TW |
|---|---|---|---|---|---|---|
| Mkcloud 深港ixp 1 | 4 → AWS HK (TW exit, primary) | 3 | 23 / 32 / 69 | 16 / 21 / 31 | 7 / 11 | 00:00–00:09 |
| Mkcloud 深港ixp 1 | 4 → Datawave HK 2 | 10 | 60 / 94 / 111 | 21 / 45 / 104 | 40 / 49 | 00:24–00:29 |
| Mkcloud 深港ixp 1 | 3 → Datawave HK 2 (primary) | 7 | 36 / 67 / 94 | 21 / 44 / 102 | 16 / 23 | 00:10–00:19 |
| Mkcloud 深港ixp 1 | 3 → Bestvm HK 年付 | 5 | 36 / 52 / 74 | 23 / 34 / 44 | 14 / 18 | 00:20–00:24 |
| Mkcloud 沪日ixp 440 | 25 → Bestvm JP B | 26 | 113 / 116 / 141 | 60 / 63 / 68 | 53 / 53 | 00:00–00:10 |
| Mkcloud 沪日ixp 440 | 28 → Bestvm JP B (→ Datawave HK 2) | 26 | 289 / 290 / 294 | 235 / 238 / 251 | 54 / 52 | 00:10–00:19 |
| Mkcloud 沪港ixp 新春版 | 61 → AWS HK (primary) | 21 | 90 / 96 / 105 | 47 / 52 / 214 | 43 / 44 | 00:00–00:09 |
| Mkcloud 沪港ixp 新春版 | 61 → Datawave HK 2 | 26 | 101 / 141 / 176 | 52 / 70 / 84 | 49 / 71 | 00:19–00:24 |
| Mkcloud 沪港ixp 新春版 | 64 → Bestvm HK 年付 (→ Bestvm JP B) | 24 | 284 / 310 / 376 | 233 / 249 / 275 | 51 / 61 | 00:09–00:19 |
| Mkcloud 沪港ixp 新春版 | 64 → Datawave HK 2 (→ Bestvm JP B) | 26 | 287 / 332 / 374 | 234 / 250 / 261 | 53 / 81 | 00:24–00:29 |
| Dmit Nathan | 74 → Datawave HK 2 (primary) | 2 | 19 / 22 / 37 | 13 / 17 / 19 | 7 / 5 | 00:00–00:09 |
| Dmit Nathan | 74 → AWS HK | 1 | 10 / 12 / 13 | 8 / 10 / 13 | 3 / 2 | 00:09–00:14 |
| Dmit Nathan | 106 → Bestvm HK 年付 | 2 | 20 / 22 / 23 | 13 / 17 / 17 | 7 / 5 | 00:14–00:18 |
| Dmit Nathan | 107 → Bestvm HK 年付 (→ Bestvm TW) | 3 | 77 / 233 / 304 | 71 / 114 / 301 | 6 / — | 00:18–00:23 |
| Dmit Nathan | 109 → Bestvm HK 年付 (→ Bestvm JP B) | 2 | 199 / 202 / 202 | 193 / 195 / 228 | 6 / 7 | 00:23–00:28 |
| Dmit Nathan | 110 → Bestvm HK 年付 (→ Bestvm sg 年付) | 3 | 150 / 157 / 164 | 145 / 153 / 158 | 5 / 3 | 00:28–00:33 |
| Dmit Nathan | 108 → Bestvm HK 年付 (→ Bestvm US) | 2 | 622 / 655 / 657 | 615 / 650 / 652 | 7 / 5 | 00:33–00:38 |
| Dmit Nathan | 111 → Bestvm HK 年付 (→ JP B → US) | 2 | 605 / 611 / 621 | 600 / 607 / 613 | 6 / 3 | 00:38–00:43 |
| Dmit Nathan | 112 → Datawave HK 2 | 2 | 18 / 21 / 23 | 11 / 14 / 16 | 8 / 7 | 00:43–00:48 |
| Dmit Echo | 116 → Bestvm US | 1 | 21 / 24 / 30 | 17 / 26 / 43 | 4 / — | 00:00–00:09 |

### Type 2, middle → exit (what a middle-side pool would add), ms

| Middle | Tunnel → exit | RTT | cold | warm | saved p50 |
|---|---|---|---|---|---|
| Bestvm JP B | 28 → Datawave HK 2 | 45 | 182 / 184 / 185 | 92 / 93 / 96 | 90 |
| Bestvm JP B | 111 → Bestvm US | 99 | 406 / 420 / 425 | 205 / 224 / 230 | 201 |
| Datawave HK 2 | 64 → Bestvm JP B | 45 | 183 / 184 / 191 | 93 / 94 / 95 | 90 |
| Datawave HK 2 | 107 → Bestvm TW | 14 | 67 / 307 / 974 | 37 / 257 / 1375 | 31 (TW site tails both modes) |
| Bestvm HK 年付 | 109 → Bestvm JP B | 45 | 184 / 185 / 189 | 93 / 95 / 96 | 91 |
| Bestvm HK 年付 | 110 → Bestvm sg 年付 | 32 | 139 / 142 / 146 | 74 / 80 / 89 | 65 |
| Bestvm HK 年付 | 108 → Bestvm US | 149 | 607 / 626 / 633 | 305 / 318 / 321 | 302 |

So on multi-hop lines the middle hop dominates: 沪日ixp 440 t28 cold 289 → entry warm 235 → entry +
middle warm ≈ 145; 沪港ixp 新春版 t64 284 → 233 → ≈ 142; Dmit Nathan t108 (→US) 622 → 615 → ≈ 313.

### Type 1 (端口转发), entry → rule target TCP connect (= the most a pool can save), ms

| Entry | Tunnel | Targets | connect p50 | p99 / max | >1 s | idle lifetime |
|---|---|---|---|---|---|---|
| Mkcloud 深港ixp 1 | 105, 90 | user SG server; 264 备线 Bestvm JP B :60061; 279 备线 AWS HK :10022; 263 备线 99U :10017 | 2.8 / 51.5 / 2.9 / 14.4 | ≤ 55 | 0 | ≥ 310 s / 120 s / 5 s / ≥ 310 s |
| Mkcloud 沪日ixp 440 | 39 | 8 user JP servers (2 refuse = dead, 0-traffic rules) | 26–33 | ≤ 54 | 0 | busiest 109.107.140.78:8080 **4 s**; 219.62.10.238 300 s; others ≥ 310 s |
| Mkcloud 沪港ixp 新春版 | 57 | jp.okonfu.com:6666 | 119 | 136 | 0 | ≥ 310 s |
| vmsilo 沪日首发 | 101 | 4 user servers (lindays) | 27–34 | ≤ 41 | 0 | 60 s (RST) |
| Leikwanhost 沪台IPLC | 75, 78, 79 | PQS TW :10015/:30001, Boil Hinet 158 :10018/:10021/:30001, Boil Hinet 马年 :10016 | 30–33 | 761 / 1046 | 2 / 180 | mmwx-official 5 s, FLVX landing 4 s, 马年 :10016 120 s |
| Dmit Nathan | 114 (0 rules) | Boil Hinet 158 Reality :30000 (panel landing) | 25 | 28 | 0 | 7.3 s |
| Dmit Echo | 115 (0 rules) | Bestvm US (relay port, no landing inbound active) / NexKR landing :30001 | 1.0 / 140 | 1.1 / 141 | 0 | — / 4.1 s |
| AWS HK | 88 | Boil Hinet 158 / 马年 (4 targets) | 18–19 | ≤ 31 | 0 | 5 s |
| Datawave HK 2 | 91 | Boil Hinet 158 :10021 | 23.5 | 843 / 1047 | 1 / 21 | 5 s |

### Idle lifetime of a pre-opened conn (no bytes)

| Peer | Lifetime | Closed by |
|---|---|---|
| Every exit / middle relay (AWS HK, Datawave HK 2, Bestvm HK 年付, Bestvm JP B, Bestvm US) after TLS | **15.0 s** | EOF — relay handler `readTimeout` default 15 s |
| Same, plain TCP without TLS | 10.0 s | EOF — TLS listener handshake deadline |
| mmwx-official inbounds (Boil Hinet 158, PQS TW) and AWS HK :10022 (279 备线) | 5.0 s | RST — Xray `policy handshake: 5` |
| FLVX landing core (SS2022 :30001 on shared/native servers, NexKR) | 4.0 s | RST — `go-gost/x/landing/types.go` `handshake: 4` |
| FLVX landing Reality (Boil Hinet 158 :30000) | 7.3 s | EOF |
| Bestvm JP B :60061 (264 备线), Boil Hinet 马年 :10016 | 120 s | RST |
| 99U lkbk-core :10017, most third-party user servers | ≥ 300 s | — |
| vmsilo 沪日首发 user targets | 60 s | RST |
| busiest 沪日ixp 440 rule target 109.107.140.78:8080 | 4 s | EOF |

### Burst need (new streams per 1 s, 12 min late peak 23:42–23:58 TW)

Almost every type 2 path: ≤ 25 new streams in 12 min, busiest second 1–2. Busiest type 1 targets:
沪日ixp 440 → 109.107.140.78:8080 835 streams, p99 8 / max 13 per s; vmsilo 沪日首发 → dmjptokyo
122, p99 5.4 / max 6; Leikwanhost 沪台IPLC → PQS TW :30001 p99 4. → pool size 2 per (tunnel, first
node) covers type 2; 4–8 only for the busy type 1 targets (which can't be pooled anyway, see idle).

## Recommendation

| Line | Verdict |
|---|---|
| Mkcloud 沪日ixp 440 t25 / t28 | **Warm (type 2)**: −53 ms p50 at the entry; t28 also middle Bestvm JP B → HK −90 ms. Pool 2. |
| Mkcloud 沪港ixp 新春版 t61 / t64 | **Warm**: −43…−53 ms entry; t64 middle (Bestvm HK 年付 / Datawave HK 2 → JP B) −90 ms. Pool 2. |
| Mkcloud 深港ixp 1 t3 / t4 | Marginal: −7 ms on the primary AWS HK (−40 only on the Datawave fallback), t3 −14…−16. Low priority. |
| Dmit Nathan t74 / t106 / t112 | Not worth (entry hop RTT 1–3 ms → −3…−8 ms). |
| Dmit Nathan t107–t111 (0 rules today) | Entry hop not worth; **middle pools** worth if rules appear (Bestvm HK 年付 → US −302, → JP −91, → SG −65; JP B → US −201). |
| Dmit Echo t116 / t115 (0 rules) | Not worth (Echo ↔ Bestvm US 1 ms RTT). |
| Type 1 lines (沪日ixp 440 t39, 沪港 t57, 深港 t105/t90, vmsilo t101, 沪台 t75/78/79, AWS HK t88, Datawave HK 2 t91) | **Skip for now**: saving = 1 RTT (18–33 ms typ.), and our/mmwx landings drop idle conns in 4–5 s; third-party targets vary (4 s…≥ 300 s) and are not ours to tune. Only revisit for own landings after handshake ≥ 120 s, or for 沪台IPLC where 1 s SYN tails exist (2/180). |

Preconditions for type 2: (1) panel-only change — relay handler `readTimeout` 130 s on pooled
tunnels (exit + middle services), no exit OTA; (2) agent pool code → OTA of the chosen entries and
middles only. Without (1) the pool must recycle every ≤ 12 s (≈ 5 TLS handshakes/min per pooled
conn) — not recommended.

**Honest big picture.** Warm only trims connection setup of a new stream: −50 ms on the Shanghai
entries to HK/JP, −90 ms more on HK↔JP middles, ~0 on Dmit Nathan/Echo. Like LeiKwan, a real page's
p90 is dominated by the target site and the client's own leg; browsers reuse conns, so the effect
is mostly "first click after idle" and the first conns of a page. No SYN-loss tails were seen on the
type 2 legs in this window (cold max ≤ 2× p50 except site tails behind Bestvm TW), so the tail benefit
here is smaller than at LeiKwan; 沪台IPLC type 1 legs did show 1 s SYN retransmits (2/180).

## Step 2 design notes (from what step 1 showed; notes only, no code)

**Where it fits (go-gost/x).** Type 2 entries/middles dial in `chain/route.go` `chainRoute.connect()`:
`Transport.Dial(addr)` + `Transport.Handshake()` (TCP + TLS) for hop 0, then `Transport.Connect()`
(relay CONNECT with the tunnel credential; with `nodelay: true` the connector writes the request and
waits for the exit's response, which the exit sends only after it has dialed the target).
A pool belongs exactly between Dial+Handshake and Connect: it hands out an already TLS-handshaked
conn; `Connect()` (credential + target) still runs per stream, so pooled conns carry no target and
no credential and need no protocol change on the exit.

**Pool key = (from node, next-hop addr:port, dialer type, interface).** In FLVX every tunnel has its
own relay port on each next hop, so in practice the key is per (tunnel, next-hop node); there is
nothing to share across tunnels except between tunnels that share a port (none today). Keep the pool
per hop node in the chain hop's selector order: warm only the node the panel put first (最优 =
fifo in panel order, plan 165) — optionally 1 conn for the second node; when the panel re-orders
(最优 switch) the pool follows the new first node and the old pool drains by age.

**Middles too.** Middles dial their next hop the same way (their service handler has `chain`), so the
same code warms the middle→exit hop. On multi-hop lines (沪港ixp 新春版 → Bestvm HK 年付 / Datawave
HK 2 → Bestvm JP B; Dmit Nathan 107–111) the middle hop is the expensive one (HK↔JP 45 ms RTT →
~90 ms saved) while the entry hop is cheap (Dmit Nathan → HK ~2 ms RTT → ~6 ms).

**Idle lifetime / exit change.** Every exit and middle relay closes a TLS conn that sent no relay
request after exactly 15.0 s (handler `readTimeout` default, go-gost/x/handler/relay/metadata.go);
plain TCP without TLS is cut at 10 s (silentTLSListener handshake deadline). `readTimeout` is already
read from handler metadata, so the fix is **panel-only**: add `"readTimeout": "130s"` to the relay
handler metadata in `buildTunnelChainServiceConfig` (go-backend mutations.go) for tunnels with the pool
enabled → services re-push, no agent OTA on exits. The pool then uses conns ≤ 120 s old (LeiKwan
lesson: replace well before the peer deadline, spread replacement 90–110 s, test-before-use is not
possible on an idle TLS conn so rely on age). Without that change the pool must recycle every ≤ 12 s
(≈ 5 handshakes/min per pooled conn) — too much churn, not recommended.

**Type 1 (端口转发).** The entry's tcp handler dials the rule target directly
(`handler/forward/local` → `Router.Dial` → `DefaultRoute`). A raw-TCP pool there only saves 1 RTT
and only works when the target keeps an idle no-byte conn: measured mmwx-official inbounds 5 s
(Xray `policy handshake: 5`), FLVX landing core 4 s (`go-gost/x/landing/types.go` policy
`handshake: 4`), Reality 7 s, the busiest 沪日ixp 440 target (109.107.140.78:8080) 4 s; only some
third-party user servers keep ≥ 300 s. So: type 1 pool only per target with a measured lifetime
≥ 120 s, or after our own landings move to `handshake ≥ 120` (FLVX landing core = agent change on
landing nodes → OTA; mmwx shared servers = mmwx Custom/guard like LeiKwan 99U). Raising handshake
weakens slow-client protection on the landing; prefer to do it only on landings that are a rule
target of a warm entry.

**Relay auth (plan 225).** Unaffected: credentials are in the per-stream relay request (`Connect`),
not in TLS. The exit target guard runs per CONNECT as today.

**nodelay.** Keep `nodelay: true`. The saving measured is TCP+TLS only; the relay request/response
round trip (which includes the exit's dial to the target) stays. A further step (not proposed) would
be exit-side target pools; not worth it — exit→site dials are short.

**Counting / limits.** Pooled conns are outbound on the entry and inbound on the next hop before any
relay request:
- entry service `maxConnections` / `groupMaxConnections` / per-user IP limits count inbound client
  conns of the rule service → pool not counted (good, nothing to do);
- 整机连接数 (machine protection, `machineprotect`: every /proc/net row) and node `tcp_conns` metric
  DO see pooled conns on both ends → keep pools tiny (≤ 2–4 per path) and document the offset;
- exit relay services have no conn limit metadata today; the exit relay handler logs a `readTimeout`
  / EOF error per expired idle conn, but handler errors are aggregated (`recordHandlerError`, plan
  167) → no log spam, still worth an explicit "pool conn closed unused" quiet path;
- landing 入站连接数 / per-landing-node `max_connections` / online-IP limits and landing scan detection
  only matter for a type 1 pool toward our own landings (each pooled conn is an inbound landing conn
  from the entry IP; scan detection watches the landing's outbound targets, not affected). If a
  type 1 pool is ever built toward landings, the landing must not count unauthenticated pre-handshake
  conns — today Xray counts them only after the handshake, verify in step 2.

**Failure handling.** A dead pooled conn (EOF on first write/read) → discard and dial cold at once
in the same stream (no user-visible error); never mark the hop node failed (`maxFails 1` /
`failTimeout 60 s`) because of a pooled conn; flush and rest the pool for 60 s after N dead conns in
a row (LeiKwan `rest_s=60`). Refill in the background after each take, bounded concurrency 1 per key.

**Switch.** Per tunnel, default off (`tunnel.warm_pool` = 0/N); panel pushes pool size in the chain
hop metadata; agent ignores unknown keys on old versions. Size from burst data: see table.

## Step 2 build (2026-11-12) — decisions

**One command, hot.** The panel sends each warm-capable agent (handshake `warmPool=1`) its full
desired state with `SetWarmPools` — `{"chains": {"chains_<tunnel>": 2}, "forwards": {"<rule id>": 2}}` —
whenever it changes (rule create / update / delete / pause / resume / expiry / flow pause, tunnel
save / switch / delete; debounced 1.5 s), after every agent (re)connect, and in a 30 s safety pass that
pushes only differences. Rule services and chains are **not** changed by the pool at all, so the switch
and rule changes never re-create a service (no listener gap, no dropped connection) and the reconnect
diff (plan 138) is untouched. Old agents never receive the command (no capability). The brief's
"carried with the rule's service push" became "pushed right after it" (≈ 2 s): the rule service config
stays byte-identical, which is what keeps everything hot.
- 隧道转发: chains of every entry that serves ≥ 1 active gost rule of the tunnel + all its middles (the
  whole path); exits have no chain. nftables / WG rules do not count (they do not dial through the chain).
- 端口转发: one pool per active rule on its entry (`<rule>_<user>_<userTunnel>_tcp` service, TCP only).
- Switch: `tunnel.warm_pool` NOT NULL DEFAULT 1 (AutoMigrate adds it → every existing tunnel on, new
  tunnels on); create/update accept `warmPool`; the dialog saves a switch-only change through the light
  `POST /api/v1/tunnel/warm-pool` (column write + reconcile, no redeploy).

**Relay `readTimeout: 130s` on every tunnel relay service** (middles + exits, type 2), independent of the
switch, so the switch never touches services. Existing metadata key → old agents honour it, no exit
OTA. It only affects conns that finished TCP+TLS but sent no relay request (cleared once the request is
read). At the release, the panel restart reconnects the agents and the reconnect diff re-pushes every
relay service once with `UpdateService` (re-create: established connections stay, ~ms listener gap —
same mechanism as every earlier relay config change).

**Agent pool** (`go-gost/x/warmpool`): key = (owner, next-hop addr); owner = chain or rule service. Per
owner the head (first node not fail-marked in the current order = the 最优 / fifo pick) gets 2 conns,
up to 2 other nodes that real streams used in the last 5 min get 1 each (round-robin, 规则最优
per-destination order); a chain update / 最优 reorder re-reads the head at once, old pools drain by age.
Age 90–110 s; agent cap 64 pooled conns (one Warn line per 10 min when hit); refill concurrency 1 per
key; dial failures back off 2 / 4 s then rest 60 → 120 → 240 → 300 s; 3 dead-in-use in a row → flush +
rest 60 s. Each idle conn has a watcher goroutine blocked in Read (no CPU, no traffic): a peer close is
seen at once; a peer that closes idle conns younger than 40 s three generations in a row (an exit restart
is one generation) → 「目标不支持预热」 for 30 min; a peer limit ≥ 40 s lowers that key's max age to ¾ of it
(min 30 s). Type 2: pooled conn → relay CONNECT under TCP_USER_TIMEOUT 3 s; EOF / reset / timeout → close,
dial cold in the same stream, no node fail mark; relay status errors are passed through (not redialed).
Type 1: pooled TCP conn handed to the forwarder (a byte the target sent first is kept). Pooled conns never
touch fail markers or 最优. No per-conn / per-stream logs. New agents: a relay conn closed before its
first request byte is not an error (no log line).

**Threshold 40 s instead of the brief's 20 s:** a peer that cuts idle conns at 20–40 s would force
replacements every 15–30 s per conn (4–8 dials / min / key) — not worth it; ≥ 40 s keeps it ≤ ~2.7.

**Native landings:** `go-gost/x/landing` policy `handshake` 4 → 120 s for all FLVX landing cores (the
policy is global per Core and read only at Core start; per-target values would need restarts on every
rule change). No hot path exists and the agent does **not** restart the Core: it takes effect at the
next Core start. Until then pools to those targets detect the 4 s limit and back off
(「目标不支持预热」, retried every 30 min, ≈ 6 dials / 30 min / key). A release can restart the Core
once on the landing nodes that are targets of warm 端口转发 rules (drops that landing's live connections
for a few seconds) — only at the OTA, decided at release time.

**Counting:** pooled conns are outbound on the dialing node and pre-request on the next hop → not counted
by rule / user connection limits, online-IP limits, landing 入站连接数 (counted per authenticated user)
or scan detection. The dialing agent subtracts its own idle pooled conns from its `/proc/net`-based
`tcp_conns` (整机连接数); they stay visible as a small fixed offset on the receiving side (next hop's
整机连接数 / `tcp_conns`, ≤ 2 per active tunnel hop / rule, ≤ 64 per dialing agent) and in the landing
port TCP counts (plan 211) for 端口转发 targets on our landings. LeiKwan pipe tunnels never warm (the
entry dials the local LeiKwan client, which has its own pool and LeiKwan's connection limit).

**Speed tag** (admin tunnel page): agents send counters per key on the existing 1 s metric message once
a minute (`warm_pool`, deltas); the panel keeps 12 × 5 min buckets in memory (restart = empty).
Per hop: saved = avg cold dial (refills + pool-miss streams) − avg extra delay of warm streams (≈ 0, or
the failed attempt + cold redial for a dead conn); needs ≥ 10 warm streams in the hour; tunnel value =
sum over the current path's hops (entry → middle → exit by the agents' current target), averaged over
entries by warm streams; 端口转发 = per rule target. Green 「预热 −N ms」, red 「预热 +N ms」 (+ 「常被对方
断开」 when ≥ 5 % of warm streams hit a dead conn), grey 「目标不支持预热」, nothing when off / no active
rule / not enough samples. Detail (tap / click): 用上预热 % · 冷连接 · 预热多等 · 近 1 小时, one line
per hop with node names. The 60 s path-latency probe and every 最优 UI are untouched.
