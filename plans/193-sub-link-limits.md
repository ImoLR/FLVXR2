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
- [x] Baseline: fork.107 generator allocs/ms at 100 nodes (all formats)
- [x] A1 100-node cap: model + sublink.MaxItems, save validation, render stops at 100, editor cap UI
- [x] A2 source cap 100 + early-stopping parser (lines + Clash proxies-only), truncation hint
- [x] A2 body cap decision measured (realistic 100-node Clash YAML)
- [x] A4 Clash generator low-alloc, golden byte-identical (fixtures + 100 nodes + tricky names vs fork.107)
- [x] A4 sing-box check / fix
- [x] A5 cold-render slot limit (503 quick), refetch outside the slot
- [x] B Snell: 复制链接 Surge line omitted for v4/v5 + tls obfs (with reason); generators verified
- [ ] Tests: unit + contract SQLite + PostgreSQL; frontend tsc/build/lint vs base
- [ ] A6 demo panel: peak RSS, 20 distinct 100-node subs cold, before vs after
- [ ] Screens of changed UI text → /root/mmwx-preview/sub-link-limits/
- [x] Prod read-only counts (subs/sources >100, Snell landing nodes)
- [ ] Push, CI green
- [ ] Release fork.108: baselines, backup, upgrade, post-checks, prune, cleanup
- [ ] Plan marked complete

## Notes
- Generators, 100 mixed nodes (go test -bench Render100, GOMAXPROCS=2), fork.107 → plan 193:
  clash 16.0 ms / 18.1 MB / 68.5k allocs → 0.27 ms / 0.157 MB / 1.9k; stash 15.3 ms / 17.2 MB → 0.30 ms / 0.149 MB;
  sing-box 0.83 ms / 0.34 MB (unchanged, under target); base64 0.20 ms / 0.15 MB; surge 0.08 ms / 0.12 MB;
  surfboard 0.09 ms / 0.13 MB; loon 0.13 ms / 0.13 MB; qx 0.14 ms / 0.13 MB (unchanged code).
- Clash writer = port of yaml.v3 v3.0.1 scalar rules (+ the `!!merge <<` quirk); fallback to yaml.v3 for line
  breaks / invalid UTF-8. Checked vs the fork.107 encoder: goldens incl. 100-node files written at fork.107
  (commit 26751e10), all protocols with tricky strings, 20k random strings, 90 s native fuzz (90k execs).
- Source parser (BenchmarkParseAirport, MaxNodes 100): 100 nodes + 3k rules (268 KB) 12.1 ms / 3.5 MB → 5.7 ms / 1.5 MB;
  300 + 8k (741 KB) 34.8 ms / 9.8 MB → 7.9 ms / 2.1 MB; 500 + 20k (~1.6 MB) 57.9 ms / 20.0 MB → 6.6 ms / 3.0 MB;
  base64 500 links 4.0 ms / 1.1 MB → 0.9 ms / 0.44 MB.
- Realistic 100-node airport Clash YAML: 76 KB (no rules), 268 KB (3k rules), 588 KB (8k rules). Body cap kept
  at 2 MiB: big embedded rule sets exist and, with the proxies-only parse, body size no longer drives memory
  (~1 copy of the body); fetch buffer pre-sized from Content-Length.
- 链式代理 import keeps 500 entries (only ≤20 picks stored; a 100 cap would hide nodes of big airports), same parser.
- Snell: Surge manual (policies/snell.html): v1–v3 http|tls, v4/v5 http only, v6 none. Surfboard docs
  (external-proxy/snell): obfs tls|http, versions 1–4 (5 treated as 4). mihomo wiki (proxies/snell): v1–v5,
  obfs-opts http/tls/shadow-tls/restls/jls. FLVX Snell server = mmwx Core proxy/snell (sing-snell obfs port,
  none/http/tls for v4/v5; Helper advertises obfs none/http/tls) → landing form unchanged. Generators already
  followed this (Surge skips v4/v5+tls, Surfboard keeps it, Clash obfs-opts tls); only 复制链接's Surge line
  was wrong.
- Prod read-only (11-02 ~22:05 TW): 0 subscriptions, 0 sources, 0 Snell landing nodes (5 SS2022 nodes; 6 landing
  servers default Snell v4 / none) → nothing over 100, nothing to trim.
