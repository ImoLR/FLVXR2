# 149 — 落地 page chips + 路由规则 waterfall + 3rd 大陆 forced seed (BUILT + SCREENSHOTS — waits for user approval)

Source: user 2026-10-24 voice messages (~16:50 TW chips / cards / links, ~17:40 + ~17:50 TW 大陆 seeds +
路由规则 layout). Brief: `/root/flvx-workers/runs/landing-chip-polish/brief.md`. Base:
`maintenance/3.0.27-fork.89-open-items-fix` (fork.89 = prod, 04e310cc). Branch
`feature/3.0.27-landing-chip-polish`. No tag, no release, no prod write, no OTA, no mmwx change.
Release waits for the user's screenshot approval.

## Checklist
- [x] 0  one tinted pill for 落地 (`landing/pill.tsx`), palette green/slate/purple/teal/pink(协议)/amber/red/cyan;
         replace every white/bordered chip on 落地 (cards, dialogs, admin table, egress tab, monitor tab)
- [x] 1  server cards: flow → 出站 (always visible) → ONE 协议 chip + popover (all protocols); admin source line tinted,
         no `+N` in normal data; equal card heights kept
- [x] 2  我的节点 cards: status chip, 剩余 N 天 / 长期有效 / 已过期 / 宽限期 chip, plug 连接数 chip, 规则 N, 出站;
         traffic progress bar; admin table 到期 column chip + plug icon
- [x] 3  links dialog: summary line, 直连 / 经规则转发 chips, `address:port` from the URL when cheap
- [x] 4a backend: rule matcher 「节点协议」 (`inboundProtocols`) → inbound tags of matching nodes (forced / default /
         profile rules); validation; node rules refuse it; signature includes it
- [x] 4b seeds: `seed-cn-direct` (source geoip:cn, 节点协议 ss2022) + all three enabled on fresh installs; preset
         「屏蔽中国大陆」 same three; global_seed_version 3 + preset_seed_version 2 one-time migrations
- [x] 4c exempt list: DDNS current IPs, agent-reported public IPs (new add-only columns), IPv6 node addresses as /64,
         30 s cache (shared loop runs every 5 s); re-sync on address change verified
- [x] 4d frontend: 节点协议 multi-select in admin rule editor (global / server scopes), summary chip, hidden in node
         rules; preset description
- [x] 4e shared servers: inbound-tag scoping check (no mmwx change) → note only if unsupported
- [x] 4f tests: compile scope, seeds + migration (switch-on, append, edited untouched, deleted not re-added, cap),
         exempt (DDNS / reported / /64), SQLite + PostgreSQL
- [x] 4g prod copy: plan diff before/after per server (only the 大陆 rules + exempt list)
- [x] 5  路由规则 dialog: numbered always-visible layers ①–⑥ with counts + meaning + connector, compact rows with
         详情 toggle (H5 collapsed), 内置 collapsed on H5
- [x] 6  tsc / build / lint vs base, bundle delta; go test vs base
- [x] 7  screenshots before/after (desktop 1440 + H5 390, light + dark) on a prod-DB copy, gallery on :8791
- [x] 8  push branch, CI green, final.md + summary.md

## Notes

### Palette (one tinted pill, `pages/landing/pill.tsx`)
- `Pill tone=…` (rounded-full, 12px semibold, tinted bg + dark-mode text) replaces every bridge Chip on 落地:
  green = online / 运行中 / 直连 / 剩余 > 7 天 / 长期有效 · slate = offline / 已停用 / neutral info ·
  purple = 单向/双向 · 倍率, 规则 N, 连接数, 经规则转发, 妙妙屋共享 · teal = 出站 / other outbounds ·
  **pink = 协议** (the only free hue that is neither blue-heavy nor amber/red) · cyan = FLVX 自有落地 (same accent
  as the monitor tab) · amber = warnings · red = errors / 封锁 / 已过期.
- Admin egress pages swap the import to `TintChip` (same props as the bridge Chip; bordered → slate). Monitor tab
  `default` tone (white, border-only) → slate. Rules page (chain-links / rule-target / chain-target) untouched.
- `ChipLine` gets `short` forms (「强制 IPv4」, 「协议 · 5」, 「自有落地」, 「内置 Xray」, 「Core 待升级」 …) used before
  anything folds; `+N` stays only as a last-resort fallback (slate pill now).

### 4 — 大陆 seeds + 节点协议
- `Rule.inboundProtocols` (omitempty → stored JSON of unscoped rules unchanged), keys = landing protocols
  `ss2022, vless_reality_vision, anytls, snell, mieru` (no Hysteria2 / TUIC / Trojan / plain SS exist in the panel).
  SS family = `ss2022` only. Compiled into inbound tags (forced, 全局·默认, profile rules, ∩ 适用落地节点); chain
  copies never get scoped rules; no node of that protocol → rule not emitted. Node rules refuse it; scoped presets
  are admin-only. `RuleSignature` / frontend `ruleSignature` add the scope only when set.
- Seeds: `seed-cn-ip`, `seed-cn-site`, `seed-cn-direct` (source geoip:cn, 节点协议 SS2022), all enabled; preset
  「屏蔽中国大陆」 = same three + new description. Migrations: global marker 2→3 (switch on unedited seeds, add
  direct after them unless same signature exists / both cn seeds deleted / list full; edited seeds kept; logged),
  preset marker 1→2 (replace the preset rules only while they equal the pre-149 seed; description only if unchanged).
- Exempt list: + agent-reported public IPs (new add-only `node.reported_ip_v4/v6`, written only on change), + DDNS
  records' `current_ip`, node IPv6 exempt as /64 (privacy / SLAAC), 30 s cache (shared push compiles every 5 s),
  dropped on whitelist save and DDNS address change. Re-sync on address change = existing loops (native 1 min,
  shared 5 s) → ≤ ~1.5 min.
- Shared servers: per-rule `inbound_tags` already carry node rules there (landing-egress-v1, mmwx renders
  `inboundTag` per rule, exempt CIDRs accepted) → 禁止大陆直连 applies on shared SS nodes too; no 「不支持」 note.
- Prod copy (fork.89 DB, 10-24 12:32Z): plans change on servers 3 / 5 / 6 only (the servers with nodes): +1 rule
  「禁止大陆直连」 on the SS node tags (3: flvxl-10/95, 5: flvxl-97, 6: flvxl-105) + `exempt_sources` (33 CIDRs);
  every other plan field identical; servers 1 / 2 / 4 no plan before and after. Migration log: enabled [] (both
  already on), direct added, preset updated.
- Forwarded traffic: only forward 156 (node 48 → PQS TW node 105 SS2022) targets a landing directly; node 48 dials
  pqstw (A only, 36.232.0.161) over IPv4 NAT egress 114.35.6.24 (read via SSH) = not geoip:cn → unaffected.
  Mainland node addresses (52, 53/54, Mkcloud 2/22/23/32, IPv6 of 20/48/50/51) are all in the exempt list.

### 5 — 路由规则 waterfall (`pages/landing/rule-layers.tsx`, `node-rules.tsx`)
- ① 内置 (slate) ② 全局·强制 (purple) ③ 我的规则 (green) ④ 服务器规则 (teal) ⑤ 全局·默认 (cyan) ⑥ 默认出站 (pink):
  numbered dot + connector line, count pill (「N 条」 / 「暂无」), one-line meaning; every layer always shown (empty
  layers say why: 管理员没有设置… / 全局规则暂未应用…). Locked rows = lock + one-line name (title = full name) +
  action pill + 「详情」 toggle (collapsed on H5, open on desktop). 内置 on H5 = one line per rule + 「展开详情」 to
  the unchanged `BuiltinRuleList`. 我的规则 keeps `EgressRuleList` (editing unchanged) with 详情 folded on H5.
  The compact LayerLegend row is dropped from this dialog (the headers are the legend); data, order, save unchanged.
- A node's view lists only the global / server rules that apply to its protocol (VLESS nodes do not list
  禁止大陆直连).

### 6 — gates
- tsc 0 errors (base 0); `vite build` OK; changed files lint 0 errors / 0 warnings (whole `src`: base 96 errors /
  3754 warnings, all pre-existing in untouched files). Bundle: total 4,267,753 → 4,286,141 B (+18 KB), main
  index.js 3,001,333 → 3,002,390 B (+1 KB, 2.86 MiB < 5 MiB PWA limit), CSS +7.4 KB, egress chunk +9.9 KB.
- `go test ./...`: same failures as the unmodified base (10 top-level + 5 subtests: TestBackupExportImportRestore,
  TestBuildFederationServiceConfig_NonTLSProtocol_NoNodelay, 3 × FederationDualPanel, 2 × OpenMigrates*Legacy*,
  TestRunNodeRenewalCycleJob_AdvancesOverdueAnchorTimes, TestServiceMonitorResults,
  TestTunnelUpdateRecoversFromAddressInUseContract). New/updated landing tests pass on SQLite and PostgreSQL
  (local PG 18 + CI 「Go Backend PostgreSQL Contract」).
- Handler tests written for the pre-149 fresh install keep it (`newFlowTestEnv` disables the seeds);
  `newSeededFlowTestEnv` + `TestLandingSeededForcedSent` / `TestLandingCNDirectScopedToSS` cover the shipped state.

### 7 — screenshots
- Prod DB read-only copy (python sqlite3 online backup, credentials scrubbed), demo panel 127.0.0.1:16365 in an
  offline netns, before = fork.89 binary + dist, after = this branch; Chromium in a 650 M scope. 140 shots (68 before
  / 72 after), 164/164 automatic checks (no bridge badge / white pill on 落地, no clipped pill text, equal server
  card sizes, no 「+N」 on any real prod card — user and admin, desktop and H5), 0 page errors, 0 write requests.
  Link credentials masked in the browser. Gallery: `:8791/<token>/landing-chip-polish/`.
- Demo-only data: user 26 also granted servers 4 / 6, three extra nodes (≤ 7 天, 宽限期, 长期 — 长期 via response
  mock because the user's own expiry caps every node), offline 「正在同步」 shown as 正常, 路由规则 (a)/(c) mocks.

### 8 — release notes (for the release worker, after the user approves)
- Panel only, no agent / OTA, no mmwx change. Startup migration (global marker 3, preset marker 2) runs once and sets
  the resync flag → every landing server re-pushed once; plans change only by 禁止大陆直连 on SS nodes + exempt list.
- After release: 屏蔽大陆 IP / 网站 stay on (already on in prod), 禁止大陆直连 newly blocks DIRECT mainland clients of SS
  nodes (all 4 prod landing nodes are SS2022). Verify forward 156 (node 48 → PQS TW) keeps working and that
  `node.reported_ip_v4/v6` fill in as agents reconnect (exempt list grows).
- Rollback to fork.89 needs `--restore-db` only if the 3rd rule must disappear (fork.89 would keep it as an unscoped
  rule → blocks direct CN to every node incl. VLESS); new columns are add-only.
