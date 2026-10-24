# 149 — 落地 page chips + 路由规则 waterfall + 3rd 大陆 forced seed (BUILD + SCREENSHOTS, no release)

Source: user 2026-10-24 voice messages (~16:50 TW chips / cards / links, ~17:40 + ~17:50 TW 大陆 seeds +
路由规则 layout). Brief: `/root/flvx-workers/runs/landing-chip-polish/brief.md`. Base:
`maintenance/3.0.27-fork.89-open-items-fix` (fork.89 = prod, 04e310cc). Branch
`feature/3.0.27-landing-chip-polish`. No tag, no release, no prod write, no OTA, no mmwx change.
Release waits for the user's screenshot approval.

## Checklist
- [ ] 0  one tinted pill for 落地 (`landing/pill.tsx`), palette green/slate/purple/teal/pink(协议)/amber/red/cyan;
         replace every white/bordered chip on 落地 (cards, dialogs, admin table, egress tab, monitor tab)
- [ ] 1  server cards: flow → 出站 (always visible) → ONE 协议 chip + popover (all protocols); admin source line tinted,
         no `+N` in normal data; equal card heights kept
- [ ] 2  我的节点 cards: status chip, 剩余 N 天 / 长期有效 / 已过期 / 宽限期 chip, plug 连接数 chip, 规则 N, 出站;
         traffic progress bar; admin table 到期 column chip + plug icon
- [ ] 3  links dialog: summary line, 直连 / 经规则转发 chips, `address:port` from the URL when cheap
- [ ] 4a backend: rule matcher 「节点协议」 (`inboundProtocols`) → inbound tags of matching nodes (forced / default /
         profile rules); validation; node rules refuse it; signature includes it
- [ ] 4b seeds: `seed-cn-direct` (source geoip:cn, 节点协议 ss2022) + all three enabled on fresh installs; preset
         「屏蔽中国大陆」 same three; global_seed_version 3 + preset_seed_version 2 one-time migrations
- [ ] 4c exempt list: DDNS current IPs, agent-reported public IPs (new add-only columns), IPv6 node addresses as /64,
         30 s cache (shared loop runs every 5 s); re-sync on address change verified
- [ ] 4d frontend: 节点协议 multi-select in admin rule editor (global / server scopes), summary chip, hidden in node
         rules; preset description
- [ ] 4e shared servers: inbound-tag scoping check (no mmwx change) → note only if unsupported
- [ ] 4f tests: compile scope, seeds + migration (switch-on, append, edited untouched, deleted not re-added, cap),
         exempt (DDNS / reported / /64), SQLite + PostgreSQL
- [ ] 4g prod copy: plan diff before/after per server (only the 大陆 rules + exempt list)
- [ ] 5  路由规则 dialog: numbered always-visible layers ①–⑥ with counts + meaning + connector, compact rows with
         详情 toggle (H5 collapsed), 内置 collapsed on H5
- [ ] 6  tsc / build / lint vs base, bundle delta; go test vs base
- [ ] 7  screenshots before/after (desktop 1440 + H5 390, light + dark) on a prod-DB copy, gallery on :8791
- [ ] 8  push branch, CI green, final.md + summary.md

## Notes
