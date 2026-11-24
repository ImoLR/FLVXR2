# 304 — Native landing port picker skips ports used on the host (+ warm-tag test flake)

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

## Checklist

- [x] Read plan 147 / 216 code, agent commands (TcpPing, MonitorPort), prod state (ss on 99u / Fachost, DB)
- [x] Repo: split `landingUsedPorts`, add `NativeLandingPortCandidates`, `NoteNativeBusyPorts`, `NativeLandingMovableCount`
- [x] Handler: `probeNativeLandingPorts` + call in node create, forward 新建落地, plan-147 move
- [x] Unit / contract tests (probe busy → skip, offline / error → fallback, bounded probes, move path)
- [x] Prod-DB-copy lab: servers 15 / 16 with the real port set, ports below 38889 filled → pick skips 38889
- [x] Fix `TestForwardWarmTagContract` (reproduce before, stable after)
- [x] Full backend tests vs fork.167 base (compare failure sets)
- [ ] Commit, push, tag `3.0.27-fork.168`, CI + Images green, 10 assets, gost sha256
- [ ] Prod: baseline (nodes, forwards, 5 busiest diagnose + RTT), backup + rollback.sh, prune, swap
- [ ] Prod verify: health, 30/32 online, 63 forwards, light node check, live probe proof on 99u / Fachost
- [ ] Ledger row 304 → 已发布, memory updates, plan marked complete
