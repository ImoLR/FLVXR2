# 138 · Reconnect stability → 3.0.27-fork.79 (close reasons on both sides, diff-only reconnect redeploy) + the one all-node OTA

Approval: user 2026-10-23 ~18:30 TW (「可以把这个安排上吧」). Brief: `/root/flvx-workers/runs/reconnect-stability/brief.md`.
Numbering: plan 140 (landing rule presets) shipped as fork.78 (panel only), so this plan releases **fork.79** and
does the single all-node OTA (also delivers fork.77's agent log level + fork.75's GuardVersion 2).

Base: `origin/maintenance/3.0.27-fork.78-landing-rule-presets` (e5d4b1d0 = prod fork.78).
Branch: `maintenance/3.0.27-fork.79-reconnect-stability`. Worktree `/root/projects/flvxx/flvxx-reconnect-stability`.
Run dir: `/root/flvx-workers/runs/reconnect-stability` (tools/, tests/, ota/, summary.md).

Standing rules: no connection / CPU / memory / disk blow-ups on nodes; logs bounded; no UI change; no prod DB edits;
no install-script edits; no connection-mode change on any node (node 26 stays on Cloudflare); node 36 protection stays
OFF; never touch flux_agent / the old Flux panel / single-x domains / other stacks.

## Findings before the change (read-only, 10-23 22:40–00:10 TW)

- Node 45 (Fachost seednet, NAT VPS 192.168.11.211 → 123.204.3.9, CF colo NRT over IPv4, SIN/KIX over IPv6):
  121 agent WS closes since 10-22 23:00 TW (outside panel restarts), concentrated 11:00–22:00 TW, ~0 at night.
  The mmwx `mmw-agent` on the same node (different backend host, different CF zone) logs
  `read: connection reset by peer` on its CF WebSockets at the same seconds (75 of 121 within ±3 s; 144 resets total,
  all RST, both CF IPv4 addresses); `mmwxc-helper` HTTPS posts to CF also get RST / time out (incl. over IPv6).
  → the closes are TCP resets on the node ↔ Cloudflare path, not caused by FLVX agent/panel/cloudflared.
- Panel side: the old session ends ~0.1 s before the agent's new session is established, i.e. only when the new
  connection replaces it (`handleNode` closes the old conn) — Cloudflare never propagated a close for the old leg.
- Cross-node coincidences are rare (15 multi-node clusters in 24 h, mostly panel restarts); NRT-colo nodes 21/53
  have 35/18 closes, 42 (HKG) 15, 52 (LHR) 12, 48 (SJC) 11, 47 (HKG) 8, 20 (direct ws) 6.
- Every reconnect redeploys every tunnel/forward service on the node (DeleteService + AddService / UpdateService →
  listener restart), and the agent rebuilds its nftables manager (NewManager clears all FLVX DNAT rules + accounting)
  → nftables-mode forwards lose their DNAT for new connections until the redeploy ~10 s later.
- Agent logs only 「🔌 WebSocket连接已关闭」 (gorilla `IsUnexpectedCloseError` hides 1006/EOF/RST and every non-close error);
  panel logs only `GET /system-info -> 200 (<duration>)`.

### Cause (passive captures 10-23 23:07–00:00 TW, read-only)

- Node 45 SYN/FIN/RST headers toward the Cloudflare anycast IPs (tcpdump streamed over SSH, nothing written on the
  node): 20 incoming RSTs in 7 bursts (several connections of different programs reset within seconds). All are
  stateless resets (`R`, no ACK, win 0 = "connection unknown here"); 14 of 15 carry a TTL different from their own
  connection's SYN-ACK (e.g. SYN-ACK 44 → RST 51, 43 → 54) → the reset comes from another Cloudflare location than
  the one that holds the connection. SYN-ACK TTLs vary 43/44, 50/51, 53/54 (different paths/sites); the IPv6 trace
  colo flips HKG ↔ SIN ↔ NRT every 1–2 min (IPv4 NRT). Two long-lived SSH control sessions to the node (IPv4 through
  the provider's port forward, IPv6 direct through the same router) never broke.
- Panel side (`:6365` capture): for nodes 12 and 26 (both TPE, IPv6, two providers, reconnected 5 s apart at
  15:51 UTC) the backend closed the old cloudflared leg only when the replacement arrived — Cloudflare never closed
  it. Same signature as node 45.
- Verdict: the drops are the Taiwan ISPs' path into Cloudflare's anycast edge moving existing TCP connections to a
  location that does not know them (stateless RST). Not FLVX (agent, panel, cloudflared, keepalive or timeouts) —
  every long-lived Cloudflare connection on node 45 drops the same way (mmw-agent too). Nothing on our side can keep
  such a connection; what we can do is make a drop cheap (reconnect < 1 s, no redeploy, no listener restart, no nft
  wipe) and log it precisely.

### Lab (scripts/reconnect-lab, netns entry → hop → exit, proxy resets like node 45)

| case (fork.78 → fork.79) | before | after |
|---|---|---|
| entry reconnect: mutating commands | 5 (DeleteChains/AddChains/UpdateService/DeleteNft/AddNft) | 0 (1 × ListServices) |
| hop / exit reconnect: mutating commands | 4 / 2 | 0 / 0 |
| listener re-created (socket inode) | entry, hop, exit each time | never |
| UDP flow through the tunnel (1 pkt/100 ms) | relay session re-created (2 at target) | 1 session, 0 lost |
| new nftables connections after an entry reconnect | fail 4.5 s → ≥10.5 s (rules wiped) | all OK |
| panel restart | all 3 nodes' listeners re-created | 0 commands besides ListServices |
| 10 healthy-session reconnects | 50 mutating commands, 169 agent log lines | 0, 22 lines; RSS/fds flat |
| changed while offline (rule target) | — | exactly 1 UpdateService |
| deleted while offline (nft rule) | rule stays (orphan) | removed on reconnect (1 DeleteNftablesRules) |
| agent restart / old agent | full deploy | full deploy (unchanged behaviour) |

## Checklist

### Investigation (Part 1)
- [x] Panel log, cloudflared journal, node 45 journal (flvxx + mmw-agent + mmwxc-helper), per-node close counts, colos
- [x] Cross-node close timeline (21 42 45 47 48 52 53 20) → clusters
- [x] Passive captures: node 45 SYN/FIN/RST headers (TTL/hop-limit of RSTs vs CF SYN-ACKs) + v4/v6 SSH control
      sessions; panel :6365 FIN/RST + upgrade requests (Cf-Ray colo)
- [ ] Nodes 12/26 (no SSH): panel-side reasons/colo after fork.79 panel; compare with 45
- [ ] Verdict with evidence (fixable on our side vs provider/Cloudflare path) + options

### Close-reason logging
- [x] Agent: one line per session end: who/what (read error, write error, ping write, hello timeout, pong stall,
      server close frame code/text, requested, switch), duration, scheme, IP family + remote/local addr; ≤1 line/10 s,
      suppressed count carried into the next line
- [x] Panel: one line per node session end: node id, duration, reason (replaced by newer connection, read error text,
      close code, pong timeout, keepalive write failure), last message age, Cf-Ray colo + client IP (CF) / remote addr;
      new session line notes whether it replaced a live session; ≤1 line/10 s per node
- [x] Unit tests for reason classification + rate limit (agent + panel)

### Diff-only reconnect redeploy (Part 2)
- [x] Agent: per-process runtime revisions (`_flvxRev` on service/chain/limiter/nft payload items) recorded on
      successful apply, cleared on delete/pause/resume/failed apply; `ListServices` reports `revs` + `boot`
- [x] Agent: keep the nftables manager across WS reconnects (built once per process); no DNAT wipe on reconnect
- [x] Agent: handshake capability `runtimeRevs=1`
- [x] Panel: attach `_flvxRev` (sha256 of the item without the key) to every AddService/UpdateService/AddChains/
      UpdateChains/AddLimiters/UpdateLimiters/nft payload at the send layer
- [x] Panel: reconnect redeploy = one `ListServices` → per tunnel / per forward compare desired revs with agent revs →
      skip unchanged, today's path for changed/missing; remove classified orphans (guarded, bounded); old agents
      (no revs) and agent restarts (empty revs) → full deploy
- [x] Reconnect-time extras: machine protection push no-op when unchanged; public IP discovery not on every
      reconnect; quota groups / panel IP family / landing kept (justify)
- [x] Tests: unchanged reconnect → 0 Delete/Add; tunnel edited while offline → only that one; tunnel deleted while
      offline → removed; forward deleted while offline → removed; agent restarted → full deploy; old agent → full deploy;
      determinism of desired payloads (twice the same rev)

### Tests
Results: base backend 17 FAIL lines (the known 15 tests + subtests), go-gost 0, go-gost/x 0 (TMPDIR must be short:
x/landing binds unix sockets); after: identical sets, 0 new. Lab runs `/root/rs138t/lab-{new2,old,final}/results.json`.
- [x] Baseline failure sets on untouched base (go-backend, go-gost, go-gost/x) → `runs/.../tests/base-*.fails`
- [x] Same suites after; no new failures
- [x] Lab: panel + 3-hop tunnel (entry → hop → exit agents), traffic flowing; kill TCP / panel restart / agent restart
      → unchanged reconnect 0 Delete/Add, no listener blip, download continues; changed-while-offline cases; close
      lines on both sides with the right reason; reconnect cost (ms, messages) before/after
- [x] CI green on branch

### Release + rollout
- [x] Prod inventory dry run (orphan classification of every online node = 0 unexpected orphans) before release
- [ ] Tag `3.0.27-fork.79`, Images green, Latest, same 10 assets, pinned version + sha256
- [ ] Prod panel backup `pre-fork79` + prune (keep 2) + upgrade + health + all nodes online
- [ ] Canary 42 (SSH backup), then 45 (SSH backup), then batches with diagnose before/after; skipped nodes listed
- [ ] Resource numbers (agent CPU/RSS/fd/goroutines, journal lines/10 min on 42, 45 + 2 others; panel CPU/RSS,
      reconnect cost) before/after
- [ ] ≥1 h watch: reconnect counts + close reasons for 45/12/26 vs before; reconnects no longer redeploy (panel log);
      GuardVersion 2 on landing/Reality nodes; journal volume drop on 2 busy nodes
- [ ] Summary (`runs/reconnect-stability/summary.md`) + plan complete
