# 220 — sub-dns-fix: 订阅 DNS / 各客户端对照妙妙屋 (fork.118, panel only) — DONE 2026-11-06 00:17 TW

User report 2026-11-05 ~23:00 TW: FlClash + FLVX 订阅 → every site stalls 2–3 s on 「Dmit中转 · Dmit Nathan」, the mmwx
sub to the same landing is instant. Urgent prod fix (4th FLVX release of 11-05, allowed for urgent fixes).
Scope extended by the user ~23:25 TW: model every client format on mmwx's real output, make sure no client has this class
of slow / no-connect problem. Run dir: `/root/flvx-workers/runs/sub-dns-fix`. Base = tag 3.0.27-fork.117 (33ce8075).
Panel only, no DB change, no OTA.

## Cause
`internal/sublink/clash.go` `clashDoc()`: `nameserver` = 1.1.1.1 / 8.8.8.8 DoH used DIRECTLY (both time out from mainland),
and rule mode ends with `GEOIP,CN,DIRECT` without `no-resolve` → mihomo resolves every non-geosite:cn domain locally with
those dead servers before it can pick the proxy → ~5 s per new connection (measured on leikwan-sh: A 5.3–5.7 s vs B 0.26–0.68 s).

## mmwx black box (user's mmwx sub, one GET per UA / t=, 4 s apart; bodies in run dir `mmwx/`)
| Client (UA) | mmwx returns | FLVX returns | Connectivity / speed-relevant difference → decision |
|---|---|---|---|
| FlClash, Clash Verge Rev, mihomo, Clash Meta for Android, curl, browser | mihomo YAML (redir-host, `8.8.8.8#🚀 手动选择`, direct/proxy-server CN DoH, `GEOIP,cn,…,no-resolve`, https://cp.cloudflare.com/generate_204, many rule-providers) | mihomo YAML (fake-ip) | **adopt**: foreign DoH via the select group, `direct-nameserver` CN DoH, `no-resolve`. **keep** fake-ip (DNS answer is instant; redir-host needs one proxied DoH round per new domain), 2 foreign resolvers (resilience), http test URL (plan 213; 204 without TLS), no rule-providers (brief) |
| NekoBox | mihomo YAML | mihomo YAML | same (NekoBox only imports the proxies) |
| Stash | Clash YAML, `nameserver: [8.8.8.8#group, 120.53.53.53]`, fake-ip-filter list | Clash YAML (Stash-safe) | Stash docs (stash.wiki/en/features/dns-server): no `#group` suffix, DNS goes direct unless follow-rule → **CN DoH pair** as nameserver (mmwx's `#group` entry would be dialled directly = dead in CN; its CN entry is what answers); **adopt** fake-ip-filter list + `no-resolve` |
| sing-box, SFA, SFI, Hiddify | outbounds-only JSON (no inbounds / DNS / route) | full config (tun + mixed, dns-remote 1.1.1.1 **detour 节点选择**, dns-local 223.5.5.5 for geosite-cn + server names, sniff, rule sets via proxy) | **keep** ours (SFA/SFI need a full profile; DNS already proxied). Fix: no nodes → CN DoH only, no rule sets (select = direct only would dial 1.1.1.1 / GitHub directly) |
| Surge iOS / Mac | full Surge profile (`dns-server = system, 223.5.5.5, 119.29.29.29`, `ipv6 = false`, `hijack-dns 8.8.8.8/8.8.4.4`, `udp-policy-not-supported-behaviour = REJECT`, `always-real-ip`, ChinaCIDR without no-resolve, FINAL dns-failed) | managed Surge profile (same DNS, `GEOIP,CN,DIRECT`, FINAL dns-failed) | DNS already CN (local resolve fast) → keep. **adopt** `ipv6 = false`, `hijack-dns`, `udp-policy-not-supported-behaviour = REJECT` (Surge iOS default DIRECT: QUIC on a no-UDP policy would go direct from CN and stall), short `always-real-ip` |
| Surfboard | node list (1 SS line) | Surge-syntax profile (DNS system + CN) | keep (Surfboard-documented keys only) |
| Loon | node list | node list | same — no DNS inside |
| Quantumult X | full QX template (dns 223.5.5.5 / 119.29.29.29, geoip cn direct) | node list | keep (rules/DNS stay in the app; no DNS problem possible) |
| v2rayN, v2rayNG | Base64 links | Base64 links | same |
| Shadowrocket | Clash-style `proxies:` list (Sub-Store shadowrocket producer) | Base64 links | keep Base64 (links only, app's own DNS; not run here) |
| `t=` values | clash/clashmeta/stash/sing-box/surge/surfboard/loon/qx/v2ray/shadowrocket ok; mihomo/singbox/quanx/quantumultx/base64 → 500 | `t=` clash/singbox/base64/surge/surfboard/loon/qx | n/a |

Landing side checked: no FLVX landing preset blocks 1.1.1.1 / 8.8.8.8 / DoH or port 80 (cp.cloudflare.com test URL).

## Fix
- Clash (mihomo), both modes: `nameserver` = `https://1.1.1.1/dns-query#节点选择`, `https://8.8.8.8/dns-query#节点选择`
  (CN DoH pair when the sub has no nodes); new `direct-nameserver` = 223.5.5.5 / 120.53.53.53 DoH; rule mode
  `GEOIP,CN,DIRECT,no-resolve`; fake-ip-filter + mmwx's list (Windows NCSI, STUN, consoles).
- Stash: `nameserver` = CN DoH pair (no `#group`, no direct-nameserver), same fake-ip-filter + `no-resolve`.
- sing-box: no nodes → only dns-local, final dns-local, no remote rule sets. With nodes unchanged.
- Surge (not Surfboard): `ipv6 = false`, `hijack-dns = 8.8.8.8:53, 8.8.4.4:53`,
  `udp-policy-not-supported-behaviour = REJECT`, `always-real-ip = …`.
- Loon / QX / Base64: node lists only → unchanged.

## Checklist
- [x] Plan number 220 free on every branch / worktree (218 admin-attention, 219 landing-exempt-drop taken)
- [x] Worktree `fix/3.0.27-sub-dns` from tag 3.0.27-fork.117
- [x] mmwx black-box pulls per UA + t= (run dir `mmwx/`), comparison table above
- [x] Code: clash.go DNS + no-resolve + fake-ip-filter, Stash variant, sing-box empty, Surge General lines
- [x] Golden files + tests (`clash_dns_test.go`); `go test ./internal/sublink/...` green
- [x] mihomo v1.19.32 `-t` OK (clash cn/global/empty, 100-node, stash); sing-box 1.12.25 / 1.13.21 / 1.14.4 `check` OK
- [x] Mainland proof on leikwan-sh (mihomo + sing-box, proxy + TUN-style): new ≈ mmwx; dev box (US) no regression
- [x] bench100 not regressed
- [x] Gates: go build/vet/test (SQLite + PG contract); frontend untouched (no diff vs fork.117)
- [x] Baselines (pre)
- [x] Release branch `maintenance/3.0.27-fork.118-sub-dns`, push, CI green, tag 3.0.27-fork.118, Images + Release + assets verified
- [x] Backup + rollback dir; prune (keep 2); upgrade /opt/flvx-svc panel
- [x] Post checks = baseline; version.json fork.118; old profile ≤ 1 reload; /api/clients/v2/rpc 403
- [x] Prod sub (usb2) on leikwan-sh as cfgP ≈ B; sing-box / Surge UAs still parse
- [x] Ledger 220 → 已发布 fork.118
- [x] Cleanup (leikwan-sh /root/mh-test, temp users/rules/subs/browsers); final.md / summary.md; plan marked complete

## Results
Release: branch `maintenance/3.0.27-fork.118-sub-dns`, tag `3.0.27-fork.118` → 5d0cc831, CI Build Check 42059330693,
Build and Push Images 42061219766 (Create Release hit a GitHub HTTP 500, failed job re-run → green), Latest, 10 assets,
pins / sha256 / ghcr amd64+arm64 verified. Prod upgraded 11-06 00:17 TW (panel only, no OTA, no DB change), 30/30 nodes online.
Rollback → fork.117: `/opt/flvx-svc/rollback/pre-fork118-20261105T161706Z/rollback.sh` (image swap; `--restore-db` last resort).

Gates: go build/vet OK; `go test ./...` with the PostgreSQL DSN = 25 packages ok, 0 failures (fork.117 base: 25 ok, 0 failures);
frontend / agent untouched (no diff vs fork.117). bench100: Clash 156.5→157.1 KB, 1864→1887 allocs, ~0.2 ms (noise);
Stash +21 allocs; others unchanged.

TTFB (8 sites × cold/warm1/warm2, sequential; proxy = HTTP proxy with the domain, tun = DNS via the client's
resolver then SOCKS5 to that IP, like a phone VPN):
## mainland (leikwan-sh, Volcengine Shanghai) 11-05 ~23:40–00:05 TW, mihomo v1.19.32 / sing-box 1.14.4, Dmit中转 · Dmit Nathan → Boil Hinet 158
| config | mode | ok/total | DNS median ms | TTFB(+DNS) median s | cold median | warm median | max s | client DNS log lines / failures |
|---|---|---|---|---|---|---|---|---|
| mhA | proxy | 24/24 | - | 5.37 | 5.37 | 5.40 | 5.68 | 188 / 24 |
| mhA | tun | 24/24 | 20 | 5.39 | 5.39 | 5.39 | 5.97 | 176 / 24 |
| mhB | proxy | 24/24 | - | 0.36 | 0.36 | 0.37 | 0.68 | 50 / 0 |
| mhB | tun | 24/24 | 21 | 0.42 | 0.47 | 0.39 | 5.53 | 82 / 0 |
| mhE | proxy | 24/24 | - | 0.36 | 0.35 | 0.38 | 0.69 | 48 / 0 |
| mhE | tun | 24/24 | 19 | 0.38 | 0.38 | 0.38 | 0.73 | 48 / 0 |
| sbA | proxy | 24/24 | - | 0.38 | 0.40 | 0.37 | 1.14 | 48 / 0 |
| sbA | tun | 24/24 | 82 | 0.44 | 0.45 | 0.43 | 0.69 | 215 / 0 ("NOERROR" lines excluded) |
| sbD | proxy | 24/24 | - | 0.37 | 0.35 | 0.41 | 0.60 | 48 / 0 |
| sbD | tun | 24/24 | 82 | 0.44 | 0.45 | 0.44 | 0.59 | 217 / 0 ("NOERROR" lines excluded) |

## dev box (US) same configs
| config | mode | ok/total | DNS median ms | TTFB(+DNS) median s | cold median | warm median | max s | client DNS log lines / failures |
|---|---|---|---|---|---|---|---|---|
| mhA | proxy | 24/24 | - | 1.09 | 1.08 | 1.09 | 1.47 | 158 / 0 |
| mhA | tun | 24/24 | 10 | 1.10 | 1.16 | 1.09 | 1.74 | 159 / 0 |
| mhE | proxy | 24/24 | - | 1.10 | 1.09 | 1.10 | 1.68 | 48 / 0 |
| mhE | tun | 24/24 | 10 | 1.11 | 1.10 | 1.12 | 6.00 | 48 / 0 |
| sbA | proxy | 24/24 | - | 1.10 | 1.11 | 1.10 | 1.44 | 48 / 0 |
| sbA | tun | 24/24 | 251 | 1.33 | 1.35 | 1.33 | 2.15 | 215 / 0 |

## after deploy: usb2's real prod sub pulled on leikwan-sh (fork.118), Dmit中转 · Dmit Nathan
| config | mode | ok/total | DNS median ms | TTFB(+DNS) median s | cold median | warm median | max s | client DNS log lines / failures |
|---|---|---|---|---|---|---|---|---|
| mhP | proxy | 24/24 | - | 0.38 | 0.39 | 0.37 | 0.47 | 54 / 0 |
| mhP | tun | 24/24 | 20 | 0.38 | 0.38 | 0.38 | 0.48 | 48 / 0 |

Prod after deploy: version.json fork.118, `/api/clients/v2/rpc` 403 (CF + local), old fork.117 browser profile → fork.118 with
1 reload, no loop / logout / blank; all real rules same signature as before (rule 37 2/4 pre-existing), 6 temp tunnels 2/2,
8 landing links 2/2, 救命版 unchanged; panel CPU 14.14 → 14.08 s / 5 min, RSS 44.3 → 45.5 MiB.
