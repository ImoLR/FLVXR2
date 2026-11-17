# 270 — 利群汇聚 page redesign (everything 利群 in one page)

Base: tag `3.0.27-fork.140` (+ plan-269 docs, `maintenance/3.0.27-fork.140-leikwan-step2` @ f57fc683).
Branch `feature/3.0.27-leikwan-redesign`, worktree `/root/projects/flvxx/flvxx-leikwan-redesign`.
Design approved by the user 11-17 ~05:00 TW (screens `/root/mmwx-preview/leikwan-redesign/`).
Panel only: no agent OTA, no DB migration.

## Decisions
- 「导入为规则」 tab removed (backend `import/*` kept).
- 利群专线 + 利群规则 only in 「利群汇聚」: `LeiKwanTunnelsSection` (隧道), `LeiKwanRulesSection` (规则),
  `LeiKwanUserGrantSection` (用户 · 隧道权限) removed; port skip (265), 节点 badge, plan-094 pipe handling stay.
- Old 服务概览 (raw LeiKwan forward table) + pipe create / takeover UI removed (backend kept).
- 设置 modal: 账号 / Token (old 账号设置) + global 日志快切 triggers and timings (read-only, as before).
  No new setting writes.
- 旧管道 card only when `leikwan_pipe` rows exist (prod: 0).
- Users: nav item 「利群汇聚」 only with an ACTIVE grant (new cheap flag `POST /api/v1/leikwan-rule/access`,
  one call per page load, cached in localStorage like the 订阅 switch); route redirects without a grant.
- Backend: non-admins without an active grant get no 利群 rules / lines / options (no vendor quota call);
  granted users get the active line list (id, name, route, transport) for 「可用专线」; users never see
  利群转发号, owner, 预热, 备线 forward ids (备线 error text replaced by a neutral note).

## Results (build)
- `go test ./...` (SQLite + PG 18 via FLVX_POSTGRES_TEST_DSN): 28/28 packages ok; go vet clean.
- New contract test `TestLeiKwanRedesignGrantFlagContract` (sqlite + postgres): flag for admin / no grant / active /
  disabled grant, no-grant list + options empty without a vendor read, granted user sees only own rules without
  admin fields, neutral 备线 error text, 「旧管道」 pipe view (id / etag / tunnelRules).
- `npm run build` ok, tsc clean, eslint 0 errors on touched files (layout warnings are pre-existing).
- Lab gallery http://104.145.236.29:8791/leikwan-redesign-built/ (34 PNG, 0 failed checks, 0 page errors):
  structure, chips, colours and wording match the approved screens 01–08; real data needs a wider rule row than the
  mock (app sidebar), so rule buttons use the design's 12 px btn-sm and 「待切换」 wraps as text.

## Checklist
- [x] Backend: `leikwan-rule/access` flag + list/options grant gate + user line list + neutral backupError
- [x] Contract tests: flag (no grant / active / disabled / admin), no-grant list/options empty, granted user sees only own rules
- [x] Frontend: remove sections from 隧道 / 规则 / 用户 pages
- [x] Frontend: admin page desktop (header chips, 专线·规则 tree, 授权 / 入口机 / 旧管道 cards, host drawer with step-2 controls, 设置 modal)
- [x] Frontend: admin H5 (chips, tabs 规则/授权/入口机 + 设置, ＋/− rows, one open)
- [x] Frontend: user page (header chips, 可用专线 filter cards, 我的利群规则 grouped, empty state, H5 chips + rows)
- [x] Frontend: nav flag (desktop sidebar + H5 menu) + route redirect
- [x] `go test ./...`, go vet, `npm run build`, eslint on touched files
- [x] Lab screens: admin / granted user / no-grant user; 1440 + 390 + 360 (long name); light + dark → gallery `/leikwan-redesign-built/`
- [x] Compare with the approved PNGs
- [ ] Commit, push, CI green
- [ ] Tag `3.0.27-fork.141`, Images + 10 assets, Latest
- [ ] Prod backup + rollback script, swap panel, prune backups (keep 2)
- [ ] Verify: nodes online, rule count, 利群 tables unchanged, page loads for admin
- [ ] Ledger row 270, memory notes, summary
