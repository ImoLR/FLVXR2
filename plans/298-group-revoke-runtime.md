# 298 — group revoke leaves agent services of deleted forwards running

Base: tag `3.0.27-fork.162`, branch `fix/3.0.27-group-revoke-runtime`, release `3.0.27-fork.163` (panel only, no DB schema change, no OTA).

## Problem
Prod 11-23 ~19:35 TW: user ns-saorana (76) removed from user group 7 「沪日IEPL」 via `group/user/assign`.
Forwards 605/606/608 were deleted from the DB, but `605_76_416_*`, `606_76_416_*`, `608_76_422_*` stayed on
「vmsilo 沪日首发」 (listening + in gost.json) until a manual `node/orphan-sweep`.

## Root cause
The group handlers commit the revoke transaction (which deletes the group-created `user_tunnel` rows 416/422) and only
then call `cleanupForwardsForUserTunnel`. `forwardServiceBaseCandidates` builds `<fwd>_<user>_<ut>` names from the user's
**current** user_tunnel ids (+0), so the revoked id is never a candidate; DeleteService tolerates not-found → silent leak.

## Fix
- `repo.RevokedUserTunnelPair` carries `UserTunnelID` (the deleted row id), set in all 3 revoke helpers.
- `model.ForwardRecord.ExtraUserTunnelIDs` (`gorm:"-"`, runtime only): extra service-name candidates.
- `forwardServiceBaseCandidates` adds them; `cleanupForwardsForUserTunnel(user, tunnel, revokedUT...)` sets them.
- Every caller that cleans after a revoke passes `pair.UserTunnelID`.

## Path audit
| Path | Status |
|------|--------|
| `group/user/assign` (`groupUserAssign`, RevokeGroupGrantsForRemovedUsersTx) | **fixed** |
| `group/tunnel/assign` (`groupTunnelAssign`, RevokeStaleTunnelGroupGrantsTx) | **fixed** |
| `group/permission/remove` (RevokeGroupPermissionPairTx) | **fixed** |
| user group delete / tunnel group delete (`groupDelete` → GroupDeleteCascade) | **fixed** |
| tunnel entry split (`tunnel_entry_split.go`, ApplyTunnelEntrySplitTx → RevokeStaleTunnelGroupGrantsTx) | **fixed** |
| `tunnel/user/remove` (`userTunnelRemove`) | already OK: cleans before `DeleteUserTunnel` (test added) |
| user delete / `user/batch-delete` (`deleteUserAndCleanup`) | already OK: runtime cleanup before DeleteUserCascade |
| tunnel delete / batch delete (`deleteTunnelByID`) | already OK: runtime cleanup before DeleteTunnelCascade |
| `reconcileTunnelEntryPermissions` (entry restriction → 0 entries) | already OK: no user_tunnel row is deleted on that path |
| `tunnel/user/batch-assign`, `batch-update-status`, forward batch ops | already OK: no user_tunnel deletion |
| `tunnel-group*` / `tunnel-group-new*` handlers | already OK: only junction tables, no grants/user_tunnel touched |
| nftables / WG path forwards | not affected: delete by forward id / path, not by `<ut>` name |

## Checklist
- [x] Worktree + branch from fork.162, plan doc
- [x] Fix (repo pair id, ForwardRecord extra ids, candidates, callers)
- [x] Tests: group/user/assign, group/tunnel/assign, group/permission/remove, group delete, tunnel/user/remove
- [x] `go test ./...` (compare failures with fork.162) — 29/29 packages ok, no failures; new test fails 5/6 on fork.162 code (tunnel/user/remove already OK)
- [ ] Commit, push, tag fork.163, CI + Images + assets, Latest
- [ ] Prod backup + rollback script, prune, swap
- [ ] Post-swap: nodes, forwards, log 15 min
- [ ] Live check with throwaway user/group/forward
- [ ] Orphan-sweep dry-run all nodes; delete only clear FLVX forward orphans
- [ ] Memory + ledger, plan marked complete
