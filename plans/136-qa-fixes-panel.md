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
- [ ] F11 stale 「在线」 after panel restart: reset local nodes offline before serving, agent connect → online
- [ ] F12 tunnel delete migration: target list = tunnels every rule owner may use; backend re-checks per owner
      (also forward batch change-tunnel)
- [ ] F15 LeiKwan: DB pipes missing on LeiKwan side listed as 「利群侧已删除」 + existing cleanup actions
- [ ] F16 opening 规则页 never writes config; R10 no pruning of off-page group preferences
- [ ] F17 home/rules polling 5 s → slower + paused when hidden; admin WS volume reduced (measure bytes/min)
- [ ] F09 「Powered by FLVX」 overlay no longer covers the 24 h chart axis on first screen
- [ ] F18 useless requests per page (app_favicon, config/list ×3, check-updates)
- [ ] F19 node modal: validation errors visible; invalid IPv4-looking hosts rejected
- [ ] F20 landing node name: input length limit + correct message
- [ ] F21 rule port out-of-range message wording
- [ ] F22 diagnose: no stale 「诊断中」 line after the result
- [ ] F23 node delete refusal names the tunnel(s)
- [ ] F24 two-tab user edit: optimistic version check
- [ ] F25 over-limit user: hint on 主页
- [ ] F26 login timing: dummy bcrypt for unknown users; disabled account does not confirm password
- [ ] F27 `node/info` with arbitrary token → 401 (no nil pointer)
- [ ] F28 plan 134 user rule editor: default outbound matches label; cancel with unsaved rules asks
- [ ] R8 escape `:`/`@` in AnyTLS/Mieru share link userinfo
- [ ] R11 24 h chart tick
- [ ] R6 / R9 investigate; fix if contained, else list under 「还没解决」

### Tests
- [ ] Contract/unit tests: F11, F12, F15, F16, node/info 401, login timing, F24, R8
- [ ] Full backend suite after (no new failures vs base)
- [ ] Frontend tsc / lint / build
- [ ] Relevant `scripts/qa-sweep/` checks against the local build; screenshots as evidence for UI nits
- [ ] Resource numbers before/after (CPU, RSS, WS bytes/min, request rate, list p50/p95, log bytes/h)

### Release
- [ ] Commit + push branch, CI green
- [ ] Tag `3.0.27-fork.76`, images + release assets verified
- [ ] Prod backup `pre-fork76-<TS>` + prune (keep 2)
- [ ] Upgrade prod panel (no OTA), health + 29 nodes
- [ ] Prod read-only checks: node status = live connections, #318 listed as 「利群侧已删除」, 规则页 opens
      without POST config, polling interval in nginx log
- [ ] Summary → run dir summary.md; plan marked complete
