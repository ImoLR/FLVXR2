# 242 — release-1109: fork.127 (plan 240 本机 DNS 缓存常驻服务) + 全网 agent OTA

Base: `feature/3.0.27-dns-cache-default` @ a2814fa9 (code 1455ba60, base fork.126 a5966795) + docs 238 (a3fdb94d) + 241 (16edb50a).
Branch `maintenance/3.0.27-fork.127-release-1109`. Run dir `/root/flvx-workers/runs/release-1109-dns-cache`.

Brief: release ledger row 240 as `3.0.27-fork.127`, panel upgrade (backup first), fleet OTA of every online FLVX agent
(canary Leikwanhost 沪台IPLC, then one host at a time; skip Breadcloud JP2 = mmwx-prod and offline Nobrand ×2; never
04:40–06:10 TW), Dmit Echo last via panel OTA + one 「修复 DNS 缓存」 (resolv.conf backed up first; never touch its
`flux_agent.service`). Expected chip: 正常 on the 33 configured hosts, 精简模式 on Lightlayer TW.

## Tasks
- [x] branch + plan doc, gates (backend build/vet/test, go-gost/x dnscache + socket + landing tests) vs base
- [x] push branch, branch CI green, tag `3.0.27-fork.127`, Images workflow, release Latest + assets validated
- [x] panel backup + rollback dir (no schema change → image swap), upgrade /opt/flvx-svc, health + node state
- [x] OTA canary Leikwanhost 沪台IPLC (backup, RSS before/after, chip state, ensure no-op check)
- [x] OTA the rest one host at a time (SSH backup each), chip state after each
- [x] Dmit Echo: panel OTA, resolv.conf backup, 「修复 DNS 缓存」 once, report state
- [x] ensure no-op proof on 3 hosts (DNS files vs plan-239 backup timestamps, agent/core PIDs)
- [x] fleet DNS-cache state table, ⚠ list
- [x] ledger 240 → 已发布, prune backups, summary, plan complete

## Results
- Tag `3.0.27-fork.127` → 9318100f (= 240 head a2814fa9 + docs 238/241 + this doc). Branch CI 42532033944 green,
  Images/Release 42532176111 green; release Latest, asset set = fork.126, compose/install pinned to fork.127,
  gost-amd64 sha256 + `-V` ok.
- Gates: backend build ok, `go vet` only the known RouteRef unkeyed-fields warnings (pre-existing, also on fork.126),
  backend SQLite `go test ./...` rc=0, go-gost/x dnscache + socket + landing ok, go-gost root build ok. PG via CI.
- Panel 07:01:56 TW → fork.127 (rollback `/opt/flvx-svc/rollback/pre-fork127-20261108T230137Z/rollback.sh`,
  DB quick/integrity ok); 31/31 online; old agents showed 未知 as designed.
- OTA 07:03–07:23 TW, one host at a time, SSH backup each (`/root/flvxx-backup-<ts>-pre-fork127/restore-agent.sh`):
  28 agents → fork.127, back in 2.5–12 s (火山广州 37 s: slow download). Skipped: Breadcloud JP2 (mmwx-prod),
  Nobrand 5cm / Nobrand IPLC 沪港 (online but user said leave alone; still fork.126, chip 未知).
- Canary Leikwanhost 沪台IPLC: agent RSS 82.3 MiB → 62.0 (+1 min) → 68.8 MiB (+21 min), CPU 0.4 %; chip 正常 (dnsmasq).
- Every host: DNS files (resolv.conf, dnsmasq.conf, unit) md5/mtime identical before/after OTA; dnsmasq, resolved,
  landing Core, flux_agent (救命版) PIDs unchanged; only flvxx restarted.
- ensure no-op proven (dry-run first, then real ensure → 「无需改动」, files + PIDs identical, mtimes = plan 239 run):
  Leikwanhost 沪台IPLC (dnsmasq), Bestvm sg 年付 (resolved), Breadcloud JP (dnsmasq + landing Core).
  Lightlayer TW dry-run would now install dnsmasq (MemAvailable 106 MB ≥ 60) → real ensure not run there.
- Dmit Echo: agent + resolv.conf backed up over SSH, panel OTA, chip ⚠ 「未启用本机 DNS 缓存（解析超时仍是 5 秒）」,
  one 「修复 DNS 缓存」 (5.9 s) → 正常 (dnsmasq, apt dnsmasq-base, upstreams 1.1.1.1 1.0.0.1 8.8.4.4, RSS 5.4 MB,
  public :53 closed); flux_agent untouched (PID same).
- Fleet chip after: 19 dnsmasq 正常 + 8 resolved 正常 + Lightlayer TW 精简模式 = 28/28, 0 ⚠. Business counts identical.
- Ledger 240 → 已发布 (fork.127). Backups kept: pre-fork126 + pre-fork127 (pre-fork125 + fork.124 images pruned).
