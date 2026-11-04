# 204 — 「解除禁用」 of a scan-blocked landing node must move the revision (fork.112)

Found during release-batch-1104 (plan 203) on production, fork.111, Breadcloud JP canary, temporary admin test node.

## Problem
Scan block worked (hit on the 30th port, panel disabled the node within 4 s, event recorded, traffic refused).
After the admin 「解除禁用」 the node showed 「启用」 in the panel but stayed blocked in the Core for 2 min, until the
test node's delete pushed a newer revision. Agent log at the unblock:
`ApplyLandingDesired 首次失败: "landing revision reused with different content"`.

## Root cause
`TouchLandingRevision` only moves the revision when the egress plan hash or the conn/scan signature changes. A node's
enabled flag is in neither; every normal node status change therefore bumps `landing_server.revision` in its own
transaction (`UpdateLandingNode`, `DeleteLandingNode`, `PrepareLandingNodeDeletion`, …). `LiftLandingScanBlock` did
not. On block the hit's "seen" mark changes the signature, so a new revision went out; on unblock nothing in hash/sig
changes → same revision, new content → the agent (correctly) refuses it → Core keeps the block. The handler test's fake
agent does not enforce the revision rule, so the build round did not see it.

## Fix
- `LiftLandingScanBlock`: `revision + 1` of the node's server in the same transaction (like every other status change).
- Tests: contract test asserts the revision moves on lift (SQLite + PG); handler flow test asserts the pushed revision
  after 解除禁用 differs from the blocked one. Both failed on the unfixed code ("unblock pushed the blocked revision 4
  again") and pass with the fix.

## Checklist
- [x] Reproduce on prod (test node only) + find root cause
- [x] Fix + tests (fail before, pass after; SQLite + PG)
- [x] Full backend suite SQLite + PG on the release head (25/25 + 25/25 packages, 0 failures); build + vet ok
- [x] Tag `3.0.27-fork.112` (→ 17ba3568, 11:28 TW; branch alias `maintenance/3.0.27-fork.112-scan-unblock`); CI + Images 41767393731 green; release Latest, assets = fork.111 set, pins/sha256/arches ok
- [x] Panel upgrade 11:41 TW (backup `pre-fork112-20261104T034142Z`), business checks = baseline (one entry-23 blip in the first round, clean on re-run)
- [x] Re-run the scan test on Breadcloud JP: blocked at port 30 → 解除禁用 → traffic back in 1 s → test node deleted; agent log clean
- [x] Continue plan 203 (NexKR OTA → fork.112, same scan test passed)
