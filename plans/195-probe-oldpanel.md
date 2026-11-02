# 195 — 救命版的机器也能放进探针（只读救命版数据）+ 自选上探针的机器

Branch `feature/3.0.27-probe-expiry` (after plan 194, same worktree). BUILD ONLY + screens; the coordinator batches it
into a later release. Brief: `/root/flvx-workers/runs/probe-oldpanel/brief.md` (user 2026-11-02, coordinator 3d7c53c7:
「可以，就读取救命版的数据吧」 + 「我只会加进一些我想要的机器」).

## Facts measured before building (read-only, 2026-11-02)
- 救命版 DB = `/var/lib/docker/volumes/sqlite_data/_data/gost.db` (WAL, 48 nodes). Its `node` table has no region.
- **The 救命版 writes node_metric in batches**: rows arrive every ~10–20 s (~100–200 rows at once), one row per node per
  5 s. The newest row of an online node is therefore 0–25 s old. A `timestamp > now-15s` query returns *nothing* for
  most nodes right before a flush, and a 30 s online threshold would flap. → incremental cursor + 45 s online threshold.
- `SELECT … WHERE timestamp > now-30s` = 0.4–1.2 ms (100–290 rows) on the live file.

## Design (as approved, plus the measured adjustments)

### Read path (`internal/oldpanel`, opt-in)
- Env `FLVX_OLDPANEL_DB` = path of the 救命版 `gost.db` inside the FLVX container. Unset → feature off, zero work.
- Opened with `file:<path>?mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)`; `database/sql`, max 1 open
  connection, idle connection closed after 60 s (so an idle panel holds no file handle / lock). Only constant SELECTs;
  every statement is an autocommit read (rows read fully, closed at once) → no long read transaction, the 救命版's WAL
  checkpoints are never held back. Never `journal_mode`, never a write, never a schema/index change.
- Node list: `SELECT id, name, server_ip, server_ip_v4, server_ip_v6, extra_ips, version, expiry_time, renewal_cycle, inx
  FROM node WHERE COALESCE(is_remote,0)=0` — re-read every 60 s, only while somebody looks.
- Live metrics, only while somebody looks, at most every 5 s: incremental `WHERE id > cursor` (rowid range, only the
  rows written since the last poll); bootstrap / after a pause `WHERE timestamp > now-60s` (time index). Newest row per
  node kept in memory. Online = newest row ≤ 45 s old.
- 「最后在线」 of an offline machine: one `MAX(timestamp) WHERE node_id = ?` seek (node_time index), once per machine.
- Series: `node_id = ? AND timestamp BETWEEN` + the same SQL bucketing as plan 187, cached 30 s, shares the ≤ 2
  concurrent series slots.
- Missing file / locked / schema mismatch / query error → 救命版 machines simply absent; one summary log line per
  10 min at most; never crash.

### Machine model
- Probe machine = FLVX node, or 救命版 node **not** matched to an FLVX node. Matching (addresses never published):
  same public IP literal (server_ip / v4 / v6 / extra_ips), same host name, or an IP literal on one side that the other
  side's host name resolves to (host names resolved off the build path, cached 10 min, failures 2 min). Two host names
  that merely resolve to the same IP do not match (Cloudflare-proxied names would collide). Private / loopback / CGNAT
  addresses never match. While a 救命版 host name is still resolving the machine stays out of the snapshot (no flash of
  a duplicate card).
- Matched pair → ONE card with FLVX data (FLVX expiry; if FLVX has none, the 救命版 one).
- 救命版-only machine: name (or 显示名), region = embedded GeoIP of its public address (same `internal/geoip` as the
  节点 page), OS/arch from its version string, metrics, rolling expiry (plan 194 helper), no latency.
- **Added after the live dry run:** GeoIP is wrong for several 救命版-only machines (Vmsilo US / JP / KR / sh-de have a
  private IPv4 + a China Telecom IPv6 → 「中国大陆」; Vmiss KR → HK), and the 救命版 has no region column we could fix
  (it must not be written). So 救命版 rows get a 「地区」 select in the probe card (default 「自动」 = GeoIP), stored in
  `probe_nodes.old_regions`.
- Public key = HMAC over a source-qualified id (`old:<id>`; FLVX keys unchanged, existing links/groups keep working).
  Internally a 救命版 machine is the negative id `-<old id>` (never leaves the panel; the admin API uses it too).

### Settings (设置 → 公开探针页)
- The list shows all machines (FLVX + 救命版-only) with a source chip for the admin (「转发面板」/「救命版」, FLVX rows
  that also run in the 救命版 get a small 「救命版同机」 chip), a search box and 「全部显示 / 全部隐藏」 (on the
  filtered rows). Chips only when `FLVX_OLDPANEL_DB` is set.
- **救命版-only machines default hidden**; later new ones too. FLVX nodes keep today's state.
- Storage, additive: `probe_nodes` gains `old_shown:[ids]` + `old_names:{id:name}`; `probe_groups` entries gain
  `old:[ids]`. Old rows keep loading; a rollback binary ignores the new fields.

### Custom groups (plan 189)
- 救命版 machines can be put in groups (negative ids in the admin API, `old` list in storage). Hidden machines never
  appear in groups.

## Checklist
- [x] `internal/oldpanel` reader (ro DSN, 1 conn, idle close, node list, incremental metrics, latest seen, buckets) +
      unit tests (fixture DB built in the test; write attempt refused; schema mismatch; missing file)
- [x] Matching + DNS cache + GeoIP region (pure functions + tests with a fake resolver)
- [x] Handler: poll gating via frame builds, snapshot merge, online 45 s, expiry fallback, series for 救命版 machines,
      log throttle
- [x] Settings: `old_shown` / `old_names` / group `old`, admin GET/save (negative ids), generic config API validation,
      default hidden
- [ ] Contract tests SQLite + PostgreSQL (whitelist byte scans incl. 救命版 machines, switch off, env unset, missing DB,
      matching by IP/host, default hidden, mixed groups, old-format settings)
- [ ] Frontend settings card: source chips, search, 全部显示/全部隐藏, hint; group picker works for 救命版 rows
- [ ] WAL safety: `:ro` vs rw mount test on a copy (docker), 10 min WAL size with reader vs without; short ro run on
      the live file
- [ ] go build / vet / test (SQLite + PostgreSQL); tsc / build / lint vs base
- [ ] Resource numbers (tick ms/rows, node refresh ms, RSS/CPU 0 / 10 viewers on vs off, snapshot bytes, idle = 0)
- [ ] Demo (fake 救命版 DB, 25+ machines) + screens gallery `/root/mmwx-preview/probe-oldpanel/`
- [ ] Push, CI green
- [ ] `runs/probe-oldpanel/release-notes.md` (compose mount + env, prod check, rollback) + summary
