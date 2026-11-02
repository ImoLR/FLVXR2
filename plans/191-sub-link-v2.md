# 191 — 订阅链接第二期：Surge / Surfboard / Loon / Quantumult X 原生格式（sub-link-v2）

Build only (no tag / release / prod writes / OTA). Base = `3.0.27-fork.106` (tag → 926bd86d).
Branch `feature/3.0.27-sub-link-v2`, worktree `/root/projects/flvxx/flvxx-sub-link-v2`.
Run dir `/root/flvx-workers/runs/sub-link-v2`. User approval 2026-11-02 「开始做吧」 (phase 2 of plan 185).

Scope: backend generators + UA table + `?t=` values + small text changes only. No new settings,
pages, per-node options, tables or columns. The editor's saved format stays auto|clash|singbox|base64.
Panel only, no agent change, no OTA, no DB change, no new goroutines/timers, same render cache.

## Tasks

- [x] 1. Plan doc + branch/worktree, WIP push
- [x] 2. Official docs read (Surge manual, Surfboard docs, Loon docs, QX sample) → support matrix below
- [x] 3. Golden test: Clash / sing-box / Base64 output byte-identical to fork.106 (captured before any change)
- [x] 4. Generators: `surge.go` (Surge + Surfboard flag), `loon.go`, `qx.go`; name escaping; empty results valid
- [x] 5. UA table (Surge iOS/Mac, Surfboard, Loon, QX) + comment; `?t=` surge/surfboard/loon/qx
- [x] 6. Public handler: formats, managed-config line built from the request (cache stays per token+format), headers, Content-Type
- [x] 7. Text: browser HTML page client list + 订阅 page QR hint (no layout change)
- [x] 8. Unit tests (protocol × client, skip, empty, names, rule modes, managed line, real UA strings) + handler/contract tests
- [ ] 9. Sub-Store parser round-trip oracle (test only, deleted afterwards)
- [ ] 10. Demo panel: curl with each UA, samples; render timing 300 nodes (Surge / QX cold + cached vs Clash)
- [ ] 11. Gates: go build / vet / test (SQLite + PostgreSQL contract), frontend tsc / build / lint vs base; push; CI green
- [ ] 12. Gallery :8791 /sub-link-v2/ + `runs/sub-link-v2/release-notes.md` + summary

## Support matrix

Source: official docs only (manual.nssurge.com policies/*, getsurfboard.com docs/profile-format/proxy/*,
nsloon.app docs/Node, github.com/crossutility/Quantumult-X sample.conf). Enforced by `TestSupportMatrix`.

| Node (FLVX) | Surge | Surfboard | Loon | Quantumult X |
|---|---|---|---|---|
| Shadowsocks AEAD (aes-*-gcm, chacha20-ietf-poly1305) | ✓ | ✓ | ✓ | ✓ |
| SS2022 2022-blake3-aes-128/256-gcm | ✓ | ✓ | ✓ | ✓ |
| SS2022 2022-blake3-chacha20-poly1305 | ✗ | ✗ | ✗ | ✗ |
| VLESS Reality Vision (tcp) | ✗ | ✗ | ✓ | ✓ |
| VLESS tcp / ws, none or TLS | ✗ | ✗ | ✓ | ✓ |
| VMess tcp / ws (± TLS) | ✓ | ✓ | ✓ | ✓ |
| Trojan tcp / ws | ✓ | ✓ | ✓ | ✓ |
| any gRPC transport (VLESS / VMess / Trojan) | ✗ | ✗ | ✗ | ✗ |
| Hysteria2 | ✓ | ✓ | ✓ | ✗ |
| Hysteria2 with Salamander obfs | ✗ (iOS lacks it; Mac 6.4.3+ only) | ✓ | ✓ | ✗ |
| SOCKS5 / HTTP / HTTPS | ✓ | ✓ | ✓ | ✓ |
| AnyTLS (FLVX landing, pinned cert) | ✓ | ✓ | ✓ | ✓ |
| Snell v4/v5, no obfs or HTTP obfs | ✓ | ✓ | ✗ | ✗ |
| Snell v4/v5 with TLS obfs | ✗ (v4/v5 HTTP obfs only) | ✓ (docs allow tls) | ✗ | ✗ |
| Mieru | ✗ | ✗ | ✗ | ✗ |

Also skipped: secrets a format cannot write (Surge / Loon: `"` or `\` in a quoted value; QX and Surfboard's
unquoted Trojan / SOCKS / HTTP credentials: comma, quote, whitespace). Unsupported → left out silently.

## Format decisions

- Surge: full managed profile. `#!MANAGED-CONFIG <url> interval=43200 strict=false` is prepended by the handler from
  the request (scheme: TLS / X-Forwarded-Proto / CF-Visitor; host as requested; `?t=` only when it is a valid value),
  so the 60 s render cache stays keyed by (token, format[, flavour]). `[General]` loglevel / skip-proxy / dns-server
  system + 223.5.5.5 + 119.29.29.29 / proxy-test-url generate_204. Groups 节点选择 (select: 自动选择 + nodes + DIRECT)
  and 自动选择 (url-test, url=generate_204, interval=300, tolerance=50); empty → `节点选择 = select, DIRECT`.
  Rules: private CIDRs `no-resolve` + `DOMAIN-SUFFIX,local` → DIRECT, cn_direct adds `GEOIP,CN,DIRECT`,
  `FINAL,节点选择,dns-failed`. No RULE-SET downloads. Passwords quoted (`password="…"`), vmess-aead=true.
- Surfboard (UA `Surfboard` or `?t=surfboard`, flavour of the Surge generator, cache key `|surfboard`): no loglevel /
  skip-proxy, no `no-resolve` / `dns-failed` (not in its docs), explicit `udp-relay=true` (default false there),
  positional SOCKS / HTTP credentials and unquoted Trojan password (as its docs show), Salamander and Snell TLS obfs allowed.
- Loon: node list, `Name = Protocol,host,port,…` per line, no sections (legacy-compatible). Empty → one `#` comment line.
- Quantumult X: server_remote lines `type=host:port, …, tag=Name`; IPv6 written without brackets (last colon = port, as
  Sub-Store's QX parser expects). Empty → one `#` comment line.
- Names: `,` `=` → `，` `＝` everywhere; Surge / Loon also `#` `;` `//` `"` → full-width (inline comment / quote
  markers); re-deduped after replacement; built-in policy names get ` · 节点`.
- `?t=`: `surge`, `surfboard`, `loon`, `qx` (+ `quanx`, `quantumultx`). Saved editor format unchanged
  (auto | clash | singbox | base64); a saved non-auto choice still wins over the UA as before.
- Headers for all four: Content-Type `text/plain; charset=utf-8`, subscription-userinfo (Surge and Loon document it),
  profile-update-interval, profile-title, content-disposition (`.conf` Surge/Surfboard, `.txt` Loon/QX).

## Notes

