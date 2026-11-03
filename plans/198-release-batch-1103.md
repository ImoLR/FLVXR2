# 198 — Release batch 1103: plans 194 + 195 + 196 + 197 as 3.0.27-fork.109 (panel only)

User 2026-11-02 (coordinator 3d7c53c7): batch release of 194 探针剩余天数标签, 195 救命版机器进探针 (read-only, admin picks
which show), 196 Telegram 续费 bot (@Fxboard_bot, 「已续费」), not before 2026-11-03 10:00 TW. Coordinator 6f979d30 added
plan 197 sub-link-hint (text/toast only). Panel only: **no OTA, no agent change**. DB: additive only (196: 3 new tables;
195: new probe settings fields). Prod settings: 公开探针页 switch stays OFF, probe visibility/groups unchanged.

- Base: `3.0.27-fork.108` (prod) → `feature/3.0.27-probe-expiry` cbf08527 (194+195+196, CI green) + merge
  `feature/3.0.27-sub-link-hint` c0046c02 (197, CI green, merged clean).
- Release branch: `maintenance/3.0.27-fork.109-batch-1103`; tag `3.0.27-fork.109`.
- Run dir: `/root/flvx-workers/runs/release-batch-1103`.

## Checklist
- [x] Pre-checks: prod = fork.108, latest tag fork.108, all 4 final.md clean, branch CI green, plan 198 free
- [x] Release branch (clean merge of 197) + this plan doc
- [x] Gates on release head: backend build/vet, go test SQLite + PostgreSQL, frontend tsc/build/lint (vs fork.108)
  - build ok; vet = only the known leikwan/testapi unkeyed-field notes; go test 25/25 packages ok SQLite and 25/25
    PostgreSQL (contract incl.), 0 FAIL
  - frontend: tsc 0 errors, vite build ok, eslint 94 errors / 3793 warnings = identical to fork.108 per file
- [x] Push branch → CI Build Check green (41566676523); tag `3.0.27-fork.109` → 50631bbe, Build and Push Images green (41566678414)
- [x] Release validated: Latest, not prerelease, same asset set as fork.108, pins, ghcr amd64+arm64 (13/13)
- [x] Baselines: panel CPU 13.73 s/5 min, RSS 43.6 MiB, 29/29 online, 13 real rules, 251 runtime objects; 救命版 48/48 online,
      API 0, WAL 8 553 152 B, container up since 08-15, 0 restarts
- [x] Backup `pre-fork109-20261103T025303Z` → upgraded 10:53 TW incl. 195 compose change
      (ro mount of 救命版 data dir + `FLVX_OLDPANEL_DB`) → version + health → prune backups (pre-fork107 removed)
- [x] 196: @Fxboard_bot token from the ForwardX backup → getWebhookInfo idle (url empty, 0 pending) → saved via admin API → profile Fxboard_bot;
      extract deleted + token file shredded; no chat bound; reminders ON 7/3/1/0 @ 10:00; token absent from config/list + config/get
- [x] Post-checks: probe 404 while switch off (anon + admin, via CF and local); settings probe card lists 22 救命版 machines, all hidden
      (screenshot); bot card shows @Fxboard_bot, bind code generated (not bound); 197 wording in bundle/backend
  - **Found: 设置 page too wide on phones (Telegram card rows) → fixed forward as fork.110, plan 199**
- [x] Business unchanged: real rules, nodes online, runtime state; 救命版 unchanged (API, WAL, online count)
- [x] Resources after upgrade vs baseline; non-request log lines in 10 min (measured on fork.110, see plan 199)
  - CPU 13.73 → 11.77 / 11.89 s per 5 min; VmRSS 43.6 → 66.4 MiB = anon 39.2 + 27.8 MiB paneld file pages (reclaimable;
    the embedded IP table is touched by the 救命版 region lookup); docker stats 45 MiB; DB 57.3 MB unchanged
  - non-request lines: 32 / 10 min, all node 48 Leikwanhost 沪台IPLC WS pong timeouts via Cloudflare SIN 11:29–11:31
    (8 min after the restart, same reconnect pattern as before), recovered 11:32, rules on 48 pass; `[tgbot] stopped
    (nothing to do)` after the bind code expired
- [x] Mark plan complete — prod runs 3.0.27-fork.110 (= fork.109 + plan 199)
