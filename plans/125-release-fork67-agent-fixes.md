# 125 — Release fork.67: two agent fixes (5xx no-ws-fallback + OTA without service_name) + OTA all nodes

Brief: `release-fork67-agent-fixes` (coordinator called it plan 124, but 124 is already taken by
`124-embedded-share-trial.md` on this base, so this is 125). User approval 2026-10-22: 「可以,你直接去升級吧」.

Branch `maintenance/3.0.27-fork.67-agent-fixes` from `maintenance/3.0.27-fork.66-embedded-share` (prod fork.66,
c17b60cd + trial doc 25e3e4ef). Worktree `/root/projects/flvxx/flvxx-fork67`. Run dir
`/root/flvx-workers/runs/release-fork67-agent-fixes`. Agent only — no panel / UI / DB change.

## Fixes
1. Cherry-pick c0518acb: after this process has had a healthy wss session, a 5xx during reconnect (panel restart)
   ends the round and wss is retried after the backoff instead of falling back to ws.
2. OTA / rollback when `config.json` has no `service_name` (node 20 「Vmsilo TW」 root cause: `getConfigDir("")` =
   `/etc/flvx_agent`, restart script stopped a unit that does not exist → silent no-op).
   - Explicit `service_name` always wins (incl. the legacy `flux_agent*` → `flvx_agent*` migration).
   - Empty → derive from the running executable (`/etc/<n>/<n>`, symlinks resolved) when
     `/etc/systemd/system/<n>.service` exists; never a `flux_agent*` name from this rule.
   - Config loader: a fallback config (`/etc/flvx_agent/config.json`, `/etc/flux_agent/config.json`) is only used
     when the running binary lives in that directory — another panel's co-installed agent is never adopted.
   - Missing target unit → OTA / rollback fail before downloading, progress `failed` + reason, error to the panel.

## Checklist
- [ ] Plan doc
- [ ] Cherry-pick c0518acb, re-run its tests
- [ ] Fix 2 + unit tests (empty name + exe in /etc/flvxx; explicit unchanged; flux_agent co-installed untouched;
      missing unit → error; rollback)
- [ ] go-gost/x + go-gost tests (`-race` on x/socket)
- [ ] Backend `go test ./...` vs fork.66 baseline by name (15 known failures); frontend tsc + build
- [ ] E2E: OTA with empty service_name in a systemd container/VM (or scripted fake) upgrades + restarts the right unit
- [ ] Fix 1 local reproduction (healthy wss → panel 502 → stays wss)
- [ ] Push branch, CI green
- [ ] Tag `3.0.27-fork.67`, Images green, release Latest with the fork.66 asset set (10)
- [ ] Prod panel: rollback dir `pre-fork67-<TS>`, prune-backups.sh, upgrade, health, 29/29 online, ws fallbacks recorded
- [ ] Canary 「Bestvm HK 年付」(42): SSH backup, scheme/IP/conn/CPU/RSS, diagnose identical
- [ ] Node 20 「Vmsilo TW」 via panel OTA
- [ ] Remaining nodes in batches, diagnose before/after
- [ ] Resource numbers (canary + 3 nodes), summary, plan complete

## Notes
