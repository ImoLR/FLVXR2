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
