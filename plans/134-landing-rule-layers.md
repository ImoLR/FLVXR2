# 134 — landing-rule-layers: 全局·强制 / 节点规则 (user) / 服务器 / 全局·默认

Base: `feature/3.0.27-landing-node-limits` (plan 131 966386da, on plan 132 = fork.72 content, on fork.71). Branch
`feature/3.0.27-landing-rule-layers`. Build + tests + screenshots only — no release, no tag, no prod change, no OTA
(waits for the user's screenshot approval; brief /root/flvx-workers/briefs/landing-rule-layers.md).

## Goal (user 2026-10-22: 「可以就按你建议的这么做吧 … 逻辑你自己补齐一下」)
Landing routing gets layers like mmwx's global vs node split: a global group applied automatically to every landing
(native + mmwx-shared, incl. newly enabled), and node rules each user adds on their own landing nodes.

Evaluation order per node (first match wins, every rule scoped by inbound tag):
1. 系统内置 (locked, unchanged): safety, plan 127 anti-abuse defaults, source exemptions
2. 全局·强制 (admin) — every landing node incl. line-chain copies
3. 节点规则 (node owner; admin may edit) — own inbound tag only, not on line-chain nodes
4. 服务器规则 (admin) — the server's route profile, or the admin node override (now replaces only this layer)
5. 全局·默认 (admin) — every non-chain node
6. 默认出站 — node owner's choice > admin override profile default > server profile default (server has a profile)
   > 全局默认出站; line-chain nodes keep their own upstream

## Tasks
- [x] Plan doc + baseline backend suite on the unmodified base (15 FAIL, same list as plan 131:
      /root/flvx-workers/runs/landing-rule-layers/base.fails)
- [ ] Models: `landing_egress_setting` global columns (强制/默认 rules text, 全局默认出站); `landing_outbound.user_allowed`;
      `landing_node.route_rules` / `route_rule_count` / `route_default`; `landing_user_setting` node-rule caps
      (value + custom flag ×2); global caps in vite_config (5 / 10); SQLite + PostgreSQL, no jsonb/serial
- [ ] Compiler: layered `Compile` (L2 forced, L3 node, L4 server, L5 default, L6 default outbound precedence);
      byte-identical output when global is empty and no node rules (all existing goldens unchanged);
      line-chain tags get only L1+L2 (agent marker `builtin: "forced"`, landing_guard ≥ 2); global layers fail-open
      (`builtin: "global"`); WARP failure in a global layer → 直连 + warning; IPv4/IPv6-forced global part skipped on a
      server without that family + warning; node rules over the server budget skipped per node + warning
- [ ] Agent (go-gost): GuardVersion 2 — accepts `builtin` forced/global (any outbound), forced rendered before chain
      rules, builtin rules never withhold nodes; tests
- [ ] Handler/repo: global get/save (validate every server, debounce one apply per server), per-server sync status
      (已同步 / 同步中 / 失败 / 不支持（原因）/ 待重启 / warnings); node-rule get/save (owner or admin; caps + rate limit in
      one tx with the user row locked; non-published outbound refused; private-range still blocked by layer 1);
      owner transfer moves rules (refuse over target caps); node delete drops rules with the row; WARP ensure + upstream
      resolution + loop edges include global and node layers; user caps in landing user get/save (跟随全局/自定义)
- [ ] Native routing-only change stays hot (RoutingService `adrules`), never a Core restart for node rules; servers
      whose Core still needs the one-time restart keep node rules queued (「下次重启生效」) and global layers wait like
      the abuse defaults
- [ ] Frontend: 出站与路由 tab 「全局规则」 section (强制/默认 lists, 全局默认出站, sync status, 用户规则 caps, 匹配顺序
      legend); outbound 「允许用户使用」 checkbox; 路由规则 dialog (user node card + admin node details); user edit 「落地」
      two cap fields
- [ ] Golden tests (layer order, line-chain, node scope, default precedence, empty global identical, IPv4-forced on
      IPv6-only, WARP fallback) + contract tests SQLite + PostgreSQL (caps, 20 parallel saves, authz, unpublished
      outbound, transfer, delete, global save → one apply per server)
- [ ] Acceptance runner (local lab, real Core): global 强制 blocks a domain on two servers incl. a newly enabled one;
      user A 封锁 rule vs user B on the same Core; B's live connection survives A's edit; private block still wins
- [ ] Resource numbers: Core RSS + apply time 0 vs (10 users × 10 node rules + 20 global incl. geosite/geoip); panel
      RSS/CPU before/after
- [ ] Backend suite after (no new failures), frontend tsc / lint / build, go-gost build/tests
- [ ] Screenshots desktop + H5, light + dark; gallery on mmwx-preview `/landing-rule-layers/`
- [ ] Push branch, CI green, summary + final.md
