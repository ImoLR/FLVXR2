# 143 — 防滥用 all in 内置 + ✓/✗ state icons

Source: user 2026-10-24 (voice msg after using 出站路由 → 全局规则 on fork.78+). Brief:
`/root/flvx-workers/briefs/builtin-abuse-unify.md`. Base: `maintenance/3.0.27-fork.82-node-ddns`
(fork.82, newest released; already contains the 「强制 IPv4/IPv6」 label fix 645df5c5 / de526789).
Branch `feature/3.0.27-builtin-abuse-unify`. Build + screenshots only — no tag, no release, no prod change.

## Design
- 内置 防滥用 group = 屏蔽 SMTP 25 (TCP) · 屏蔽发信端口 465/587/2525 (TCP) · 屏蔽 Telnet/SMB 23、135-139、445
  (TCP+UDP) · 屏蔽 BT. Compiled as builtin abuse rules right after safety (same path as SMTP/BT).
- Settings columns `builtin_mail_ports`, `builtin_telnet_smb` (nullable bool, **NULL = off**, true = on), so
  new installs and pre-143 rows never start blocking silently. SMTP/BT keep NULL = on.
- Effective = global switch AND profile 「默认防滥用规则」 toggle (one toggle for the group).
- One-time migration (`global_seed_version` 1 → 2 marker): for the two abuse seeds in 全局·强制 (same
  matchers + block + no node scope, any id/name) → switch = enabled (OR over duplicates), rule removed
  from 强制. Seed id with edited matchers stays in 强制 (custom rule now; moving it would change blocking).
  Not found → switch stays off. 大陆 rules untouched. Fresh installs seed only the 2 大陆 rules.
- After a migration that changed anything: one normal re-sync of every landing server at startup.

## Checklist
- [x] Backend: AbuseSwitches + 4 default rules, duplicate check, warning text
- [x] Backend: model columns, settings read/save, migration + marker, fresh-install seeds, resync flag
- [x] Backend: admin/node 内置 views, save endpoint (mail/telnet), server-settings abuseRules labels
- [x] Backend tests (switches, migration cases, idempotent, seeds, line-chain + shared scope, presets)
- [x] Frontend: 内置 list 防滥用 group + ✓/✗ icons, switches, texts (global, profile, server settings,
      overview), preset 「内置已封锁」 for all 4, rule-list 「已停用」 chip → ✗ icon
- [ ] Frontend tsc / lint / build, bundle size
- [ ] go test ./... base vs branch, contract SQLite + PostgreSQL, -race on new code
- [ ] Prod-copy gate: blocked set per server identical, migration result, idempotent, 大陆 + 默认 untouched,
      compile time before/after
- [ ] Screenshots desktop 1440 + H5 390×844, light + dark; review; gallery on :8791
- [ ] Push branch, CI green; final.md + summary.md
