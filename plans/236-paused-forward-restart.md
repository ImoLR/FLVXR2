# 236 paused-forward-restart — paused rules came back up after an agent restart (P0)

User approval: 2026-11-07 ~20:55 TW 「这个肯定是要修的 赶紧查明原因然后修复它」 (urgent; third FLVX release of 11-07 waived).

Incident: clot88 expired 11-06 23:59:59 TW, the panel paused his 12 rules at 00:00:05 (Mkcloud 沪港ixp 新春版 +
Mkcloud 沪日ixp 440). The fork.123 OTA restarted both agents at 08:55/08:56 → every paused service listened again
(gost.json still had `metadata.paused: true`) → ~13.8 GB used until the coordinator re-paused them at 20:45.

- Base: tag `3.0.27-fork.124` (4799be34)
- Feature branch: `feature/3.0.27-paused-restart`; release branch `maintenance/3.0.27-fork.125-paused-restart`, tag `3.0.27-fork.125`
- Run dir: `/root/flvx-workers/runs/paused-forward-restart`

## Root causes (every path that re-opened a paused rule)
1. **Agent startup** — `go-gost/x/config/loader/loader.go` parsed + registered every service of gost.json and
   `program.run` served them all; `metadata.paused` was never looked at (the incident).
2. **Agent reload** — SIGHUP `reloadConfig` and the HTTP `/config/reload` use the same loader (same bug).
3. **Agent UpdateService/AddService** — always re-created + served the service and replaced the stored config
   (dropping `paused`); the panel's follow-up PauseService closed it again, so a paused rule was open for a moment
   on every sync (rule edit, tunnel/node edit, …) and stayed open if that PauseService failed.
4. **Panel nftables sync** — `syncForwardServicesWithWarningsOnNode` installed the nft rules of a *paused* nftables
   rule and returned before the keep-paused step (any sync of a paused nft rule re-opened it).
5. **Panel reconnect** — the reconnect redeploy only handles status-1 rules; nothing ever re-paused what a
   restarted agent had re-opened.

Not affected: WG rules (paused = rule file removed), nft rules on agent restart (paused = rules deleted, nothing
restored), native landing (desired.json keeps `enabled:false`, the Core config has no static inbounds; the agent
adds only enabled nodes after a Core/agent restart).

## Fix
- Agent: paused service configs register a closed placeholder (`PausedService`) at load/reload/Add/UpdateService;
  Resume/Delete/Pause find it by name; ResumeService parses + serves (no 500 ms wait for placeholders);
  ListServices reports `paused` (always present → panel can tell plan-236 agents); one boot log line.
- Panel: paused-rule pushes carry `metadata.paused: true`; paused nft rules are removed, never installed (quota
  release sets status 1 before its sync); on every agent (re)connect one DB query; if the node hosts non-running
  rules (status <> 1: user/admin pause, user expiry, flow quota, rule expiry, CN block, landing pause): one
  ListServices + at most one batched PauseService for what the agent still runs (plan-236 agents: nothing).

## Checklist
- [x] Investigation: all resurrect paths (agent loader/reload/update, panel nft sync, reconnect) + landing check (code)
- [x] Agent fix + test (`TestPausedServiceStaysClosedAcrossRestartAndUpdate`: fails on fork.124 loader, passes)
- [x] Panel fix + handler tests (`TestRepause*`, `TestSyncPaused*`: sync tests fail on fork.124) + contract test
      (`TestPausedRuleStaysPausedAcrossAgentRestartContract`: old-agent case fails on fork.124 panel, passes)
- [x] Gates: go build / go test ./... SQLite + PostgreSQL, go-gost + go-gost/x tests (compare with fork.124 base) — backend SQLite + PG all ok; agent x: only pre-existing flaky TestSocketCapSharedByProbeAndPin; branch CI 42377494034 green
- [x] Lab: real agents fork.124 vs fork.125 + local panel: pause → restart → closed; resume; old agent + new panel
- [x] Baseline before release (rules, 沪台, landing links, subs, 救命版, panel CPU/RSS, paused-rule ports)
- [x] Ledger row 236; push branch + tag; CI + images + assets verified (tag → e3d4bfc7, branch CI 42377494034, Images 42378251234, Latest, 10 assets, compose/install pinned, gost sha256 + `-V` fork.125)
- [x] Panel backup + rollback dir (`/opt/flvx-svc/rollback/pre-fork125-20261107T140216Z`); prune (kept pre-fork124 + pre-fork125); upgrade 22:02:48 TW (9 s); re-check = baseline
- [x] OTA Mkcloud 沪港ixp 新春版 + Mkcloud 沪日ixp 440 (backup first); post-OTA restart keeps paused ports closed — proven with temp rules `pr-paused-23/32` (clot88 was renewed by an admin at 21:52:59 TW, his 12 rules are running legitimately)
- [x] Fleet check (read-only): no status<>1 rule port listening on any online node (31/31 online, 29 checked; Nobrand 5cm SSH down, Dmit Echo no key; 0 paused rules left on prod)
- [x] Landing: expired users' landing nodes stay disabled (code + `TestDisabledLandingNodeStaysClosedAfterRestart`; prod has 0 expired users with landing nodes; shared = panel sends enabled:false, mmwx side not checked)
- [x] Ledger → 已发布; temp `pr-` leftovers 0; summary; plan complete

## Results
- Lab (netns, synthetic `pr-*` data), pause → agent restart → 25 s watch → resume:
  | panel / agent | paused port after restart | resume |
  |---|---|---|
  | fork.124 / fork.124 | **open after 0.7 s, stays open** (bug reproduced) | ok |
  | fork.124 / fork.125 | closed (never opened) | ok |
  | fork.125 / fork.124 | opened at 0.6–0.8 s, closed ~0.1 s later by the reconnect re-pause (1 ListServices + 1 PauseService) | ok |
  | fork.125 / fork.125 | closed (never opened), no re-pause command | ok |
  Editing a paused rule: ≤10 ms window with an old agent, 0 with fork.125.
- Prod (fork.125 panel):
  - Old agent fork.123 restart on Mkcloud 沪日ixp 440 with a paused temp rule: port re-opened at +7.2 s (agent start), closed
    ~0.9 s later by the reconnect re-pause (`repause: node 23: 1 paused rules, re-paused services=2 … commands=2`).
  - OTA 沪日ixp 440 22:09 (online 5.6 s) and 沪港ixp 新春版 22:12 (6.5 s): paused temp port 0 open samples (692 / 499 samples
    at 0.1 s); post-OTA `systemctl restart`: 0 open samples, no re-pause command (plan-236 agent reports `paused`);
    running rules missing ≤0.9 s per restart. Resume after the restarts works; temp rules deleted (0 leftovers).
  - Business = baseline (15 real rules incl. 6 沪台, 7 landing links 2/2, 10 sub formats same bytes, 救命版 48/49 untouched,
    runtime objects 304, 0 orphans). Panel CPU 18.9–21.6 → 17.2 s/5 min, RSS 42.9–43.4 → 55.0 MiB (fresh process).
  - Re-pause cost: nodes without paused rules = 1 DB query, 0 commands; with paused rules: old agent 2 commands
    (ListServices + one PauseService for all), plan-236 agent 1 (ListServices).
- Not OTA'd (panel re-pause protects them on every reconnect): every agent except the two Mkcloud machines.

