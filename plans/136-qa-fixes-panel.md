# 136 · QA round-2 panel fixes → 3.0.27-fork.76 (panel only)

Source: `/root/flvx-workers/runs/qa-sweep/findings.md` section 「第二轮」 (F11–F28, R6–R11).
User approval 2026-10-23 ~15:20 TW (「可以按你推荐去修吧」): panel batch, no OTA, no screenshot gate,
do not redesign existing UI, keep the rules-page palette.

Base: `origin/maintenance/3.0.27-fork.75-landing-rule-layers` (e7b5174b, = prod fork.75).
Branch: `maintenance/3.0.27-fork.76-qa-fixes`. Run dir: `/root/flvx-workers/runs/qa-fixes-panel`.
Agent batch (F13/F14) is plan 137 and bases on fork.76.

## Checklist

### Setup
- [x] Read findings 「第二轮」, r2 final/report, fork.75 release recipe
- [x] Baseline backend suite on untouched base (`base.fails`: 15 real = fork.75 set; +4 export artefacts
      that need repo files outside go-backend)

### Must fix
- [x] F11 stale 「在线」 after panel restart: reset local nodes offline before serving, agent connect → online
- [x] F12 tunnel delete migration: target list = tunnels every rule owner may use; backend re-checks per owner
      (also forward batch change-tunnel)
- [x] F15 LeiKwan: DB pipes missing on LeiKwan side listed as 「利群侧已删除」 + existing cleanup actions
- [x] F16 opening 规则页 never writes config; R10 no pruning of off-page group preferences
- [x] F17 home/rules polling 5 s → slower + paused when hidden; admin WS volume reduced (measure bytes/min)
- [x] F09 「Powered by FLVX」 overlay no longer covers the 24 h chart axis on first screen
- [x] F18 useless requests per page (app_favicon, config/list ×3, check-updates)
- [x] F19 node modal: validation errors visible; invalid IPv4-looking hosts rejected
- [x] F20 landing node name: input length limit + correct message
- [x] F21 rule port out-of-range message wording
- [x] F22 diagnose: no stale 「诊断中」 line after the result
- [x] F23 node delete refusal names the tunnel(s)
- [x] F24 two-tab user edit: optimistic version check
- [x] F25 over-limit user: hint on 主页
- [x] F26 login timing: dummy bcrypt for unknown users and legacy MD5 hashes (disabled-account message kept, see notes)
- [x] F27 `node/info` with arbitrary token → 401 (no nil pointer)
- [x] F28 plan 134 user rule editor: default outbound matches label; cancel with unsaved rules asks
- [x] R8 escape `:`/`@` in AnyTLS/Mieru share link userinfo
- [x] R11 24 h chart tick
- [x] R6 / R9 investigate; fix if contained, else list under 「还没解决」

### Tests
- [x] Contract/unit tests: F11, F12, F15, F23, F24, F26 (login timing), F27 (node/info 401), R8, WS throttle/fields, service_name write-once (F16 = frontend, verified in the browser lab)
- [x] Full backend suite after (no new failures vs base: same 15 pre-existing; one old test updated for F12)
- [x] Frontend tsc / lint / build (no new eslint findings vs base after auto-fixing my own lines)
- [x] QA checks re-run on a local prod-size lab (neutralised DB copy via `scripts/qa-sweep/make_lab_db.py`, 29 fake agents
      replaying prod metric payloads, headless Chromium): F16 write reproduced on fork.75 and gone on fork.76, polling,
      WS volume, F09/F19/F25/R11 screenshots (run dir `lab/ui-new/`)
- [x] Resource numbers before/after on the lab (prod numbers after deploy below)

### Release
- [x] Commit + push branch, CI green (run 39929767197)
- [ ] Tag `3.0.27-fork.76`, images + release assets verified
- [ ] Prod backup `pre-fork76-<TS>` + prune (keep 2)
- [ ] Upgrade prod panel (no OTA), health + 29 nodes
- [ ] Prod read-only checks: node status = live connections, #318 listed as 「利群侧已删除」, 规则页 opens
      without POST config, polling interval in nginx log
- [ ] Summary → run dir summary.md; plan marked complete

## Decisions / notes
- F11: `ResetNodeOnlineState` runs in `App.Run` before `ListenAndServe` (no agent can be connected yet);
  federation (`is_remote=1`) nodes keep their synced status. Consumers checked: redeploy scheduler / quota
  groups (hooks, not DB status), landing sync (returns early for offline nodes instead of failing a send),
  line-chain exit check, path latency (skips offline), health checks (no node status), federation node info.
- F12: owners that no longer exist stay unrestricted (nobody but an admin can edit those rules).
- F17: 主页 30 s, 规则页 15 s (both already skipped hidden tabs; now also refresh once on becoming visible);
  federation share lists cached 5 min; admin WS metric = 20 rendered fields only + at most one per node per
  1.5 s; `service_name` stored only on change (prod showed a node-row UPDATE per node per second).
- F26: disabled accounts still answer 「账号被停用」 after a correct password (kept for the account owner).
- R6: no change — the fork.20 retry loop already re-syncs a failed (tunnel, node) every 60 s, backoff to 10 min.
- R9: not changed — running the expiry job earlier also disables the account (login) and moves auto-renew
  billing earlier; a first traffic report already pauses rules of expired / over-quota users at once.

## Lab numbers (29 fake agents, prod-size neutralised DB copy, 2 runs each)
| | fork.75 | fork.76 |
|---|---|---|
| panel CPU (60 s, 1 admin WS open) | 4.12 % / 3.62 % | 3.08 % / 2.77 % |
| RSS | 40.6 / 41.0 MB | 41.0 / 42.1 MB |
| admin WS | 1740 msgs, 1.86 MB per min | 870 msgs, 0.71 MB per min |
| 主页 admin / user requests per min | 24 / 12 | 4 / 2 |
| 规则页 admin requests per min | 36 | 4 |
| config/list per page load | 3 | 1 |
| opening 规则页 with a stale group key | 2 × config/update-single | none |
| node/list p50/p95 | 5.99 / 8.34 ms | 5.88 / 8.11 ms |
| forward/list p50/p95 | 13.2 / 17.6 ms | 12.7 / 17.6 ms |
| panel log | 216 / 61 B/min | 217 / 61 B/min |
