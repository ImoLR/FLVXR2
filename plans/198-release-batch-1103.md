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
- [ ] Gates on release head: backend build/vet, go test SQLite + PostgreSQL, frontend tsc/build/lint (vs fork.108)
- [ ] Push branch → CI Build Check green; tag `3.0.27-fork.109` → Build and Push Images green
- [ ] Release validated: Latest, not prerelease, same asset set as fork.108, pins, ghcr amd64+arm64
- [ ] Baselines: panel CPU/RSS, health, nodes online, real-rule client check, runtime state; 救命版 health/WAL/online
- [ ] Backup (rollback dir + validated DB + compose + image tags) → upgrade `/opt/flvx-svc` incl. 195 compose change
      (ro mount of 救命版 data dir + `FLVX_OLDPANEL_DB`) → version + health → prune backups
- [ ] 196: @Fxboard_bot token from the ForwardX backup → getWebhookInfo idle → save via admin API → getMe = Fxboard_bot;
      token extract shredded; no chat bound; reminders ON with defaults
- [ ] Post-checks: probe 404 while switch off (anon + admin); settings probe card lists 救命版 machines default hidden
      (screenshot); bot card shows @Fxboard_bot, bind code generated (not bound); 197 wording in bundle/backend
- [ ] Business unchanged: real rules, nodes online, runtime state; 救命版 unchanged (API, WAL, online count)
- [ ] Resources after upgrade vs baseline; non-request log lines in 10 min
- [ ] Mark plan complete
