# 118 — Whole-machine 连接数 + machine connection protection (conn-protect)

User decisions 2026-10-20 (memory `connection-limit-redesign.md`): the ONE number that matters is the whole-machine
连接数 = TCP + UDP rows, every state (exactly the mmwx server card, Helper `readConnections()`); spikes come from
TIME_WAIT / CLOSE_WAIT. Forward rules / user pools / landing-node limits stay as they are (inbound-only).
New per-machine protection, ONE owner per machine (FLVX agent, or the mmwx Helper where it runs).

Base: tag `3.0.27-fork.62` (29f25ee3, = prod). Branch: `feature/3.0.27-conn-protect`.
**Build + screenshots only** — no tag, no release, no prod upgrade, no OTA, no writes on real nodes.

## Defaults (ON for every FLVX-owned node)
| setting | default | 0 means | range |
|---|---|---|---|
| 连接保护 开关 | on | — | — |
| 整机连接上限 (`max_total`) | 1500 | no cap | 100–1,000,000 |
| TIME_WAIT 上限 (`tw_buckets`, kernel `net.ipv4.tcp_max_tw_buckets`) | 500 | leave the kernel value alone | 100–2,000,000 |
| CLOSE_WAIT 超时 (`close_wait_seconds`, native landing Core only) | **10 s** | Core default (off) | 1–3600 |

CLOSE_WAIT default is 10 s, not the 30 s of the brief: user changed it 2026-10-20 ~16Z (memory
`connection-limit-redesign.md`, the mmwx brief says 10 too).

Blocking hysteresis: start dropping NEW connections when total ≥ cap, release when total < ⌊cap × 0.95⌋
(1500 → release below 1425). Sampled every 5 s.

## File contracts with the mmwx Helper (fixed names)
- FLVX writes `/run/flvx/business-ports.json` (dir 0755, file 0644, atomic rename) on every machine:
  `{"version":1,"updated_at":"<RFC3339>","tcp":[ports],"udp":[ports]}` — gost service listeners (not loopback-only
  ones), nft-forwarded rule ports, native landing inbound ports; never the panel connection. Rewritten when the set
  changes and refreshed every 4 min (the Helper ignores the file after 10 min).
- FLVX reads `/run/mmwxc/machine-protection.json` (written by a newer Helper):
  `{"version":1,"owner":"mmwx","updated_at":"<RFC3339>","enabled":bool,"max_total":int|null,"tw_buckets":int|null,
  "close_wait_seconds":int|null,"total":int,"tcp":int,"udp":int,"blocking":bool,"dropped_total":int}`.
- mmwx-owned machine = `mmwxc-helper.service` active OR that file fresh (< 60 s): FLVX keeps no nft table and does
  not touch the sysctl (removes its own table / restores its saved sysctl value), still reports the count and mirrors
  the file (owner = mmwx).
- Custom FLVX share status (`GET /shares/{id}/status`) may carry a server-level `machine` object (see contract doc
  `/root/flvx-workers/contracts/machine-protect-v1.md`); absent = unknown.

## Tasks
- [x] 1. Baselines: go-backend `go test ./...` failure set on fork.62; go-gost/x packages touched
- [x] 2. Agent counter: /proc/net tcp+tcp6+udp+udp6 row counts (= mmwx) + TCP state breakdown, one pass, 5 s; tests incl. parity with the mmwx algorithm
- [x] 3. Agent owner detection (Helper unit active / fresh status file) + mmwx status mirror; tests
- [x] 4. Agent business ports (gost services, nft forwards, native landing) + business-ports.json writer; tests
- [x] 5. Agent nft guard: own table `inet flvx_machine_protection`, prerouting −150 chain only while blocking, lo exempt, ct new + port set + local destination → counter drop, named counter = dropped_total, atomic netlink batches, self-heal; tests
- [x] 6. Agent sysctl `tcp_max_tw_buckets`: save original (state file) before first change, apply on start, restore on disable / mmwx ownership; tests
- [x] 7. Agent CLOSE_WAIT: native landing Core `default_close_wait_timeout_seconds` (verified Core semantics); tests (+ real Core if available)
- [x] 8. Agent wiring: ONE loop per process (not per WS reconnect), settings command `SetMachineProtection` persisted in config.json, handshake capability, report `machine_conns` / `machine_protect` (only when a new 5 s sample exists); goroutine-stability test
- [x] 9. Agent netns E2E (real nft, own netns): gost listener + nft DNAT + non-business port; low cap → new TCP/UDP dropped on business ports only, existing alive, lo fine, release, counter rises; tw_buckets applied/restored; mmwx-owned → no table, sysctl untouched, ports file written; restart/reconnect no duplicate loops
- [x] 10. Panel: node columns + migration defaults (SQLite + PostgreSQL), create/update validation, push on connect + on change (capable agents only); tests
- [x] 11. Panel: report parsing, in-memory latest per node (bounded, TTL), role-aware WS broadcast (admins only get the new fields), node list (admin) + monitor nodes (visible nodes, chip fields only) APIs; tests admin vs user
- [x] 12. Panel: shared landing `machine` passthrough from Custom status → memory → 落地 monitor server view; tests
- [ ] 13. UI: node dialog 「连接保护」 section (FLVX-owned / mmwx-owned / old Helper / old agent)
- [ ] 14. UI: chip 「整机连接 N/上限」 (yellow ≥ 80 %, red + 保护中, `-` unknown) + breakdown popover on node page (list + cards) and 监控 node view
- [ ] 15. UI: 监控 →「落地」 shared server card chip from the mmwx `machine` object
- [ ] 16. Gates: go vet, go-backend tests vs baseline, go-gost/x tests, tsc, eslint on changed files, `npm run build` (< 5 MiB)
- [ ] 17. Resource numbers: agent CPU/RSS at ~1500 sockets, nft cost, report payload growth, panel memory for 200 nodes
- [ ] 18. Screenshots (desktop + H5, light + dark) + index page in the run dir
- [ ] 19. Push, CI green, contract doc, summary

## Out of scope
Release/tag/OTA/prod; mmwx code; `tcp_conns` and its display; forward/user/landing-node limits; billing;
tcp_tw_reuse or other sysctls; history of the new numbers; other existing nft tables.

## Evidence
- Baseline (fork.62 export, run dir `logs/`): go-backend 15 failing test names (10 top-level, identical to the fork.62
  release record); go-gost/x `go test ./...` all pass.
- Agent (`8cc90ce2`, `2322829d`): package `go-gost/x/machineprotect` + socket wiring; `go test ./...` in go-gost/x
  all pass (race on the new packages), vet clean. Counter = verbatim mmwx algorithm on fixtures and on a copy of this
  host's /proc/net. Real pinned Core (SHA ae4b83f0): CLOSE_WAIT stays without the setting, closed after 2.0 s with
  `default_close_wait_timeout_seconds=2`.
- Agent netns E2E (`socket/machine_protect_netns.sh`, evidence `logs/agent-netns-e2e-evidence.txt`): real gost TCP+UDP
  forward services + real nft DNAT (TCP/UDP) + non-business port, cap 60: over cap (85) new TCP/UDP to gost and DNAT
  ports dropped, existing TCP/UDP flows, non-business port, loopback and own-IP-via-lo all work, dropped_total 6,
  release at 15 < 57; tw_buckets 16384→10 applied (TIME_WAIT stayed ≤ 10 after 40 closes) and restored on disable;
  mmwx-owned (fresh status file) → table removed, sysctl untouched (also a later Helper value 777 left alone), ports
  file still written; restarted agent adopts + removes the leftover table and restores the saved original; one loop.
- Agent resources (1501 sockets, `logs/agent-netns-resources.txt`): count 1.5 ms; whole sample 3.2 ms CPU = 0.064 % of
  one core at 5 s; report fields 251 B JSON per 5 s; idle table has no hooked chain (zero per-packet work).
- Panel (`f1941e21`): full `go test ./...` = the same 15 baseline failure names, 0 new (`logs/new-go-backend.fails`);
  `go vet ./...` output identical to the base (only the pre-existing leikwan/testapi warnings). New tests: ws push
  only to capable agents, admin-only WS fields, store bounds/freshness; contract tests for create/update defaults and
  validation, push on connect / change / reconnect, admin vs monitor-user visibility, SQLite + PostgreSQL migration
  defaults (temporary postgres:18 container, removed); shared `machine` passthrough (canonical + alias, malformed and
  absent = unknown, stale after 3 min). Landing tests also passed on PostgreSQL. Memory: 200 node samples = 23 KB heap.
