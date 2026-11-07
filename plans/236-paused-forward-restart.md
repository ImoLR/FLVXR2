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
- [ ] Gates: go build / go test ./... SQLite + PostgreSQL, go-gost + go-gost/x tests (compare with fork.124 base)
- [ ] Lab: real agents fork.124 vs fork.125 + local panel: pause → restart → closed; resume; old agent + new panel
- [ ] Baseline before release (rules, 沪台, landing links, subs, 救命版, panel CPU/RSS, paused-rule ports)
- [ ] Ledger row 236; push branch + tag; CI + images + assets verified
- [ ] Panel backup + rollback dir; prune (keep 2); upgrade; re-check
- [ ] OTA Mkcloud 沪港ixp 新春版 + Mkcloud 沪日ixp 440 (backup first); clot88 ports closed after a post-OTA restart
- [ ] Fleet check (read-only): no status<>1 rule port listening on any online node
- [ ] Landing: expired users' landing nodes stay disabled (read-only)
- [ ] Ledger → 已发布; temp `pr-` leftovers 0; summary; plan complete

## Results
