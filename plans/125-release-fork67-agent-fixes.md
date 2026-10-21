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
- [x] Plan doc
- [x] Cherry-pick c0518acb, re-run its tests
- [x] Fix 2 + unit tests (empty name + exe in /etc/flvxx; explicit unchanged; flux_agent co-installed untouched;
      missing unit → error; rollback)
- [x] go-gost/x + go-gost tests (`-race` on x/socket)
- [x] Backend `go test ./...` vs fork.66 baseline by name (15 known failures); frontend tsc + build
- [x] E2E: OTA with empty service_name in a systemd container/VM (or scripted fake) upgrades + restarts the right unit
- [x] Fix 1 local reproduction (healthy wss → panel 502 → stays wss)
- [x] Push branch, CI green
- [x] Tag `3.0.27-fork.67`, Images green, release Latest with the fork.66 asset set (10)
- [x] Prod panel: rollback dir `pre-fork67-<TS>`, prune-backups.sh, upgrade, health, 29/29 online, ws fallbacks recorded
- [x] Canary 「Bestvm HK 年付」(42): SSH backup, scheme/IP/conn/CPU/RSS, diagnose identical
- [x] Node 20 「Vmsilo TW」 via panel OTA
- [ ] Remaining nodes in batches, diagnose before/after
- [ ] Resource numbers (canary + 3 nodes), summary, plan complete

## Notes
- Cherry-pick c0518acb → 0219e084; its unit tests pass (`-race`).
- Fix 2 = f60fb66f (`go-gost/x/socket/agent_install.go`). Beyond the brief: without service_name, a
  `flvx_agent.service` that does not run this binary is refused too (today's fallback would restart another agent).
  Not changed on purpose: an agent without service_name still keeps node_id / traffic baseline / protection state in
  `/etc/flvx_agent` (pre-existing; moving them would reset that node's traffic baseline). That is also why node 20
  had a `/etc/flvx_agent` dir. All 29 prod configs have service_name, so no prod node is affected either way.
- Unit tests: 9 new (agent_install_test.go); mutation check (exe rule / config filter disabled) → 4 fail.
- go-gost/x all pass (`landing` needs a short TMPDIR: unix socket path limit), x/socket `-race` pass, go-gost pass.
- Backend: 15 failures = fork.66 run's 15, same names (go-backend unchanged since c17b60cd). Frontend unchanged;
  `tsc --noEmit` + `npm run build` OK.
- Fix 1 E2E: scenario (e) added to the netns `TestPanelFailoverE2E` (wss answers 502 for 6 s after a healthy
  session, ws on :80 open). fork.66 code: fell back to ws after one 502. Branch: 502 ×2, no ws, back on wss 1.25 s
  after the panel was up; goroutines 12 → 12, max 1 panel connection.
- OTA E2E (`runs/release-fork67-agent-fixes/ota-e2e.sh`, systemd container + branch panel): (1) unit file hidden →
  API `升级失败：找不到 systemd 服务文件 …`, same PID, nothing downloaded; (2) OTA → /etc/flvxx/flvxx = fork.66
  release sha256, `.old` = previous, flvxx.service restarted, online in 3 s, decoy /etc/flux_agent pid + sha
  unchanged; (3) the fork.66 agent with the same layout reproduces node 20: API ok, download in
  /etc/flvx_agent/flvx_agent.new, `Unit flvx_agent.service not loaded`, nothing restarted.
- CI Build Check 39675574265 green. Tag `3.0.27-fork.67` → 1f466a6d; Build and Push Images 39675807411 green (tag push
  triggered it). Release Latest, 10 assets = fork.66 set, compose pins `:3.0.27-fork.67`, install scripts pinned,
  gost sha256 OK, ghcr amd64+arm64. The released gost-amd64 passed the same systemd OTA E2E.
- Prod panel: rollback `/opt/flvx-svc/rollback/pre-fork67-20261021T212454Z` (DB quick/integrity ok; counts node 29,
  user 12, tunnel 45, forward 33); prune kept pre-fork67 + pre-fork66 (pre-fork65 + fork.64 images removed).
  Upgraded 2026-10-22 05:25 TW, healthy in 9 s, 29/29 online 14 s after start; panel RSS 36.8 → 39.3 MiB, 0 error
  lines. ws fallback during the restart (agents still fork.65, SSH-visible nodes): 21, 48, 53 fell to ws (CF :80);
  42, 45, 47, 52 stayed wss; 20/30/31/34/35/36 are direct-origin ws as before.
- Canary 42: wss→wss, diagnose 87/17 = 87 (forward 6 → 82.152.163.228:34749 fails before and after), 3 forced
  resets of its own panel socket → wss each time, 0.7 s (journal); CPU 0.27 → 0.28 %, RSS ~57 MB; flux_agent pid 429
  untouched. SSH backup /root/flvxx-backup-20261021T212541Z-pre-fork67.
- Node 20: panel OTA fork.65 → fork.67 (service_name flvxx already in config), ws to origin as before, flux_agent pid
  27370 untouched; 0 forwards/tunnels to diagnose. Script paused on sockets 1→3 = WS + HTTP report keep-alive +
  public-IPv6 lookup keep-alive (pre-existing) → threshold relaxed to +2 for the batches.
