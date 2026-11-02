# 193 · sub-link-limits — 订阅防撑爆（100 节点上限 + 低内存）+ Snell Surge 修正

User 2026-11-02 (coordinator 6f979d30): limit user subscriptions so nobody can blow up panel
memory with lots of nodes (100 nodes is enough, low memory), and fix the Snell Surge line. Release
right away (fork.108, panel only, no OTA). Base = `3.0.27-fork.107` (db582010), branch
`feature/3.0.27-sub-link-limits`, worktree `/root/projects/flvxx/flvxx-sub-link-limits`.

## Decisions
- 100 nodes per subscription (saved items AND rendered output; `source_all` counts with its node count).
- External subscription source: keep the first 100 usable nodes; parse stops early.
- Landing 链式代理 one-shot import keeps its 500 limit (stores only ≤20 picks; a 100 cap would hide
  nodes of big airports) but uses the same cheap, early-stopping parser.
- Clash generator rewritten without the yaml.v3 encode→parse round trip, byte-identical output.
- Cold renders: small global slot limit, short wait, quick 503.
- Snell: Surge v4/v5 = obfs http only (manual.nssurge.com/policies/snell.html); Surfboard = tls/http
  (getsurfboard.com docs); mihomo = http/tls, versions 1–5 (wiki.metacubex.one). FLVX Snell server =
  mmwx Core `proxy/snell` (sing-snell port) supports none/http/tls for v4/v5 → form unchanged.

## Checklist
- [ ] Baseline: fork.107 generator allocs/ms at 100 nodes (all formats)
- [ ] A1 100-node cap: model + sublink.MaxItems, save validation, render stops at 100, editor cap UI
- [ ] A2 source cap 100 + early-stopping parser (lines + Clash proxies-only), truncation hint
- [ ] A2 body cap decision measured (realistic 100-node Clash YAML)
- [ ] A4 Clash generator low-alloc, golden byte-identical (fixtures + 100 nodes + tricky names vs fork.107)
- [ ] A4 sing-box check / fix
- [ ] A5 cold-render slot limit (503 quick), refetch outside the slot
- [ ] B Snell: 复制链接 Surge line omitted for v4/v5 + tls obfs (with reason); generators verified
- [ ] Tests: unit + contract SQLite + PostgreSQL; frontend tsc/build/lint vs base
- [ ] A6 demo panel: peak RSS, 20 distinct 100-node subs cold, before vs after
- [ ] Screens of changed UI text → /root/mmwx-preview/sub-link-limits/
- [ ] Prod read-only counts (subs/sources >100, Snell landing nodes)
- [ ] Push, CI green
- [ ] Release fork.108: baselines, backup, upgrade, post-checks, prune, cleanup
- [ ] Plan marked complete

## Notes
