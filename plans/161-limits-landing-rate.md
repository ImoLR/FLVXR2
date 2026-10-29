# 161 — limits-landing-rate: per-user 速率 on own native landing servers + 「不限」 labels (BUILD ONLY)

Brief: coordinator task `limits-landing-rate` ("plan 160" in the brief — 160 is already taken by
`160-release-batch-1028.md`, so this plan is 161). Design decisions: `/root/flvx-workers/contracts/limits-unify-decisions.md`
section 「CORRECTION of the ~05:00 section」 (user 2026-10-29 ~05:20 TW): limits only at SERVER and USER level; every
server related to the user (entry AND landing, own native + shared) has the per-user 速率 in its row; one landing row = one
limit for ALL of that user's landing nodes on that server (up / down separately); the pinned Core has no bandwidth limit, so
the AGENT enforces it in the kernel (like the 妙妙屋 Helper does for shared servers). No Core change.

Branch `feature/3.0.27-limits-landing-rate` from `origin/feature/3.0.27-limits-speed` (159 → 158 → 157 → fork.94).
Build only: no tag, no release, no prod writes, no OTA. Release = the next worker in the chain.

## Facts (verified in code before the change)

- Native landing servers are rows of kind `node` (ref = the landing server's FLVX node) in `user_server_quota`;
  `repo.UserServerRateMbps(user, node)` (row override else `user.server_rate_mbps`) is already the user's 速率 there.
  Plan 159 applies it to the user's forward entries on that node only; the landing Core is not limited.
- Native landing nodes are SS2022 (TCP + UDP) or VLESS Reality Vision (TCP); the Core listens on 0.0.0.0 (dual stack).
  Chain nodes (plan 108) listen on 127.0.0.1 only (fed by our own tunnel exit) and are not public.
- The agent pushes the whole landing desired state (`ApplyLandingDesired`), keeps it in `desired.json`, reconciles every
  10 s and after every apply; the Core runs as its own unit and survives agent restarts.
- The agent already links `github.com/vishvananda/netlink` (tc) and `github.com/google/nftables`; no `tc` / `nft` binary is
  needed at run time.
- 妙妙屋 Helper (branch `feat/flvx-landing-user-limits`, shared servers): nftables named byte `limit` per owner and direction,
  `drop` over the rate; its own lab saw two downloads of one owner split 19.9 / 0.04 Mbps.

## Design

### Method choice (lab, numbers in Results)
Real Core 2bf03c9a in a netns lab (40 ms RTT), one user with two SS2022 nodes, measured: nftables policing (the Helper's
method) vs tc HTB class per user + fq_codel / fq leaf behind IFB devices. → see Results; chosen: **tc HTB + fq_codel**.

### Agent (go-gost/x/landing)
- Desired state (only sent to agents with the new capability): `user_rates {version: 1, groups: [{group:
  "landing-user-<id>", rate_mbps}]}` + per node `rate_group`. Unit = FLVX Mbps (131072 B/s), like every FLVX 速率.
- Kernel objects (all fixed names, so a restart / crash loop can never duplicate them):
  - IFB devices `flvxlr-up` (client → node, ingress) and `flvxlr-dn` (node → client, egress), root HTB `7f1a:`
    (unclassified = direct, unshaped), one class per user `7f1a:<slot·16>` (rate = ceil), fq_codel leaf per class.
  - On every L3 device with a global address (not lo / IFB): `clsact` + flower filters at fixed prefs, one per
    (port, tcp|udp, IPv4|IPv6, direction): `skbedit priority 7f1a:<slot·16>` + `mirred egress redirect` to the IFB.
    Only the user's landing node ports match; every other port / user / service is untouched.
- Hot apply: rate change = `class change` (no packet loss, no Core restart, no connection drop); node ports added /
  removed = filter add / delete; last node / rate / server gone = filters first, then IFB devices (a filter must never
  point at a missing or down IFB — mirred would drop the user's packets).
- Agent start: our leftover filters and IFBs are removed and rebuilt once; the 10 s tick checks devices / IFBs cheaply
  (two netlink dumps, only while rates are active) and does a full verify every 60 s; nothing per second.
- Capability: handshake `landingRate=1`; status reports `user_rate {state, error}` so a kernel without tc support shows
  an error on the row instead of a silent no-op.

### Panel (go-backend)
- `node.landing_rate` (add-only, default 0) from the handshake; `landing_server.rate_error` (add-only) from the agent status.
- `syncNativeLandingServerOnce`: capable agents get `user_rates` + `rate_group` for every owner with an effective rate > 0
  on the server's node; the content joins the revision signature (older agents: payload byte-identical).
- Rate changes: per-server row save (already syncs the user's landing servers), user 默认速率 change → sync the user's
  landing servers; node / server changes re-sync as today.

### UI (pages/landing/user-settings.tsx)
- Own native landing rows: 速率 input enabled; yellow 「agent 较旧（待升级）」 hint when a rate applies and the agent lacks
  the capability; yellow error hint when the agent reports the kernel could not apply it. Shared rows unchanged (plan 159).
- Rows where 速率 / 入站连接数 do not apply: grey 「不限」 + tap/click popover (plan 156 Dropdown) 「速率在入口服务器限」 /
  「连接数在入口/落地服务器限」. Help text updated (drop 「落地服务器的 Core 暂不支持按用户限速，显示「—」」).

## Checklist

- [x] Plan doc
- [x] Lab: nft policing vs tc HTB (fq_codel / fq) — fairness, accuracy, isolation, CPU at 20 / 100 / 500 Mbps → choice
- [x] Agent: desired-state types + validation, tc shaper (Linux) + stub, reconcile hooks, status, handshake capability
- [x] Agent tests: unit (render / diff / validation) + netns tests with the real kernel (objects, idempotent restart,
      no leaks, hot change, cleanup order)
- [x] Panel: model columns, capability persist, desired payload + signature, rate propagation, status → rate_error,
      related-servers flags; tests SQLite + PostgreSQL
- [x] UI: own-landing 速率 input, old-agent / error hints, 「不限」 + popovers, help text; tsc / build / lint
- [x] Found + fixed (plan 159): the dialog never sent rateOverride / rateMbps on save
- [x] E2E lab: real panel + real agent + real Core — 20 Mbps row, two nodes combined both directions, second user
      unaffected, live change / removal without drops, agent restart → one set of objects, old-agent path unchanged
- [x] Resource numbers: agent RSS / CPU idle + full rate before / after; kernel CPU at 20 / 100 / 500 Mbps
- [x] Full `go test ./...` SQLite + PostgreSQL; go-gost + go-gost/x touched packages
- [x] Screens 390 + 1440, light + dark → `/root/mmwx-preview/limits-landing-rate/`
- [x] Push branch, CI green
- [x] `runs/limits-landing-rate/release-notes.md`, summary.md / final.md

## Results

### Method choice (lab `runs/limits-landing-rate/lab/fair/`, real Core 2bf03c9a, 40 ms RTT, 8 tests per cell)
Two flows over two nodes of one user, both directions, plus 4 flows and a staggered start; FLVX Mbps (payload).

| method | rate | slowest/fastest flow, median (worst) | Jain per second | payload / rate |
|---|---|---|---|---|
| nftables policing (Helper's method) | 20 | 0.62 (0.55) | 0.94 | 0.99 |
| | 100 | 0.46 (0.24) | 0.88 | 0.99 |
| | 500 | 0.41 (0.10) | 0.83 | 0.99 |
| HTB + fq_codel (chosen) | 20 | 0.99 (0.98) | 1.00 | 0.94 |
| | 100 | 0.97 (0.96) | 1.00 | 0.94 |
| | 500 | 0.97 (0.95) | 1.00 | 0.92 |
| HTB + fq | 20–500 | 1.00 (0.98) | 1.00 | 0.94–0.95 |

Examples: nftables downloads at 500 split 450 / 46 (staggered 46 / 452), at 100 80 / 19, at 20 12.6 / 7.3; HTB +
fq_codel 227 / 236, 47 / 46, 9.4 / 9.4. Another user's download on the same device stayed unlimited in every cell
(650+ Mbps). Latency of a small request/response on the user's own third connection under full load: all three
≈ 41 ms p50 (idle 40.7 ms): fq queues it separately, the policer drops instead of queueing.
Chosen: HTB + fq_codel — fair like fq, CoDel keeps a multiplexed client connection's queue short (fq has no AQM and
would let one flow queue up to 100 GSO packets), bounded memory (`memory_limit` 4 MiB per user and direction, 128
flows). HTB counts whole frames, FLVX rates are payload: the class gets +6 % (payload ≈ 1.00 × rate at 20 / 100,
0.98 at 500).

CPU (whole lab machine, 8 vCPU, % of one core above idle, median of 3; both Cores + kernel included): at 20 Mbps
nft ≈ 0–4 %, HTB + fq_codel ≈ 9–10 %; at 100 Mbps nft 7–8 %, HTB 24 %; at 500 Mbps nft 24–29 %, HTB 41–44 %. Fair
shaping costs about +10 % of a core at 20 Mbps and +15–17 % at 100–500 Mbps (per-packet release by the shaper: ~5 400
NET_TX + ~3 900 tasklet softirqs/s at 100 Mbps; policing has no queue). Tried: a small-packet bypass (halved tasklets,
CPU unchanged → dropped), bigger HTB bursts (−2…5 points, less exact → default kept), fq instead of fq_codel (−6
points at 100 Mbps but the bulk flows' own RTT rose from 40 to 73 ms median / 144 ms max — bufferbloat — so fq_codel
stays). The agent process itself does nothing per packet (below). Only traffic of users WITH a rate on that server
passes the shaper; everything else keeps the normal path.

### Release behaviour (found in the lab, fixed)
Deleting a user's class/IFB at once when his rate goes dropped what was queued (a running download fell 40 → 23 and
needed ~10 s to recover). Now the class is first set unlimited (queue leaves at once, in order), filters and class are
removed on a later tick (≥ 2 s), the IFBs with the last class: no dip (40 → 45 → 53 → 65 → 84 → 109 → 141).

### Agent tests (go-gost/x/landing)
- Unit: validation, derived ports/slots (VLESS TCP only, SS2022 TCP+UDP, disabled / loopback nodes skipped, slot =
  user id or highest free), status wire, graceful-stop release, release does not wait for a long reconcile.
- Kernel (root, throw-away netns, real tc): device selection (lo / addressless / link-local / bridge port / VLAN on a
  shaped device skipped), 16 filters per device for 2 users / 4 (port, proto) pairs, classes + fq_codel leaves, steady
  state no-op, in-place rate change (leaf kept), port removal, release → cleanup, re-add during release, 5 restarts
  (crash loop) → exactly 2 IFBs and one filter set, someone deletes an IFB → rebuilt with no filter pointing at
  nothing, someone deletes our filters → found by the 60 s full check, a foreign filter + clsact and a foreign legacy
  ingress qdisc left untouched (error reported for that device only), nothing left after the last rate.
- Real Core 2bf03c9a (`TestUserRateNetnsE2E`, FLVX_LANDING_NETNS_E2E=1): user 7 two nodes at 20 → download 9.90 + 10.00
  = 19.9, upload 9.66 + 10.04 = 19.7; user 9 on the same Core and device 656 Mbps; 20 → 40 on a running connection
  (Apply 30 ms, no Core restart, connection kept), removal without a dip; 4 agent restarts → 2 IFBs / 32 filters, still
  19.99; graceful stop → nothing left.

### End-to-end lab (`runs/limits-landing-rate/lab/e2e/`, real panel + this agent + Core 2bf03c9a, 34/34 checks)
Row 20 saved through `/landing/user/save` (the dialog's body after the fix) → applied in 0.13 s; alice download
9.87 + 10.08 = 19.95, upload 10.11 + 9.87 = 19.98, IPv6 + IPv4 flows 9.97 + 9.87, SS2022 UDP down 19.57 / up 19.45
(offered 60); bob 631 Mbps; latency of a small request on alice's third connection under load 61.1 ms vs 60.8 idle.
Live 20 → 40 → removed on one connection: 19.8 → 40.4 → 45 … 141, never dropped; objects gone at the next tick
(11.6 s). User 默认速率 30 (row follows it) applied in 0.27 s → 29.2. Graceful agent stop → no objects; start →
rebuilt from desired.json 0.24 s after start; 3× kill -9 → one set; panel restart → same IFB indexes, no duplicates.
Older agent (plan-159 build): row flagged 「待升级」, panel stops sending user_rates (next sync, 37 s), no kernel
objects, alice unlimited, nothing breaks; back to the new agent → 30 again. Panel log: no sync failures.
Rollback: fork.94 panel boots on the migrated DB (both new columns present), node online, landing APIs fine.

### Resources
Agent (lab, 60 s samples): idle without rate 0.23 % CPU / 49.9 MB RSS, idle with a rate 0.23 % / 50.0 MB, while
shaping 20 Mbps 0.27 % / 50.8 MB; plan-159 agent 0.23 % / 47.6 MB. No new poller: the existing 10 s landing tick does
two netlink dumps (links, addresses) only while rates are active, a full kernel comparison every 60 s; servers
without rates make no kernel call at all. Kernel objects: 2 IFBs + 1 class and 1 fq_codel per rated user and
direction + 4 filters per (port, tcp|udp) per device (O(users), filters O(their ports)). Native payload +212 bytes
for 3 nodes with rates. Panel: 3 extra queries per native landing sync (minute loop + changes).

### Tests and CI
- go-backend `go test ./...`: SQLite 19/19 packages ok, PostgreSQL 19/19 ok (new: repo native rates / capability /
  row flags on both dialects; handler payload + signature + rate error + default-rate sync + reconnect sync).
- go-gost/x 19/19 ok (incl. the root-only kernel tests), go-gost 1/1 ok; `TestUserRateNetnsE2E` with the real Core
  (env-gated) passes.
- Frontend: tsc 0 errors, build ok, eslint 0 findings in the two touched files.
- Screens: `/root/mmwx-preview/limits-landing-rate/` — 20 shots (390 + 1440, light + dark), 44/44 checks, 0 page errors.
- CI run 40852036923 (frontend, backend, agent, PostgreSQL contract): all success.
