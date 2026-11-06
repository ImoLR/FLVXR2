# 219 — Drop the landing source-IP exemption + 来源白名单 (build only)

Branch `feature/3.0.27-landing-exempt-drop` from `3.0.27-fork.117`. Run dir
`/root/flvx-workers/runs/landing-exempt-drop`. Panel only, no OTA, no DB change.

## Why
Node 48 (Leikwanhost 沪台IPLC) leaves through two NAT IPv4s. The agent
re-detects its public IP on every WS reconnect, the panel overwrites
`node.reported_ip_v4`, and that column fed the automatic exempt list
(`exempt_sources`) of every landing plan that has a source-IP rule. Every
flip changed the plan hash on all landing servers → revision bump + push.

User decision 11-05: the exemption is not wanted at all (禁止大陆直连 only
lives on landings outside the wall; relayed traffic always arrives from an
overseas exit; a mainland box connecting directly must be blocked). Remove
the manual 来源白名单 too. Make sure nothing else flips the plan.

## Checklist
- [x] 1. landingegress: dropped `CompileInput.Exempt`, `Plan.ExemptSources`,
      `MaxExemptSources`, `MaxWhitelistEntries`, `NormalizeWhitelist`;
      exempt.go → nodeaddr.go `NodeAddressPrefixes` (chain guard only).
- [x] 2. handler: dropped `landingExemptSources`, the exempt cache fields,
      `invalidateLandingExempt` (+ the DDNS call), every `in.Exempt =`,
      overview `whitelist` / `exemptNodeCount` / `exemptPreview`, route
      `whitelist/save`. `egressResolve` stays (chain guard, external nodes).
- [x] 3. repo: dropped `GetLandingSourceWhitelist` / `SaveLandingSourceWhitelist`;
      `LandingExemptHosts` → `NodeGuardHosts` (chain guard). Column
      `landing_egress_setting.source_whitelist` stays, unused (prod `[]`).
- [x] 4. reported_ip_v4/v6: still read by the chain-upstream guard
      (`chainForbidden` → 外部节点 / 链式代理 paste, import, test). It covers
      19 nodes whose IPv6 /64 is in no address field. Writes kept (only on
      change); the columns no longer feed any landing plan.
- [x] 5. Frontend: removed the 内置 「来源白名单」 row, the 来源豁免 card, the
      API call and overview fields (4 files, deletions only + comment).
- [x] 6. Agent / Helper: `exempt_sources` was `omitempty`; absent = the
      agent renders `sourceIP` as given (`renderRules`, empty exempt set), the
      Helper already gets plans without the field. No agent/Helper change, no OTA.
- [x] 7. Determinism audit (see final.md). Second flip source fixed:
      native `relay_sources` (plan 158, in the `ul=` signature) lost a node's
      reported interface addresses while it was offline → 2 revision bumps
      on every native landing per real disconnect of 19 nodes. Now the last
      report is kept (`ddnsManager.lastAddrs`).
- [x] 8. Tests: `landing_plan_stable219_test.go` (reported-IP flips, address
      edits, DNS, DDNS create/rotate, new node → same plan hash; whitelist
      route 404 + overview fields gone; relay sources survive offline, real
      change applied). Reverse-checked: old relay logic fails the test.
- [x] 9. go-backend `go test ./...` 25/25 ok; tsc 0 errors; vite build ok;
      eslint clean on touched files.
- [x] 10. Prod effect on a COPY of prod gost.db (base vs branch): only the 60
      exempt entries vanish on 5 landings, everything else identical;
      禁止大陆直连 still SS-only; mainland last-hop check: 6 rules via node 48
      (mainland IPv6) but all targets are IPv4-only → reach via TW exit.
- [x] 11. Push, CI, ledger row, final.md / summary.md.
