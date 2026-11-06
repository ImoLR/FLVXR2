# 226 — sub-dns-node-down: 订阅在「节点挂了 / 节点选择 = 直连」时国内网站照常打开 (build only)

User 2026-11-06 ~14:30 TW 「把这些也一起修了吧」: the residual gaps listed in the plan-220 report
(`runs/sub-dns-fix-r2/report-full.md`, 「还没解决」): DNS that goes through the proxy node must not take mainland
sites down when the node is dead or 节点选择 is switched to DIRECT. FLVX only (mmwx handled separately).
Base = tag 3.0.27-fork.119 (1d031311), branch `feature/3.0.27-sub-dns-node-down`, run dir
`/root/flvx-workers/runs/sub-dns-node-down`. BUILD ONLY: no release, prod read-only (temp sub `sd-node-down` only).
Panel only, no DB change, no OTA, no frontend change.

## Tasks
- [x] Pull the fork.119 sub (temp admin sub `sd-node-down`, one landing ref, deleted afterwards) for every UA
- [x] Mainland matrix on leikwan-sh (mihomo 1.19.32; sing-box 1.12.25 / 1.13.21 / 1.14.4): node healthy / dead (closed port) /
      blackhole / 节点选择 = DIRECT × system-proxy / TUN emulation; sing-box cold + warm cache start
- [x] Fix only what fails (Clash / sing-box), minimal; other formats byte-identical
- [x] Re-run the matrix after the fix (before / after table: run dir `m/matrix.md`)
- [x] Tests: sublink unit + golden, mihomo -t, sing-box check 1.12–1.14, full go test (SQLite + PostgreSQL contract), CI
      (r1 full run: repo + contract hit the default 10 min timeout under build-lock contention; r2 11-06 16:56–17:57 TW
      `-p 1 -timeout 60m` SQLite + PostgreSQL: all 25 packages ok, repo 1883 s, contract 1121 s, 0 failures —
      run dir `runs/sub-dns-node-down-r2/gotest-r2.txt`)
- [x] Render time / allocs 1 and 100 nodes before vs after (run dir `bench-table.txt`)
- [x] Ledger row (ready) in /root/flvx-workers/RELEASE-LEDGER.md with its release check
- [x] Cleanup: temp sub deleted, leikwan-sh /root/sd-test removed, no processes left
- [x] final.md / summary.md

## Findings (fork.119, mainland leikwan-sh)
Test node = the template of the fork.119 sub as served, with its single node swapped for a real relay node
(Dmit中转 · Dmit Nathan from usb2's sub, read-only GET) because the temp sub's direct landing is blocked from the mainland
by the landing's source rules. TUN = DNS through the client's DNS listener, then SOCKS5 to that IP.
1. **Clash / mihomo**: geosite:cn sites fine in every state. The fake-ip-filter names (Windows network check, STUN,
   consoles, lens.l.google.com) resolve via `nameserver …#节点选择`: node dead → DNS fail (closed port 0.05 s, blackhole 5 s);
   节点选择 = DIRECT → 5 s DNS timeout. Windows network check (www.msftconnecttest.com) also *routes* via 节点选择
   (Akamai answer is not GEOIP CN) → fails with the node dead even in system-proxy mode → Windows 「无 Internet」.
2. **sing-box**: **refuses to start** (FATAL `initialize rule-set`) on a first start (no cache.db) when the node is dead
   (closed port 0.3 s, blackhole 5.3 s), all three versions; urltest picks a node before any test, so a dead first node
   is enough. With a cache it starts. 节点选择 = direct + no cache: downloads from raw.githubusercontent.com directly:
   2.8 s (1.12), 4.0 s (1.14), **>30 s → start failed (1.13)**. Windows network check fails in TUN whenever the node is
   unusable (DNS via dns-remote).
3. Stash: nameserver already CN DoH (plan 220) → no node dependency. Surge / Surfboard: DNS = system + CN. Loon / QX / Base64:
   node lists, no DNS. → not changed.

## Changes
- `sublink/clash.go`: `nameserver-policy` gets two more keys (the fake-ip-filter names except LAN names and
  lens.l.google.com) → CN DoH; 国内直连 rules `DOMAIN-SUFFIX,msftconnecttest.com,DIRECT` + `msftncsi.com` before
  `GEOSITE,cn`. Clash 国内直连 only: Stash (already CN DoH, no Windows) and 全部走代理 stay byte-identical. `clash_yaml.go`: plain keys may contain `*` / `+`.
- `sublink/singbox.go`: `SingBox(nodes, mode, origin)`: rule sets download **DIRECT from the panel**
  (`<scheme://host the sub was pulled from>/s/rs/geosite-cn.srs`, `geoip-cn.srs`) instead of GitHub through 节点选择;
  NCSI domain_suffix → dns-local + direct.
- `sublink/ruleset.go` + `ruleset/*.srs` (91 KB, SagerNet rule-set branch 2026-11-06): embedded copies; refreshed from
  GitHub at most once a day on demand (background, 30 s timeout, 4 MiB cap, "SRS" magic check, failure keeps the copy).
- `handler/sub_link_ruleset.go`: `GET/HEAD /s/rs/<name>.srs` (no login, 403 when 订阅 is off, ETag / 304,
  `Cache-Control: public, max-age=3600`, no per-request log); nginx `location ^~ /s/` already forwards it.
- `handler/sub_link_public.go`: `subPublicOrigin(r)`; sing-box render cache key includes the origin.
- Tests: `TestNodeDownPlan226`, `TestRuleSetsServeAndRefresh`, `TestSubSingBoxRuleSetsFromPanel`, `BenchmarkRender1`;
  golden files: clash-cn / clash-empty / 100-clash-cn / singbox-cn / 100-singbox-cn (stash, global, Surge, Surfboard, Loon, QX,
  Base64 unchanged).

## Not solved (see run dir final.md)
- sing-box TUN: when the node is dead or 节点选择 = direct, names outside geosite-cn get no DNS answer (client waits ~8 s);
  sing-box drops the query on a detour error. Fix would be sing-box fake-ip (changes routing of small mainland sites) → user decision.
- `download_detour` is deprecated in sing-box 1.14 and removed in 1.16 (`http_client` replaces it, 1.14+ only) — affects
  fork.119 and this build alike.
