# 235 release-1107b — fork.124 = fork.123 + plan 234 (incl. 233)

User approval: 2026-11-07 ~17:15 TW 「可以」 after the 234 screens. Second (last) FLVX release of 11-07 TW.

- Base: `maintenance/3.0.27-fork.123-release-1107` @ dd9a4290 (tag 3.0.27-fork.123 → 612b7b46)
- Merge: `feature/3.0.27-switch-margin-settings` @ 31c1aa9b (contains 233 @ 74e78271)
- Branch: `maintenance/3.0.27-fork.124-release-1107b`, tag `3.0.27-fork.124`
- Panel: no schema change, 3 new `vite_config` keys + `auto_switch_counts` row on first count.
- Agent OTA (native landing): NexKR, Bestvm US only. Skip Breadcloud ×4 (vendor maintenance) and the rest.
- Settings stay at defaults 5 / 5 / 20 (do not change).
- Run dir: `/root/flvx-workers/runs/release-1107b`

## Checklist
- [x] Release branch created, 234 merged (no conflicts; diff vs fork.123 = 233/234 files + plan docs only)
- [ ] Gates: go build + go test ./... (SQLite + PostgreSQL contract), go-gost + go-gost/x tests, tsc / build / lint vs base
- [x] Baseline before anything (panel, nodes, rules, landing links, sub, 救命版, API latency, NexKR / Bestvm US agent, 最优 / Reality picks)
- [ ] Push branch + tag 3.0.27-fork.124; CI + images + release assets verified
- [ ] Panel backup (incl. DB) + rollback dir; prune backups (keep 2)
- [ ] Panel upgrade; re-check vs baseline; fork.123 binary still works with the new vite_config keys
- [ ] Defaults 5/5/20 unchanged, 最优 picks unchanged right after upgrade, card renders (desktop + phone), counts start at 0
- [ ] OTA NexKR (backup first), re-check landing links
- [ ] OTA Bestvm US (backup first), re-check
- [ ] Post-checks: /version.json, old-profile client ≤1 reload, clients rpc 403, card save round-trip same values, margin capability
- [ ] Repeat baseline, compare
- [ ] Ledger 233/234 → 已发布; temp leftovers 0
- [ ] Summary written; plan marked complete

## Results
