# 231 — IPv6 按 /64 算一个 IP（用户总 IP 数 / 规则 IP 数，和妙妙屋 Core 一致）

Run dir: /root/flvx-workers/runs/ipv6-64-count. Base: `origin/feature/3.0.27-relay-egress-ips` (plan 230 @ 53691caa, on fork.122).
Branch: `feature/3.0.27-ipv6-64-count`. Build-only (next batch release with 229 + 230).

## Why
User 11-07 ~04:25 TW (follow-up to runs/node105-drops 「需要你决定」 #2): jiuwei's phone on Leikwanhost 沪台IPLC showed 7+
IPv6 privacy addresses inside `2409:8a4c:5c4f:26e1::/64`; FLVX counted each address as one IP against 用户总 IP 数.
The 妙妙屋 Core (`normalizedSourceIP`: IPv6 → `PrefixFrom(addr, 64).Masked()`) and the mmwx Helper nft limiter
(`ip6 saddr & ffff:ffff:ffff:ffff::`) already count one /64 as one IP. Not in scope: the Core's 30 s online-IP grace.

## Key ("IP 单位")
`ClientIPKey(raw)` (agent `go-gost/x/internal/util/ipkey`, panel `handler.quotaIPKey`): trim / strip `[]` and zone;
IPv4-mapped IPv6 → IPv4; IPv4 → the address; IPv6 → `<prefix>::/64` (exactly the Core's string, e.g.
`2409:8a4c:5c4f:26e1::/64`); an IPv6 prefix string (what the Core reports) → its /64; anything unparsable → itself.

## Mixed-fleet scheme (panel first, agents OTA'd later; some agents never OTA'd)
- New agents announce `ipKey=1` in the WS handshake (like `destOrder`). They count by key internally and normalise every
  `sharedIps` entry they receive to its key (so raw entries from an old/rolled-back panel still work).
- All agents keep reporting **raw** client addresses in `quotaGroups[].clientIps` (wire unchanged), so a rolled-back panel
  and old-agent shared lists keep working. Panel normalises every report (agents, native landing Cores, 妙妙屋 shares) to
  keys for counting: 用户总 IP 数 live usage = distinct keys; each slot's IP budget = limit − keys used only elsewhere.
- `sharedIps` per forward slot: `ipKey` agents get the other slots' keys (one /64 entry covers every address in it);
  old agents get exactly today's list (raw addresses seen on other slots, not local raw).
- Why this never over-counts worse / never blocks what is allowed today on an old agent: its shared list is unchanged and
  its budget = limit − |other-only keys| ≥ limit − |other-only raw| (each other-only /64 has ≥ 1 other-only raw address).
  The user total stays enforced in keys: local raw count ≥ local key count.

## Checklist
- [x] Investigate: agent gost path (service.go / quota.go), nft gate (quota_linux.go), landing user limits (landing/user_limits.go),
      panel coordinator (quota_groups.go), forward client-ips view, plan 230 egress history / relay slots, Core + Helper refs
- [x] Agent: ipkey package + unit tests
- [x] Agent gost path: rule + group counting by key, shared IPs normalised, raw addresses still reported
- [x] Agent nft path: conntrack usage by key, IPv6 sets hold /64 keys, gate rule masks `ip6 saddr & ffff:ffff:ffff:ffff::`
- [x] Agent landing: Core online IPs (`x::/64`) parsed, relay sources matched by /64 (IPv6 relays were never recognised)
- [x] Agent handshake `ipKey=1`
- [x] Panel: ws session `ipKey` capability + coordinator counts keys, per-capability shared lists, push-urgency usage by key
- [x] Panel: forward client-ips admin view (规则「当前 N 个 IP」) groups by key — no frontend change (rows show `<net>/64`)
- [x] Panel: plan 230 egress history keeps one IPv6 per /64, relay slots count distinct /64s
- [ ] Tests: agent (gost + nft + landing), panel unit, go test ./... (SQLite + PostgreSQL contract), nft netns lab with real IPv6 sources
- [ ] Resource numbers (conns/s before/after, RSS)
- [ ] Prod evidence (read-only): per-user IPv6 addresses vs distinct /64
- [ ] Merge check with feature/3.0.27-reality-probe-stable, push, CI green, ledger row ready
- [ ] final.md / summary.md
