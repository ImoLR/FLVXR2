# 145 — 落地 page compact: same scale as other pages, equal server cards, 共享详情 button

Source: user 2026-10-24 voice message (落地 page feels one size bigger; server cards uneven; shared card text block
→ button). Brief: `/root/flvx-workers/briefs/landing-compact.md`. Base: `maintenance/3.0.27-fork.84-rule-picker-group-layer`
(fork.84, newer than the expected fork.81). Branch `feature/3.0.27-landing-compact`. Build + screenshots only, no release.

Root cause: plan 106 / fork.55 (b9d8f5d6) `.flvx-landing-text` forces every text/chip/button/label/input/th/td on the
landing page and its dialogs to 16px and bumps h1/h2/h3; the page also uses looser spacing than 隧道/节点.

## Tasks
- [x] Base build (dist-base) + BEFORE screenshots on prod-DB copy (local paneld 16365, offline netns)
- [x] Measure 隧道/节点 page values (header, body text, card padding/gaps) vs 落地
- [x] Drop `.flvx-landing-text` from every landing page / landing dialog / landing monitor use; keep the CSS only for the
      two 规则-page spots (forward.tsx chain progress, rule-target line-machine) so 规则 stays exactly as it is
- [x] Landing page density = other pages (page padding, header, section gaps, card grid)
- [x] Server cards: uniform size (fixed row structure, `h-full flex-col`, actions `mt-auto`, chip lines no-wrap + `+N`)
- [x] 共享详情 button + dialog (admin, shared only; warning colour + dot on sync error / notice; text-sm)
- [x] tsc / lint / build vs base; bundle size delta
- [x] AFTER screenshots (desktop 1440 + H5 390, light + dark), review, fix, retake; gallery on :8791
- [x] Push branch, CI green
- [x] final.md + summary.md

## Result (2026-10-24, build only — no tag, no release)
- Commits: bc3cb786 (feature), 1 follow-up CSS comment commit; CI Build Check green (40054156522, 40054169778).
- Decided: protocol chips use short labels everywhere (H5 `stackProtocols` column variant dropped); 监控页 落地 tab
  also back to normal size (it reuses landing monitor-view); 规则 page bits (forward.tsx chain progress, rule-target
  line-machine, chain-links dialog) keep `.flvx-landing-text` so 规则 stays exactly as before.
- Equal cards: `flvx-card-grid grid auto-rows-fr gap-4` + card `h-full flex flex-col`, one-line chip rows
  (`ChipLine`, overflow → `+N` dropdown), actions `mt-auto`. Measured: admin 6 cards all 264 px (desktop) / 262 px (H5).
- Bundle: dist 4,247,023 → 4,250,312 bytes (+3.2 KB); largest file 2.85 MiB < 5 MiB PWA limit.
- tsc 0; eslint total 96 errors / 3791 warnings (base) → 96 / 3762 (touched files auto-fixed); no new problems.
- Screens: /root/flvx-workers/runs/landing-compact/screens (82 shots, 30/30 checks, 0 page errors, 0 writes).

## Release (fork.85, panel only)
Branch `maintenance/3.0.27-fork.85-landing-compact` = feature head a254da29 fast-forward (already on top of fork.84
maintenance head 63af100c, no conflicts). Diff vs fork.84: `vite-frontend/` + this plan only (no go-backend/go-gost change).
Gate waived by user 10-24; release worker reviewed the gallery.
- [x] Release worktree, branch from feature head (fast-forward over fork.84)
- [x] Gallery review (servers/user/nodes/egress/monitor/settings/create/详情/+N; H5 + desktop; light + dark): no defect, no re-shoot (H5 全部节点 table status wrap = same as before, not plan 145)
- [x] go build + go test ./... SQLite + PostgreSQL vs fork.84 failure list (both identical: 15 env failures, 17 ok pkgs)
- [x] Frontend tsc / build / lint (tsc 0, build ok, dist 4,250,312 bytes = build round, eslint 96 errors / 3762 warnings = build round, no new)
- [ ] Push, CI green, tag 3.0.27-fork.85, Images, Release Latest with the fork.84 asset set
- [ ] Prod: validated DB backup + rollback dir pre-fork85-*, upgrade /opt/flvx-svc, prune
- [ ] Prod checks: version.json, healthy, nodes online, no migration (2nd restart), new UI in served bundle, real-JWT read-only checks (admin + user), config fingerprints unchanged, browser check (admin + user, desktop + H5)
- [ ] After 10 min: backend CPU/RSS, DB size, log lines vs fork.84
