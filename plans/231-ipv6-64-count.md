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
- [x] Tests: agent (gost + nft + landing), panel unit, go test ./... (SQLite + PostgreSQL contract), nft netns lab with real IPv6 sources
- [x] Resource numbers (conns/s before/after, RSS)
- [x] Prod evidence (read-only): per-user IPv6 addresses vs distinct /64
- [x] Merge check with feature/3.0.27-reality-probe-stable, push, CI green, ledger row ready
- [x] final.md / summary.md

## Results (11-07 TW, build only)
- Agent `go-gost/x` go test ./... 22 packages ok; nft netns suite 38 pass / 1 skip (TestIPv6RANetNS, E2E-only), incl.
  new real-IPv6 gates: same /64 admitted, second /64 (`fd31:1:0:1::2`, added to netns_integration.sh) refused, shared
  /64 admitted at a full pool, 2 raw addresses reported for 1 counted IP. `go-gost` builds.
- Panel go test ./... SQLite + PostgreSQL 26 packages ok (handler 130 s, repo 247 s, contract 285 s). CI Build Check
  42294003120 green (agent, frontend, backend, PostgreSQL contract). No frontend change (rows print `<net>/64`).
- New landing test fails on the base (IPv6 relay `2001:db8::/64` counted as a user IP), passes now.
- Resources (lab: real accept loop, 30 k TCP conns/run, 16 IPv4 + 16 IPv6 sources in one /64, ABBA × 6): conns/s
  37 428 → 36 642 median (−2 %, spread 34.8–38.8 k), CPU/conn 50.9 → 52.2 µs, RSS 17.9 → 17.6 MB; acquire+release
  IPv4 ~118 → 125 ns, IPv6 ~120 → 250 ns (2 allocs); ipkey.Of IPv4 5.6 ns / 0 allocs, IPv6 91 ns / 1 alloc.
- Prod (read-only, 04:40–05:17): @jiuwei 6/7 counted = 2 real IPv4 + 2 IPv6 in `2409:8a4c:5c3a:1941::/64` + 2 沪台
  egress phantoms → 231 alone 5/7, 230 + 231 3/7. History: 9 addresses in `2409:8a4c:5c4f:26e1::/64` on 11-04/05.
- Not fixed (mmwx side, untouched): Helper `flvx.CanonicalIP` drops the Core's `<net>/64` online IPs → IPv6 clients on
  shared landings are invisible to FLVX's 用户总 IP 数, IPv6 relays not recognised there.
- Report: /root/flvx-workers/runs/ipv6-64-count/final.md.
