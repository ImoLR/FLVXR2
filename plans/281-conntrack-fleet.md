# 281 — conntrack limit on every machine + conntrack monitor with TG alerts

Task `conntrack-fleet` (11-18, user approved 「按你推荐的做」). Ops only — no panel / agent code change, no release.

Trigger: Boil Hinet 158 (mmwx server 12 = FLVX shared landing 7) logged `nf_conntrack: table full, dropping packet`
(max 8192) on 11-16 08:52, 11-17 17–18 h / 23:29, 11-18 00:47 / 01:18 / 01:21 TW; usb / usb2 report 断断续续.

## Decisions
- `net.netfilter.nf_conntrack_max`: RAM ≥ 900 MB → 65536, smaller → 32768, never lower a higher host (NexKR 262144 kept).
  Files: `/etc/sysctl.d/90-flvx-conntrack.conf` + `/etc/modules-load.d/flvx-conntrack.conf` (`nf_conntrack`), applied
  with `sysctl -p <file>`; backup `/root/conntrack-backup-<TS>/values`. No restarts, timeouts unchanged, buckets unchanged.
- Skipped (user: leave alone): Nobrand 5cm IPLC 沪日 (`nbnet`/`mmwx-kfc5`), Nobrand IPLC 沪港 (`mmwx-nb17`), and
  Nobrand KFC 1cm (`mmwx-kfc1`, the user's 「1厘米」 on 11-07).
- Table-full signal = `early_drop` increase or `drop − insert_failed` increase. `insert_failed` alone = clash drops
  (parallel UDP/DNS inserts), present on most hosts with an empty table → not an alert.
- Per-host `flvx-ctmon.timer` (60 s, POSIX sh + awk) → `/var/log/flvx-ctmon/ct.log` (rotate at 480 KB, keep 1 old)
  + snapshot (top-10 dport / src / dst / proto-state) only when ≥ 70 % or table-full drops, ≤ 1 per 5 min, keep 50, ≤ 2 MB.
- Central `flvx-ctmon-collect.timer` on the dev box (every 5 min, sequential SSH, one session per host, one retry on
  failure because the dev-box path loses SYNs) → TG via @Fxboard_bot sendMessage (never getUpdates) to the bound admin chat.
  ⚠️ ≥ 70 % (≤ 1/h, again at ≥ 90 %), 🚨 table-full drops (≤ 1 per 30 min, with snapshot), ✅ < 50 %, 📡 3 failed runs,
  📊 09:00 TW daily (hosts ≥ 30 % or with drops).
- No persistent SSH masters (would keep an sshd process resident on the 470 MB boxes).

## Files (outside the repo)
- `/root/flvx-workers/ctmon/`: `flvx-ctmon` (host script), `host-setup.sh` + `push.sh` (install one host),
  `refresh.sh` (update script everywhere), `hosts.txt` (alias|name), `flvx-ctmon-collect`, `flvx-ctmon-uninstall`.
- Dev box: `/etc/systemd/system/flvx-ctmon-collect.{service,timer}`, state `/var/lib/flvx-ctmon-collect/`.
- Token: `/root/flvx-workers/secrets/ctmon-tg.env` (0600).
- Hosts: `/usr/local/sbin/flvx-ctmon`, `/usr/local/sbin/flvx-ctmon-uninstall [--limit]`,
  `/etc/systemd/system/flvx-ctmon.{service,timer}`, `/var/log/flvx-ctmon/{ct.log,snap/}`.

## Checklist
- [x] Boil Hinet 158 limit 8192 → 65536 (coordinator, 17:01 TW) and monitor running (17:06 TW)
- [x] Fleet key + aliases for the 3 password-only hosts (Bestvm HK 年付, Leikwanhost 沪台IPLC, Vmsilo TW)
- [x] Limit + monitor on all reachable machines (37 incl. dev box), per-host results recorded
- [x] Drop detection excludes clash drops; script refreshed on all hosts (md5 78693265)
- [x] Collector + timer on the dev box, TG test message sent (ok)
- [x] First scheduled collector runs verified; costs measured
- [x] Boil Hinet 158 cause: retro sources checked, findings written (peak follow-up = task `conntrack-158-peak`)
- [x] Memory `conntrack-monitor` + MEMORY.md line, inventory updated
- [x] Summary `/root/flvx-workers/runs/conntrack-fleet/summary.md`

## Results (11-18 17:0x–17:40 TW)
- Raised: Boil Hinet 158 8192→65536, Boil HKT 99u 8192→65536, AWS HK 7680→65536, Dmit T1 32768→65536, Leikwanhost 沪台IPLC
  32768→65536, Lightlayer TW 32768→65536, Vmsilo TW / vmsilo 沪港 / 沪日首发 / 沪美 / Vmsilo KR / Dmit Nathan / Dmit Echo 8192→65536;
  small boxes → 32768: PQS TW, Breadcloud JP, Bestvm HK 年付 / sg 年付 / HK STD (4096), Bestvm TW / JP B / US, Datawave HK 2 / TW 1 (16384).
  Already 65536 (file written to persist): Boil Hinet 马年, Fachost seednet 黑五, Tcscloud Seednet, Breadcloud JP2, Breadcloud US 年付,
  火山广州, 火山上海, Mkcloud 深港ixp 1/2, Mkcloud 沪日ixp 440, 百度云广州, 阿里广州, dev box. NexKR 262144 kept (no file).
- Unreachable: Breadcloud US, Mkcloud 沪港ixp 新春版, 阿里深圳 (commented out in hosts.txt).
- early_drop since boot ≠ 0 (table was full at some time, evictions are silent): Breadcloud JP 8.33 M (11-09 incident),
  Boil Hinet 158 4.43 M, Lightlayer TW 104 788, 火山上海 42 915, Bestvm TW 31 036, Bestvm US 27 604, vmsilo 沪日首发 3 939.
- Cost: host run avg 14 ms CPU (max 36 ms), wall ≤ 74 ms, ~86 KB/day log (cap ~1 MB); snapshot of a full 65536 table ≈ 0.3 s,
  < 1 KB each, only ≥ 70 %. Collector 130 s wall / 1.6 s CPU / 14 MB per run, 36 SSH sessions per 5 min, sequential.
- TG test via @Fxboard_bot → admin chat: ok. Offline alert-logic test (warn / no-repeat / ≥90 / 🚨 / ✅ / 📡 / 日报): pass.
- 158 retro: journal only since 11-12; 49 ratelimited "table full" lines vs 4.43 M silent early drops since 10-07 boot;
  no access log, no per-hour mmwx data, no FLVX node metrics (shared landing). Live mix 17:0x: TIME_WAIT ≈ 70 % (120 s timeout),
  outbound tcp/443 top, loopback FLVX core API 127.0.0.1:38505 ≈ 100–230 entries steady; BT already blocked in xray.
