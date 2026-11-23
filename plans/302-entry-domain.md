# 302 — Subscriptions with IPv6-only entry domains + node DDNS for 沪日首发 / 沪美

Task `entry-domain-1124` (coordinator 22b5afb7, 11-24 TW). Panel only.

## Goal

Entries are reached by domain instead of a raw IP: on an IP change only the DNS
record changes. Step 1 = turn on the existing node DDNS (plan 142 / fork.82) for
node 51 「vmsilo 沪日首发」 (`vmsilo-huri.imgamer.top`, AAAA ::e4) and node 59
「vmsilo 沪美」 (`vmsilo-humei.imgamer.top`, AAAA ::8f), provider 1 「Cloudflare
imgamer.top」, DNS only, TTL 120 s. Both entries are IPv6-only inbound. Before
that, subscriptions must keep working when the entry address is an AAAA-only
domain.

## Findings (lab 11-24, dev box, SS server on [::1], name `0--1.sslip.io` = AAAA ::1 only)

| Client | Generated config as is | Why |
|--------|------------------------|-----|
| mihomo 1.19.32 / 1.19.34 (Clash Meta, Clash Verge, FlClash …) | OK | `dns.ipv6: false` does not apply to proxy-server lookups (negative control: server down → `dial tcp [::1]:38711 refused`). Client-side global `ipv6: false` breaks literal and domain alike (no regression). |
| sing-box 1.12.25 / 1.13.21 / 1.14.4 | OK | `dns.strategy: prefer_ipv4` falls back to AAAA. |
| Stash | OK (docs) | `dns.ipv6: false` only strips AAAA from apps' answers; Stash's own connections may still use IPv6. |
| **NekoBox** (gets our Clash YAML, imports proxies only) | **FAILS with default settings** | default IPv6 路由 = 禁用 → server names resolve on `dns-direct` with `ipv4_only`; emulated with sing-box 1.11.15 + NekoBox's DNS layout: domain → `open outbound connection: empty result`, literal → 204. URL test runs with IPv6 forced on (`forTest`), so latency looks fine while traffic fails. Both affected subscriptions (28 liuchen, 29 test-ns-fx) are pulled by NekoBox. No per-proxy field in its Clash importer can steer this. |
| Surge | FAILS (docs) | `ipv6 = false` (ours and Surge's default): "After enabling this option, Surge queries AAAA records" — literals still work. |
| Base64 (Shadowrocket, v2rayN/NG), Loon, QX, Surfboard, Hiddify | not verifiable here | resolution follows each app's own IPv6 setting. |

Mainland (阿里深圳 fleet-aliyun-sz): 223.5.5.5 (UDP + DoH), 119.29.29.29, doh.pub
return the imgamer.top AAAA (checked with leikwan-hutai-ct.imgamer.top).

## Design (smallest safe change)

- The panel knows which entry domains are IPv6-only: a node DDNS record of type
  AAAA with a current IP and no A record for the same name.
- Subscription nodes on such an entry are built on the record's **current IPv6
  literal** (exactly today's output, so every client keeps working) and carry the
  domain in `Node.EntryDomain`.
- Formats proven to resolve AAAA-only proxy names write the **domain**: Clash
  (mihomo + Stash; not NekoBox) and sing-box (not Hiddify). Every other client
  keeps the literal (refreshed on every pull, so a changed IP arrives with the
  next 更新订阅).
- Nothing changes when no entry is an IPv6-only DDNS domain: generators untouched,
  goldens unchanged.
- Stored subscription items keep working after the switch: the entry key
  `51|<::e4>` falls back to the forward's single entry on that node (existing
  `byEntry` rule); the editor rekeys them to `51|vmsilo-huri.imgamer.top`.
- 复制链接 (forward copy link) shows the domain (the user's goal); NekoBox users
  importing it by hand need IPv6 路由 = 启用.

## Results

- go test ./... (go-backend): base fork.166 29/29 packages ok, 0 failing tests; this branch 29/29 ok, 0 failing.
- New test `TestSubEntryIPv6OnlyDomain` fails on fork.166 (NekoBox / Hiddify / Shadowrocket / Surge / unknown get the bare domain) and passes here; `TestUseEntryDomains` + goldens pass (goldens untouched).

## Checklist

- [x] Read memories / brief; worktree `flvxx-302` on `fix/3.0.27-entry-domain` from fork.166
- [x] Prod read-only: nodes 51/59 addresses, tunnels, forwards, DDNS records, subs (28 = liuchen NekoBox, 29 = test-ns-fx NekoBox)
- [x] Lab: mihomo 1.19.32/34, sing-box 1.12/1.13/1.14 pass; NekoBox emulation fails; Surge/Stash docs
- [x] Mainland DNS check (fleet-aliyun-sz)
- [x] Code: repo IPv6-only DDNS domain map + literal entry nodes with `EntryDomain`
- [x] Code: `sublink.UseEntryDomains`; handler picks domain/literal per client (cache key)
- [x] Tests: sublink unit (byte-identical without EntryDomain), handler test (Clash/sing-box domain, NekoBox/Base64/Surge literal, stored literal item resolves after the switch, editor rekey), goldens unchanged
- [x] Lab after: mihomo + sing-box with the domain, NekoBox flavour literal
- [x] go test ./... compared with base fork.166
- [x] Wait for leikwan-batch-1124 to finish; merge onto what prod runs
- [x] Isolated prod-DB lab (paneld in its own netns, loopback only): base fork.166 vs new = 96/96 renders byte-identical (8 subs × 12 UAs); after a simulated switch only subs 28/29 change and only for mihomo / Stash / sing-box (domain), NekoBox / Hiddify / Shadowrocket / v2rayN / Surge / Loon / unknown byte-identical; old code would hand the bare domain to all; mihomo -t + sing-box 1.12/1.13/1.14 check pass
- [ ] Ledger row, tag next fork, CI + images + release assets
- [ ] Prod backup + rollback script + prune; upgrade; health + light node check
- [ ] CF zone: names free, no wildcard; DDNS records on nodes 51 / 59 (main AAAA, source 电信 240e)
- [ ] Verify: CF records, node addresses, tunnels 126–132 diagnose (hop 沪美 → 沪日首发 by domain), mainland AAAA + TCP, real subs (Clash + sing-box UA) via CF, mihomo -t / sing-box check, mihomo URLTest from 阿里深圳
- [ ] Summary + user list; plan complete
