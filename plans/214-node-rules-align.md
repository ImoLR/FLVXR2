# 214 — 落地节点「路由规则」: every layer uses the 内置 layer's UI (build only)

User feedback 2026-11-05 (phone): in 我的节点 → 路由规则 the 内置 layer looks right
(one clean row per rule, ✓/✗ at the far right, one 「展开详情」 toggle in the layer
header). 全局·强制 does not match: a 「详情」 button on every row that only reveals
「目标域名 2」, and on a 390 px phone the outbound chip (「→ 直连-强制ipv4出站」) squeezes
the rule name (「放行谷歌/苹果」) to nothing. Wanted: 全局·强制, 我的规则 and every
other layer of the dialog use the 内置 pattern.

Base: tag `3.0.27-fork.115` (9a4704b1). Branch `feature/3.0.27-node-rules-align`.
Pure frontend restyle: no API / DB / rule-semantics / compile change. Build only, no
release. Avoid 211's files (node-details, server-settings, shared-caps, user-settings).

## Design
- One row component `LayerRuleRow` (rule-layers.tsx) for every layer: left icon
  (🔒 locked / faded 🔒 overridable / rule number for 我的规则), then a wrapping line
  with the rule name, its action chip (tinted, flows after the name and wraps under it
  on narrow screens, never positioned over text) and a one-line gray summary, and the
  ✓ / ✗ state icon at the far right. Nothing is truncated to zero width.
- One 「展开详情 / 收起」 toggle per layer header (desktop + H5, collapsed by default).
  Expanded = a small label/value list under each row: matchers (目标域名 / 目标 IP /
  目标端口 / 来源 IP / 来源端口 / 网络 / 协议 / 节点协议 / 适用节点) and 「命中后」 in plain
  words. 我的规则 shows the real values (the dialog has the full rules); read-only
  layers show what the API sends (counts).
- 内置 uses the same rows (collapsed = today's compact rows; expanded = the same rows
  with port/protocol, explanation and the CIDR list instead of the separate big list).
- 我的规则 keeps every control (预设, 添加规则, 启用开关, 上移/下移/置顶/置底, 编辑, 删除,
  H5 「更多」 menu) through an opt-in `layout="layer"` prop on EgressRuleList; the
  admin 全局规则 page and the preset manager keep their current look.

- Chained nodes (plan 177, only block rules apply): non-block rows show a red ✗
  「未生效：已开启链式代理，只有封锁规则生效」 instead of a misleading ✓.

## Results (build)
- tsc 0 errors; eslint `src/pages/landing`: 0 errors, the same 3 pre-existing
  warnings as base (enable-button.tsx); `npm run build` ok.
- Chunks (raw / gzip): egress-rule-list 52.20 → 57.81 kB (15.55 → 17.08), landing
  87.93 → 87.19 kB (26.36 → 26.24), egress 39.26 → 39.25 kB, css 249.95 → 249.77 kB.
  Net ≈ +4.9 kB raw / +1.4 kB gzip. No new state polling, no new requests.
- Demo (fork.115 paneld, synthetic DB like prod, loopback netns): 46 shots,
  129/129 checks — 390 px: name / chip / ✓ never intersect, no name truncated,
  no horizontal overflow, 0 per-rule 「详情」 buttons, one toggle per non-empty
  layer; controls kept (switch, 置顶…置底, 编辑, 删除, H5 「更多」, 预设, 添加规则);
  admin 出站与路由 page pixel-identical (5 sections). Before (fork.115, H5):
  「放行谷歌/苹果」 squeezed to 84 px (user) / 36 px (admin) and cut off.
- Gallery: http://104.145.236.29:8791/node-rules-align/
- Merges cleanly with 211 / 212 / 213 (git merge-tree, 0 conflicts); none of 211's
  files touched.
- Open: read-only layers show matcher counts only (API sends counts; showing values
  needs a backend change + a decision whether users may see global rule contents);
  desktop now defaults to collapsed like H5. Release waits for screenshot approval.

## Checklist
- [x] Worktree + branch from fork.115, plan doc
- [x] Baseline: base frontend build (chunk sizes), tsc, lint
- [x] `LayerRuleRow` + details list + layer 详情 toggle in rule-layers.tsx
- [x] 内置 layer rows (collapsed/expanded) on the shared row
- [x] 全局·强制 / 服务器 / 全局·默认 on the shared row, per-layer toggle
- [x] 我的规则: EgressRuleList `layout="layer"` (all controls kept)
- [x] node-rules.tsx wiring (admin + user, desktop + H5)
- [x] tsc / build / lint: no new errors vs base; chunk size delta
- [x] Demo panel seeded like prod + before/after screenshots (H5 390 / desktop 1440,
      light/dark, collapsed/expanded, user/admin), 390 px overlap check
- [x] Gallery /root/mmwx-preview/node-rules-align/ (index.html, pairs side by side)
- [x] Commit, push, CI green (316c59fe, CI 41999004686 success)
- [x] RELEASE-LEDGER row (built — screens pending user approval)
- [x] Cleanup demo panel / browsers; final.md + summary.md (runs/node-rules-align)

## r2 (2026-11-05 evening) — restore the ORIGINAL 内置 row; every layer one line

User 11-05 ~17:30 TW after the r1 screens: the original 内置 UI (fork.115) is the
reference and must not change; one rule = one line with an ellipsis, never wrap;
details only via the layer header 「展开详情」; desktop same style.

Approach:
- `egress-builtin-list.tsx` + `scan-guard.tsx` restored byte-for-byte to fork.115;
  node-rules wires 内置 exactly like fork.115 (collapsed = `BuiltinCompactRows`,
  expanded = `BuiltinRuleList`). `LayerStep` header restored to fork.115.
- Other layers: an exact clone of the `BuiltinCompactRows` row
  (`grid-cols-[14px_minmax(0,1fr)_auto_auto] items-center gap-2 px-3 py-2`,
  truncate name, short capped `shrink-0` chip, ✓/✗). No collapsed summary line;
  matchers + 「命中后」 only under 展开详情 (r1 detail list kept).
- 我的规则: same row + h-5 Switch + compact 「更多」 menu (编辑, 置顶…置底, 删除) on
  desktop and H5, so the row stays one line at the 内置 height. On H5 (390 px) the
  switch + 「更多」 leave no room for the action chip (name was squeezed to 0 px), so
  我的规则 drops the chip on phones only; the action is in 展开详情 (「命中后」).
  Other layers keep the chip (capped 7.5rem / 12rem, truncates; name gets room first).
- Headers of 全局·强制 / 我的规则 / 服务器 / 全局·默认: on phones the hint goes under the
  title so the toggle stays top-right (`hintBelow`); 内置 header = fork.115.
- Default state: fork.115 desktop always showed 内置 expanded, so desktop starts
  expanded (all layers), H5 collapsed; every layer still has its header toggle.

### r2 checklist
- [x] Restore 内置 files + wiring + LayerStep header to fork.115
- [x] One-line clone row for 全局·强制 / 服务器 / 全局·默认 (details under 展开详情)
- [x] 我的规则 one-line row with Switch + 更多 menu (desktop + H5)
- [x] Default open state (desktop expanded / H5 collapsed)
- [x] tsc / build / lint vs fork.115; bundle delta
- [x] Demo screenshots fork.115 vs r2 (内置 pair first), one-line/overlap/scroll auto-check
- [x] Gallery regenerated; commit, push, CI green (code 49e84b03, CI 42009005072 success)
- [x] Ledger row → built r2; cleanup; final.md + summary.md

r2 results: tsc 0 errors, eslint no new warnings (3 pre-existing in enable-button.tsx),
build ok. Bundle vs fork.115: +4.77 kB raw / +1.02 kB gzip (egress-rule-list +7.6,
landing −3.2, css +0.3). Demo 148/148 checks, 0 page errors, 76 shots: 内置 fork.115 vs r2
pixel-identical (H5 default + expanded, desktop list box; desktop header only gains 「收起」;
markup byte-identical), every collapsed row height == 内置 row (36 px + divider), no
overlap / no horizontal scroll at 390 and 1440, admin 出站与路由 sections pixel-identical.

## r3 (2026-11-05 ~19:00 TW) — 我的规则 collapsed row = 全局·强制 row; controls into 展开详情

User after the r2 screens: r2 approved except 我的规则. Collapsed 我的规则 rows must look
exactly like 内置 / 全局·强制: number + name (ellipsis) + coloured action chip + ✓/✗, one
line, same height, phone and desktop. No switch and no 「⋯」 on the collapsed row; both
move into the layer's 「展开详情」. Disabled rule → ✗ + dimmed, chip stays.

Approach:
- `LayerRuleRow`: drop the `controls` slot; new `actions` slot rendered at the end of
  the row's details block (only when the layer is expanded).
- `EgressRuleList` layer layout: chip on H5 too (same capped `Pill` as 全局·强制);
  per-rule action line under 「命中后」: Switch 「启用」 + flat pills 编辑 / 上移 / 下移 /
  置顶 / 置底 / 删除 (moves hidden at the ends, as the r2 menu did). 删除 asks inline
  (「确认删除」 / 「取消」) — r2 had no confirm; still buffered until the dialog 「保存」.
  Move pills are text only (编辑 / 删除 keep their icons) so the 7 actions fit 2 lines
  at 390 px (with icons they took 3).
  The pills wrap inside the details block on 390 px; collapsed rows stay one line.
- 添加预设 / 添加规则 stay above the list. Other layers / 内置 / admin page unchanged.

### r3 checklist
- [x] LayerRuleRow: controls slot → actions slot in details
- [x] 我的规则: chip on H5, action line (启用 switch + pills + inline delete confirm)
- [x] tsc / build / lint vs r2 + fork.115; bundle delta vs fork.115
- [x] Demo r2 vs r3: 我的规则 pairs (390/1440, light/dark, collapsed/expanded, disabled ✗)
- [x] Auto-checks: row height == 全局·强制, chip visible 390, no switch/⋯ collapsed,
      actions work, no overlap / h-scroll; other layers + admin page 0 px diff vs r2
- [x] Gallery: r3 pairs first, older sections below
- [ ] Trial merge onto 3.0.27-fork.116 (not pushed), report conflicts
- [ ] Commit, push, CI green
- [ ] Ledger row → built r3; cleanup; final.md + summary.md
