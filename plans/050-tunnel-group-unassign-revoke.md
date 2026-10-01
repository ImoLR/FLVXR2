# 050 - Revoke inherited permissions when tunnels leave a tunnel group

## Problem
Editing a tunnel group (`POST /api/v1/group/tunnel/assign`) and removing tunnels from it
does not revoke the `user_tunnel` permissions that users inherited through
`group_permission`. Adding tunnels works (via `syncPermissionsByTunnelGroup`), but removal
leaves stale `group_permission_grant` rows and group-created `user_tunnel` rows, so users
in the bound user groups still see the removed tunnels.

`groupUserAssign` already revokes grants for removed users
(`RevokeGroupGrantsForRemovedUsersTx`); `groupTunnelAssign` has no equivalent.

## Approach
- Add `Repository.RevokeStaleTunnelGroupGrantsTx(tx, tunnelGroupID, currentTunnelIDs)`:
  delete grants of this tunnel group whose `user_tunnel.tunnel_id` is no longer a member,
  then delete group-created `user_tunnel` rows that have no remaining grants.
  Comparing against the current member set (not a previous/current diff) also cleans up
  stale grants left behind by earlier removals the next time the group is saved.
- Call it in `groupTunnelAssign` inside the same transaction; after commit, run
  `cleanupForwardsForUserTunnel` for revoked pairs (same as the user-group path).
- Manually assigned `user_tunnel` rows (`created_by_group = 0`) and tunnels still granted
  through another tunnel group are kept.

## Tasks
- [x] Add repository method `RevokeStaleTunnelGroupGrantsTx`
- [x] Wire it into `groupTunnelAssign` with forward cleanup
- [x] Add contract test for removing a tunnel from a tunnel group
- [x] Rebase the change onto the `3.0.27-fork.5` line for the `3.0.27-fork.6` release
- [x] Run `go test ./...` in `go-backend` (new test passes and fails without the fix; the
  remaining failures — 3 handler unit tests and 9 contract tests — are identical on the
  unmodified fork.5 baseline and unrelated to this change)
