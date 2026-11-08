# 240 — DNS cache as a standing service on every new node / new landing (dns-cache-default)

Base: `3.0.27-fork.126` (a5966795). Branch `feature/3.0.27-dns-cache-default`. **Build only** — no release, no OTA, no prod change.
Source recipe: plan 239 (`/root/flvx-workers/runs/dns-local-cache/`, ops-only, 34 hosts by hand on 2026-11-08).

## Goal
Turn the plan-239 recipe into product behaviour:
- one idempotent script `flvx-dns-cache` (`ensure` | `status` | `check` | `rollback [all|b]`), embedded in the agent binary
  (`go-gost/x/dnscache/flvx-dns-cache.sh`, installed to `/usr/local/sbin/flvx-dns-cache` when used);
- node installer runs `ensure` after the agent is up (one line ✅ / ⚠, never blocks the install; `FLVX_DNS_CACHE=0` skips);
- a node that becomes an FLVX native landing (`/landing/server/enable`) gets `ensure` once (panel-triggered, no-op if done);
- agent reports `dns_cache = {mode, ok, reason, checked_at}` in the metric (local check ≤ once / 10 min: resolv.conf + state
  file + /proc, no exec, no network; never changes DNS itself);
- panel: 「DNS 缓存」 row chip on the node card (正常 / 精简模式 / ⚠ 未启用·未运行 / 未知 / 检查中), ⚠ marker after the node
  name (grid + list), admin-only 「修复 DNS 缓存」 (one run per node at a time, ≤ 1 / min; agent: single flight, ≥ 20 s gap).

## Mode rules (decided)
- `dnsmasq` (default): dnsmasq-base binary + own unit `flvx-dnscache` exactly like 239 (127.0.0.1 + ::1 only when lo has ::1,
  bind-interfaces, all-servers, cache 1000, max-cache-ttl 300, neg-ttl 30, no query log, MemoryMax 32M, CPUQuota 20%,
  Restart=always with StartLimit 5 / 10 min so a broken cache never log-spams). Config written and `dnsmasq --test`ed
  before the unit exists; dnsmasq-base has no service of its own → never an open resolver.
- Upstreams (fresh hosts): vendor-internal resolvers (10/8, 172.16/12, 192.168/16, 100.64/10, fc00::/7) are kept alone
  (they may do split DNS / unlock), + a CN resolver last on CN clouds; known CN resolvers → 1.1.1.1 + 8.8.4.4 + one CN
  resolver LAST, our zones to the 2 fastest of 1.1.1.1 / 1.0.0.1 / 8.8.8.8; otherwise ≤ 2 own + 1.1.1.1 / 8.8.4.4 (max 3).
  One DNS probe (bash /dev/udp, + one retry) per candidate; dead ones dropped. Existing 239 configs keep their upstreams.
- `resolved`: systemd-resolved active with its stub → use it; stub symlink kept as is (239: stub hosts unchanged), uplink /
  static resolv.conf → static file to 127.0.0.53 + 2 fallbacks + `timeout:1 attempts:2 edns0 trust-ad`; < 2 upstreams →
  drop-in `/etc/systemd/resolved.conf.d/90-flvx-dns.conf` with one more.
- `minimal`: MemAvailable < 60 MB, :53 held by another program, full `dnsmasq` service enabled, non-systemd, NetworkManager /
  foreign symlink owner, no verified package, or the cache fails its self-test → only ≥ 2 resolvers + `timeout:1 attempts:2`.
- Packages: apt (MemAvailable ≥ 160 MB; refuses upgrades of existing packages; 404/502 → same versions from official
  archives, sha256 from the signed apt index); 60–159 MB → no apt, `dpkg -i` of pinned sha256-checked Debian 11/12/13
  amd64 packages (deb.debian.org → security → archive → snapshot.debian.org/file/<sha1>), dependency check first.
- Survives: dhclient enter hook, dhcpcd `nohook resolv.conf`, PVE LXC `.pve-ignore.resolv.conf`, resolvconf head/tail/base,
  openresolv `name_servers` + `resolv_conf_local_only=NO`, `rotate` removed. Backups + rollback in the plan-239 layout
  (`/root/dns-backup-<date>/orig.tgz, pre-b.tgz, created.list, rollback.sh`; an existing 239 dir and its rollback.sh are reused).

## Checklist
- [x] 1. Read plan 239 (apply.sh, plan_b.py, phase_b.py, port53.py, dl.py, rollback) + map agent / backend / frontend
- [x] 2. Write `go-gost/x/dnscache/flvx-dns-cache.sh` (ensure / status / check / rollback), port of 239
- [x] 3. Lab (systemd containers on the dev box, 27 scenarios): Debian 11 (security 404 → archive fallback) / 12 / 13,
       Ubuntu 22.04 / 24.04; static, Debian resolvconf, openresolv, dhclient hook, resolved stub + uplink; low-RAM pinned
       dpkg (11/12/13), 40 MB → minimal, :53 busy → minimal, no ::1, dead upstream, CN resolver, re-run idempotence
       (byte-identical), rollback all (identical to original) + b; 239 layouts (static, openresolv, stub, Phase-A) → no-op
- [x] 4. Agent: `flvx_agent dns-cache …` subcommand, `DNSCacheEnsure` command (systemd-run --wait outside the agent
       cgroup, single flight, 20 s gap), `dns_cache` detector (start + every 10 min) + handshake `dnsCache=1`, go tests
- [x] 5. install.sh: `setup_dns_cache` after the agent is up (zone from the panel address), one-line ✅/⚠, opt-out, old binary skip
- [x] 6. Backend: `dns_cache` per session (memory only, no DB column), admin node list `dnsCache`, `/node/dns-cache/ensure`
       (admin, online + capable, per-node single run + 60 s cooldown, panel zone list), landing-enable trigger, unit + contract tests
- [x] 7. Frontend: `pages/node/dns-cache-badge.tsx` — row chip + ⚠ alert (grid + list), dropdown with reason + 「修复 DNS 缓存」
- [x] 8. Screenshots desktop (light / dark / list) + H5 — local panel :18640 + vite :18641 + fake agents (port 6365 blocked)
- [x] 9. Real idle hosts (Vmsilo TW, Bestvm HK STD, 11-08 ~18:05 TW): status + dry-run + real `ensure` = 「无需改动」,
       DNS files md5 identical, resolved / agent PIDs unchanged, no files left
- [x] 10. Agent check cost: 10 µs (resolv.conf not local) – 0.95 ms (one /proc pass, 263 procs) per 10 min; dnsmasq RSS
       4.3–5.0 MB (cgroup 0.8 MB). go test backend ./... + agent socket/dnscache/landing green; tsc green, eslint = base
- [x] 11. Push, CI green (CI Build Check 42470160205 on 1455ba60: frontend, backend, PG contract, agent), RELEASE-LEDGER 待发布 row; final.md + summary.md (Chinese)

## Release notes (for the chain that ships it)
- Panel image: node list `dnsCache`, `/node/dns-cache/ensure`, landing-enable trigger. No DB schema change.
- Agents need OTA to show the chip / take the fix button; old agents show 「未知」 (节点程序需升级). install.sh from the same
  release sets up the cache on new nodes.
- The panel's zone list today: imgamer.top + nexkr.cloud (landing host `bfku2y9v.client.nexkr.cloud`). On plan-239 dnsmasq
  hosts a 「修复」 click therefore adds `server=/nexkr.cloud/…` once (config rewrite + one dnsmasq restart, ~0.1 s, fallbacks
  answer meanwhile); everything else is a no-op.
