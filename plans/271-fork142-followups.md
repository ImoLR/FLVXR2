# 271 — fork.142 follow-ups: 利群备线流量 + 妙妙屋共享「离线」闪烁 + 预热池休眠 2 h

Branch `feature/3.0.27-fork142-followups` from tag `3.0.27-fork.141` (worktree `/root/projects/flvxx/flvxx-fork142`).
Panel-only release (expected `3.0.27-fork.142`). No agent OTA, no business node touched, no rule redeploy.

## 1. 利群备线流量

User 11-17 ~15:00 TW: after 日志快切 moves a 利群 rule to its backup, the bytes count on the 利群 rule AND on the
backup forward. Keep the double count, but the backup traffic must land on the 利群 rule owner, and there must be
a separate 备线流量 statistic.

### Finding (code + prod data, read-only)

- Code: `leiKwanCreateBackup` (handler/leikwan_line.go) creates every auto 备线 as an ordinary forward **owned by the
  first admin** (comment: "so it is no extra user rule and not billed twice"). Imported/linked backups (148, 587,
  595, 599, 601, 619) are hand-made admin forwards. `planForwardFlow` bills a forward to `forward.user_id` →
  the backup bytes go to the **admin's** 总流量 / per-server quota / pools, never to the 利群 rule owner.
- Prod 11-17 15:00 TW: all 7 利群 rules and all 6 backup forwards belong to admin `imolr` → today the bytes do land
  on the rule owner, but only because the owner happens to be the admin. The first granted non-admin user's backup
  traffic would be billed to the admin. → **needs a fix** (bill the 利群 rule owner).
- `forward.in_flow/out_flow` reset with the forward owner's (admin's) reset and the linked backups carry history
  from before the import → they cannot give "backup traffic in the 利群 owner's current period". → new counters.

### Design

- Flow ingest: a forward that is the 备线 of 利群 rules owned by exactly one user U (U ≠ forward owner) bills the
  user-level parts (总流量, quota usage, per-server quotas of its path, traffic pools, flow-policy check) to U
  instead of the forward owner, with the same landing exemption as the 利群 rule (rule's landing node owned by U).
  The forward's own counters stay on the forward. Two+ different owners on one shared backup = ambiguous → stays on
  the forward owner (no invented split; logged once).
- Every 利群 rule whose backup is that forward gets the forward's billed bytes in new columns
  `leikwan_rule.backup_in_flow / backup_out_flow` (NOT NULL DEFAULT 0, auto-migrate; fork.141 ignores them). They
  reset with the owner's reset together with `in_flow/out_flow` (resetUserForwardsTx). Shared backup = full forward
  bytes on each rule (no split).
- Lookup forward → 利群 owners: in-memory map, refreshed at most every 30 s (one tiny query), not per flow item.
- `SaveLeiKwanRule` omits the four counter columns (a handler read-modify-write must not overwrite bytes billed
  in between — also closes the same pre-existing race on in_flow/out_flow).
- API: rule view `backupInFlow/backupOutFlow` (+ `backupShared` = other rules' names for the tooltip, users get
  names of their own rules only); grant view `backupFlow` = Σ over the user's distinct backup forwards of the
  largest rule counter (a shared forward counts once).
- UI (add-only): chip 「备线 x MB」 after the 利群 traffic chip (admin row, user row, desktop + H5 expanded row),
  hidden when 0; tooltip explains shared backups. 授权 card chip + user header chip 「备线流量 x」 (hidden when 0).

## 2. 妙妙屋共享「离线」闪烁

### Finding

- `landing_integration.sync_error` / `landing_server.shared_sync_error` are display-only (landing.go server + node
  views, landing_monitor.go, landing_layers.go) plus the delete gate (repository_landing_lifecycle.go:144, per-server
  `shared_sync_error`). No forward/landing data-plane path reads them. Re-verified 11-17.
- `sharedAttempt` writes ANY non-scoped failure (one status poll, one push, one user-limits PUT) into the single
  global `landing_integration.sync_error`; the views copy that onto EVERY shared server (online=false) and EVERY
  shared node (「同步异常」) until a fully clean round. The transport breaker then blocks the other servers' due
  requests for 15 s, which also write their own error. One failed request → everything 离线 for 5–60 s.
- Failures are not logged; every transport error is the same 「妙妙屋连接失败，请检查地址或网络」.

### Design

- landingcustom: transport errors get a class (dns / timeout / refused / reset / unreachable / tls / eof / net) with
  distinct Chinese text; HTTP errors stay `http <code>`.
- Handler: `sharedSyncHealth` (own mutex) records per request key (shares / hosts / status:N / push:N /
  user-limits:N / probe): failing since, last message, class. Log: one line per key+class per minute, one line when
  it clears, breaker-open skips aggregated (one line per minute).
- Views: one threshold `sharedOfflineAfter = 3 min`. A transient (transport / 5xx / breaker) per-server error is
  shown only when the server had no successful status poll for ≥ 3 min or the failure streak is ≥ 3 min. The global
  integration error is shown on a server only when the global requests (shares/hosts) have been failing ≥ 3 min or
  the server itself is stale. Verdict errors (409/422/404/401, list issues, holds) show at once as before.
- DB writes unchanged → delete gate still blocks on any stored per-server error. On a server's full recovery the
  transient per-server error is cleared (conditional clear of the same text).

## 3. 预热池 idle sleep 6 h → 2 h

- Panel `warmPoolIdleSleepS` 21600 → 7200; agent default `IdleSleepAfter` 6 h → 2 h (+ comments) — no OTA.
- Path: `idleSleepS` is part of the SetWarmPools payload (warmPool=2 agents = fork.136 code = fork.138/140 on the
  fleet). The panel keeps the last sent signature in memory only → after the panel restart every v2 agent gets the
  new payload on reconnect / the first 30 s reconcile; the agent applies it live (`applyLimitsLocked`, read on the
  5 s tick). No rule service or chain is re-created; only idle pooled conns of keys idle > 2 h are closed.

## Checklist

- [x] Investigate 1/2/3, write this plan
- [x] 3: panel + agent constant + comments (tests updated)
- [ ] 3: note the change time in plans/264-warm-redundant.md (after the release)
- [x] 2: landingcustom error classes + messages (+ unit test, live refused/timeout/hang-up/502)
- [x] 2: sharedSyncHealth tracker + logging + view threshold + per-server display + recovery clear (+ probe log)
- [x] 2: tests SQLite + PG 18 (1 failure → online; ≥3 min → only that server offline; list outage; recovery clears; delete gate still blocks; log rate limit)
- [ ] 1: model columns + SaveLeiKwanRule omit + reset + FlowBatch LeiKwanBackups
- [ ] 1: flow ingest re-attribution + backup counters + owner cache
- [ ] 1: API fields (rule view, grant view, user grant)
- [ ] 1: contract tests (owner billed, admin not, reset, shared backup, ambiguous owners)
- [ ] 1: UI chips (admin + user, desktop + H5, 授权 card, user header)
- [ ] go test ./... (go-backend SQLite + PG 18 contract, go-gost/x warmpool), go vet, npm run build
- [ ] Screens gallery /root/mmwx-preview/fork142-followups/ (admin + user, 1440 + 390, light + dark)
- [ ] Commit, push, CI green; ledger row 271 待发布
- [ ] Tag 3.0.27-fork.142, Images + 10 assets, Latest
- [ ] Prod: backup + rollback script, swap, verify (30/30, 52 rules, 利群 tables, redeploy failed=0, chips, shared online)
- [ ] Prune backups (keep 2), ledger 已发布, memory updates
- [ ] Watch new [shared-sync] log lines ~60 min → flap cause; stop + disable flvx-mmwx-sync-watch
- [ ] Summary (runs/fork142-followups/summary.md), plan complete
