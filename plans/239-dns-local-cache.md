# 239 — dns-local-cache: fix fleet DNS stalls (ops only, no release)

Source: `runs/dns-rule-sweep-1108` (P1-1..P1-4). User approved 11-08 ~15:00 TW; rejected a shorter `timeout`
(glibc/Go minimum is 1 s and resolv.conf has no memory) → local cache resolver.

## Design
- Phase A (interim, every reachable host): good resolver order, dead resolver removed, second resolver where only one,
  `options timeout:1 attempts:2` on static resolv.conf, changed at the config source (resolved.conf / resolvconf /
  interfaces), backups in `/root/dns-backup-20261108/` + per-host `rollback.sh`.
- Phase B: `dnsmasq-base` (binary only, no Debian service → can never start with the default open config) + own unit
  `flvx-dnscache.service` (`MemoryMax=32M`, `CPUQuota=20%`), config `/etc/flvx-dnscache/dnsmasq.conf`:
  `listen-address=127.0.0.1,::1` + `bind-interfaces`, no DHCP/TFTP, `no-resolv`, `all-servers` (2–3 upstreams,
  first answer wins), `cache-size=1000`, `max-cache-ttl=300`, `neg-ttl=30`, no query log,
  `server=/imgamer.top/1.1.1.1` + `1.0.0.1` (all our DDNS / panel names, TTL 120 s).
  resolv.conf → `nameserver 127.0.0.1` + public fallback(s) + `options timeout:1 attempts:2`.
- systemd-resolved hosts: resolved IS a local cache already (measured below) → keep it, no second resolver on :53.
  Uplink mode (resolv.conf listed the upstreams, cache bypassed) → static resolv.conf `127.0.0.53` + fallback + timeout;
  stub mode → unchanged except a second upstream provider where only one.
- No restarts of agents / cores / xray / gost / Helper / mmwx agent (resolv.conf is re-read live; PIDs checked).
- Never touch DNS 04:40–06:10 TW; no new hosts after 23:30 TW.

## Tasks
- [x] Read sweep report, host inventory, rewrite-risk list; pick per-host upstreams
- [x] Domain check: rule / tunnel / landing / mmwx targets → our zone(s) for `server=/…/`
- [x] Tooling: per-host backup + rollback script, Phase A apply, Phase B apply, measure (dnsq), port-53 exposure check
- [x] Phase A canaries: Leikwanhost 沪台IPLC → Fachost seednet 黑五 → Boil Hinet 马年
- [x] Phase A rest (incl. Bestvm HK STD dead resolver, single-resolver hosts)
- [x] Measure systemd-resolved behaviour (stub vs uplink) → per-host decision
- [x] Phase B canaries (same 3) + measurements: cold / cached / dead upstream / dnsmasq stopped, RSS, PIDs, outside port 53
- [x] Phase B rollout in batches (check per host)
- [x] Breadcloud JP2 (mmwx-prod) last, with Custom / boil / cloudflared checks
- [x] Final verification sweep (resolv.conf, dnsmasq RSS, outside 53 closed, agents online)
- [x] final.md / summary.md (Chinese), memory `dns-rule-sweep-1108` status line
- [x] Commit plan on docs branch `docs/3.0.27-dns-local-cache`

## Notes / results
- Domains: every own name in rules / tunnels / landings / DDNS is `*.imgamer.top` (boilhinet, boilhinet2, pqstw, boilhkt99u,
  leikwan-hutai-ct/cu, dmitnathan, dmitecho, tcsseednet, us2, facseednet, flvxx/flvxxht); mmwx uses IPs only → one zone.
- Package: `dnsmasq-base` only (binary, no unit / init script) → nothing can start with Debian's default config; own unit
  `flvx-dnscache.service`. Config syntax + behaviour tested locally on a loopback test port first (dead upstream + good one:
  cold 5 ms, cached 0.2 ms).
- resolved measured on Bestvm HK STD (global DNS dead, link 8.8.8.8): via its stub 127.0.0.53 cold 3–53 ms, cached 1–3 ms,
  no 5 s — it races scopes / sticks to the working server. Decision: resolved hosts keep resolved (stub = the local cache);
  uplink-mode hosts (resolv.conf bypassed the cache) get a static resolv.conf → 127.0.0.53 + fallback + timeout:1.
  Stub-mode hosts already use it (only single-upstream ones get a second provider).
- Phase A canaries (Go system path, 13 lookups): 沪台 p90 63→58 ms max 5045→396; 黑五 p90 13→4 ms; 马年 p90 68→6 ms.
  黑五: first try global 8.8.8.8 made it slower (uplink lists global first: p90 122 ms) → global = seednet resolver (4 ms).
- 沪台 SSH upload is ~12 KB/s (dnsq 1.9 MB took 172 s) — keep transfers to it small.
- Install path: `dnsmasq-base` only. Debian 11 hosts: security.debian.org no longer has bullseye (moved to archive.debian.org,
  404) and the 沪台 vendor mirror answers 502 → same version fetched from the official archive on the dev box, SHA256 checked
  against the host's own apt index, put into `/var/cache/apt/archives/`, `apt-get install --no-download`.
  apt itself peaks at 74–100 MB RSS → Breadcloud JP (133 MB free, no swap) and Dmit T1 (95 MB) got `dpkg -i` only
  (debs verified against the dev box's signed index; only the missing deps).
- Bugs caught on the way (all before anything was changed): apt "upgrade" guard matched `[amd64]` (refused safely);
  `_apt` cannot read /root (moved debs to the apt cache); openresolv drops all other nameservers once 127.0.0.1 is listed
  (`resolv_conf_local_only=NO` added; Tcscloud Seednet + Mkcloud 深港ixp 2 had no fallback for ~2 min).
- Phase B results (Go system path): dnsmasq hosts cached p50 0.3–2.2 ms, cold = upstream speed (1–40 ms); canary
  dead-first-upstream test max 118 ms (沪台) / 69 ms (马年); dnsmasq stopped → fallback max 61 / 8 ms; Bestvm HK STD
  5010 ms → 3–5 ms. dnsmasq RSS 4.3–5.5 MB (cgroup 0.6–1.5 MB). Outside UDP/TCP 53 closed on every public IP
  (dev box + AWS HK as second vantage). PIDs of agents / cores / xray / Helper / mmwx agent unchanged everywhere;
  31/31 FLVX nodes online; mmwx-prod: 12 services same PIDs, cloudflared 4 conns, mmwx + mmwxc 200.
- Proxied page (temp admin landing on 马年, from the dev box, 12 fetches each): TLS median 0.65 s (Phase A) → 0.61 s, 0 errors.
- Rollback tested on idle hosts (Mkcloud 深港ixp 2: `b` then `all`; Bestvm HK STD + Lightlayer TW: `all`) → original files back (same content + mtime,
  Bestvm HK STD even back to its dead resolver), then re-applied.
- Not changed: Nobrand 5cm (SSH down), Dmit Echo (no SSH); Lightlayer TW Phase A only (40 MB free);
  resolved-stub hosts AWS HK / Nobrand IPLC 沪港 / Nobrand KFC 1cm unchanged (vmsilo 沪港 / 沪日首发: +1.1.1.1 global only).
- Code change (FLVX Core dns block / gost cache): not needed any more — every lookup now hits a local cache (~1 ms).
- Upstream volume (dnsmasq counters, 16:06–16:58 TW, 23 hosts): 52,244 lookups → 18,951 upstream queries = 36 % of the
  no-cache volume even with 2–3 parallel upstreams (Boil Hinet 158: 352/min → 28/min; PQS 117 → 52; Bestvm TW 104 → 27).
- Found on the way (not caused by this change, not fixed): the PQS TW line drops every UDP DNS query containing `paypal`
  (all resolvers time out, TCP 53 works, no local rule; 马年 on Hinet resolves fine) → PayPal fails via the PQS landing.
  Fix would be core-side DNS over TCP for such names (code change) or the vendor.
- Debian 11 hosts: bullseye security pool moved to archive.debian.org (apt 404) — source fix / OS upgrade is the user's call.
- Report: `runs/dns-local-cache/final.md` (user) + `summary.md` (with technical notes), table `runs/dns-local-cache/table.md`.
