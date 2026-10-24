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
- [x] Frontend tsc / lint / build, bundle size
- [x] go test ./... base vs branch, contract SQLite + PostgreSQL, -race on new code
- [x] Prod-copy gate: blocked set per server identical, migration result, idempotent, 大陆 + 默认 untouched,
      compile time before/after
- [x] Screenshots desktop 1440 + H5 390×844, light + dark; review; gallery on :8791
- [x] Push branch, CI green; final.md + summary.md

## Result (2026-10-24, build only — not released)
- go test ./... SQLite + PostgreSQL: failure sets identical to the fork.82 base (15 env-dependent failures);
  -race clean on landingegress / repo / handler / contract tests of this plan.
- Prod-copy gate: blocked set per server identical on all 6 landing servers, non-block rules identical, only
  发信端口 / Telnet-SMB moved ahead of BT; migration → mail=on, telnet=on, 强制 = 2 大陆 rules (byte-identical);
  second start changes nothing. Compile time unchanged (0.35–0.57 ms), bundle +2.0 KB.
- Screens: 56 shots, 52/52 checks, gallery /builtin-abuse-unify/ on :8791. CI Build Check green (40040635444).
- Rollback note: binary-only rollback to ≤fork.82 loses the two moved rules (they are no longer in 强制) —
  restore the DB backup too.

## Release (fork.83, panel only)
Branch `maintenance/3.0.27-fork.83-builtin-abuse-unify` = feature head 57b8c0ec fast-forward (already on top of
fork.82 maintenance head 6bad78aa, no conflicts). Gate waived by user 10-24; release worker reviewed the gallery.
- [x] Release worktree, branch from feature head (fast-forward over fork.82)
- [x] Gallery review (H5/desktop, light/dark): no defect, no re-shoot
- [x] go build + go test ./... SQLite + PostgreSQL vs fork.82 base failure list (both identical to base: 15 env failures, 17 ok pkgs)
- [x] Frontend tsc / build / lint (tsc 0, build ok main 2,980.57 kB, changed files 0 err / 6 warn = fork.82)
- [x] Prod-copy gate re-run (fresh read-only copy, base fork.82 vs release, idempotent), copy deleted — OK: 6/6 servers blocked set identical (3/5/6: 7 rules, only order changed; 1/2/4: no plan before or after), mail/telnet → on, 强制 = 2 大陆 (byte-identical), 2nd start no change
- [x] Push, CI green (40042704945), tag 3.0.27-fork.83 → 8f6dcca8, Images 40042872322, Release Latest with the fork.82 asset set (10 assets, amd64+arm64)
- [x] Prod: validated DB backup + rollback dir pre-fork83-20261024T001246Z, upgraded /opt/flvx-svc 08:12:53–08:13:06 TW, prune (pre-fork81 + fork.80 images removed)
- [x] Prod checks: version.json local+public fork.83, healthy, 30/30 online (29 fork.79 + node 48 fork.82), migration
      logged once (mail=on, telnet=on), extra restart: no repeat; post-upgrade prod copy: blocked set 6/6 identical to
      pre-upgrade plans, 强制 = 2 大陆 byte-identical, 默认 untouched, egress/shared sync errors empty, redeploy 0 updated
      0 failed; JWT: admin global/get builtin 4 on, user 403, anon 401, user own node rules/get builtin 4 on, other's node denied
- [x] After 10 min (08:24 TW): backend CPU 4.4–8.9 % / RSS 26.7 MiB (fork.82: 4.4–6.0 % / 38.6 MiB), DB 56.77 MB
      (56.58), backend log lines/10 min 0 (fork.82: 0); 30/30 online; user/forward/user_tunnel/landing fingerprints unchanged

**Released 2026-10-24 08:13 TW as 3.0.27-fork.83 (panel only, no OTA).** Rollback:
`bash /opt/flvx-svc/rollback/pre-fork83-20261024T001246Z/rollback.sh --restore-db` (DB restore REQUIRED — fork.82
without the DB backup would stop blocking 465/587/2525 and 23/135-139/445).
