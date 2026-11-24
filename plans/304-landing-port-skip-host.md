# 304 — Native landing port picker skips ports used on the host (+ warm-tag test flake) (DONE 11-24 17:20 TW, fork.168)

Task `landing-port-skip-host` (coordinator 1218a2c5, 11-24 TW). Panel only (backend), no DB change, no OTA.
Branch `maintenance/3.0.27-fork.168-port-skip` from fork.167 (46847063). Release = `3.0.27-fork.168`.

## Problem

On Boil HKT 99u (landing_server 15) and Fachost seednet 黑五 (landing_server 16) the native
landing range is 30000–39999 and mmwx's xray listens on `127.0.0.1:38889` inside it.
The picker (`landingFreePort`) only knows ports from the panel DB. A port held by another
program is learned only after a failed start (plan 147: agent reports `port_in_use`, a
fresh node moves ≤ 3 times, busy port remembered 24 h in `landing_server.native_busy_ports`).

Prod read-only check 11-24 (`ss -ltnup`, TCP + UDP in 30000–39999):

| Host | Taken in 30000–39999 |
|------|----------------------|
| Boil HKT 99u (`mmwx-s2`) | 30000 tcp+udp (FLVX landing node 221), **127.0.0.1:38889 tcp (mmwx xray)** |
| Fachost seednet 黑五 (`mmwx-facseed`) | 30000 tcp+udp (FLVX landing node 223), **127.0.0.1:38889 tcp (mmwx xray)**, udp 38134 / 39355 = xray UDP client sockets (ephemeral, change all the time — not listeners) |

Both agents run fork.165. `native_busy_ports` is empty on both servers.

## Design

- **No agent change.** Before the picker hands out a native port, the panel checks the next
  candidate(s) with the `TcpPing` command every agent already has: one TCP connect to
  `127.0.0.1:<port>` (count 1, 1 s, RST close). A connect that succeeds = some program
  listens there (loopback or wildcard) → the port goes into `native_busy_ports` (the list
  the picker already skips) and the next candidate is checked.
- Bounded: at most `need + 3` connects per allocation (normally exactly 1), only at
  allocation — node create (`landing/node/create`, the forward form's 新建落地) and the
  plan-147 auto-move (only when a movable node will really move). No periodic scan.
- Outside any DB transaction / sync lock; 2 s per command, 5 s total budget.
- Agent offline / remote / slow / error → nothing learned, pick as before (plan 147 fallback);
  creation is never blocked by the probe.
- Re-check instead of forgetting: a remembered port expires after 24 h as before, but when it
  comes up as a candidate again it is probed again (still busy → remembered again), so an
  online agent never gets a host-used TCP port handed out.
- Not covered (fallback = plan 147 report + auto-move + sync_error text): UDP-only holders
  (SS2022 also binds UDP), listeners bound to one specific non-loopback address, transient
  ephemeral sockets (range overlaps the kernel's 32768–60999).
- No UI change: the existing `sync_error` / range texts stay.

## Item 2 — `TestForwardWarmTagContract` flake

Cause (test-only): `waitAdmin("")` used `strings.Contains(got, "")`, which is always true, so
the last step returned before the panel applied the agent's "sleep" metric (the panel handles
each metric in its own goroutine, `go onMetric`); the owner's read then raced it and sometimes
still saw 「faster」. Fix: wait for an empty tag when the wanted tag is empty.

## Tests (11-24)

- New handler tests `landing_port_probe_test.go`: busy ports skipped + remembered, no re-probe while fresh,
  re-check after 24 h, no probe for a user without access, fallback (no answer / offline agent / offline
  node → plan-147 move as before), move target probed, bounded (need + 3), and the 99u / Fachost layout
  (every port below 38889 taken → 38890). With the create-time probe disabled the layout test fails
  (node first lands on 38889 and has to move).
- `TestLandingNodeCreateHidesProfileInternalsForUsers`: its strict fake agent now also answers TcpPing.
- Flake: `go test -run TestForwardWarmTagContract -count=30 -cpu 1` fails 30/30 before, passes 30/30 after
  (+ 30/30 at -cpu 4).
- Full `go test ./...`: base fork.167 (46847063) 29/29 packages ok; new 29/29 ok (handler package once
  failed on the stub above, fixed and re-run green). Contract suite 216 s vs 218 s.
- Prod-DB-copy lab (copy 11-24 16:35 TW, real `ss -Hltn` lists of both hosts, agent stub answering from
  them): today → probe 30001 (free) → node 30001; ports 30000–38888 filled → old picker would give 38889,
  new: probe 38889 (taken) + 38890 (free) → node 38890, moves 0, one push, native_busy_ports {38889}.
  Same on server 15 and 16.

## Release (11-24)

- Commits 6faab90d (feature) + b7dd9470 (flake fix); tag `3.0.27-fork.168` → b7dd9470, branch
  `maintenance/3.0.27-fork.168-port-skip`. CI 45359723110 + Images 45359725669 green; release Latest,
  not prerelease, 10 assets, gost amd64/arm64 sha256 OK, compose images + PINNED_VERSION = fork.168.
  go-gost and frontend code unchanged → no OTA.
- Prod swap 17:08:54–17:09:03 TW (prod compose kept — it has the old-panel DB mount — only image tags
  changed). Rollback `/opt/flvx-svc/rollback/pre-fork168-20261124T090852Z/rollback.sh` (→ fork.167).
  Backups kept: pre-fork167 + pre-fork168 (prune also removed pre-fork166 and the scratch DB of the
  separate `del-fanscool-1124` op).
- After: 30/32 online as before (火山上海 / 火山广州 offline before), 49 tunnels / 8 subs, landing nodes
  unchanged, 30 redeploys failed=0, no error lines. Forwards 63 → 60 = user fanscool deleted ~17:00 TW by
  the separate `del-fanscool-1124` op (already gone in the pre-swap backup), not this release.
- 5 busiest forwards (90 / 625 / 631 / 644 / 660): diagnose 3/3 before and after, latency equal
  (e.g. #90 26.6 → 27.5 ms, #660 58.1 → 57.2 ms), aws-hk RTT p50 within ±8 ms.
- Live proof on Boil HKT 99u (17:13 TW): temporary `127.0.0.1:30001` listener → admin test node
  「plan304-probe-test」 created on **30002** (moves 0, no error, 1.6 s), log
  `landing server=15: ports [30001] are in use by another program …`; test node deleted right after
  (inbound gone, landing core not restarted), listener stopped. Left behind: server 15
  native_busy_ports {30001} until ~11-25 17:13 TW (picker skips 30001 on 99u until then; harmless).
- Read-only: on both hosts `127.0.0.1:38889` connects (taken) and `127.0.0.1:30001` is refused (free),
  i.e. what the agent's TcpPing sees.

## Checklist

- [x] Read plan 147 / 216 code, agent commands (TcpPing, MonitorPort), prod state (ss on 99u / Fachost, DB)
- [x] Repo: split `landingUsedPorts`, add `NativeLandingPortCandidates`, `NoteNativeBusyPorts`, `NativeLandingMovableCount`
- [x] Handler: `probeNativeLandingPorts` + call in node create, forward 新建落地, plan-147 move
- [x] Unit / contract tests (probe busy → skip, offline / error → fallback, bounded probes, move path)
- [x] Prod-DB-copy lab: servers 15 / 16 with the real port set, ports below 38889 filled → pick skips 38889
- [x] Fix `TestForwardWarmTagContract` (reproduce before, stable after)
- [x] Full backend tests vs fork.167 base (compare failure sets)
- [x] Commit, push, tag `3.0.27-fork.168`, CI + Images green, 10 assets, gost sha256
- [x] Prod: baseline (nodes, forwards, 5 busiest diagnose + RTT), backup + rollback.sh, prune, swap
- [x] Prod verify: health, 30/32 online, forwards (60 — see release notes), light node check, live probe proof on 99u
- [x] Ledger row 304 → 已发布, memory updates, plan marked complete
