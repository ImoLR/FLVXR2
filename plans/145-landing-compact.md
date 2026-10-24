# 145 — 落地 page compact: same scale as other pages, equal server cards, 共享详情 button

Source: user 2026-10-24 voice message (落地 page feels one size bigger; server cards uneven; shared card text block
→ button). Brief: `/root/flvx-workers/briefs/landing-compact.md`. Base: `maintenance/3.0.27-fork.84-rule-picker-group-layer`
(fork.84, newer than the expected fork.81). Branch `feature/3.0.27-landing-compact`. Build + screenshots only, no release.

Root cause: plan 106 / fork.55 (b9d8f5d6) `.flvx-landing-text` forces every text/chip/button/label/input/th/td on the
landing page and its dialogs to 16px and bumps h1/h2/h3; the page also uses looser spacing than 隧道/节点.

## Tasks
- [ ] Base build (dist-base) + BEFORE screenshots on prod-DB copy (local paneld 16365, offline netns)
- [ ] Measure 隧道/节点 page values (header, body text, card padding/gaps) vs 落地
- [ ] Drop `.flvx-landing-text` from every landing page / landing dialog / landing monitor use; keep the CSS only for the
      two 规则-page spots (forward.tsx chain progress, rule-target line-machine) so 规则 stays exactly as it is
- [ ] Landing page density = other pages (page padding, header, section gaps, card grid)
- [ ] Server cards: uniform size (fixed row structure, `h-full flex-col`, actions `mt-auto`, chip lines no-wrap + `+N`)
- [ ] 共享详情 button + dialog (admin, shared only; warning colour + dot on sync error / notice; text-sm)
- [ ] tsc / lint / build vs base; bundle size delta
- [ ] AFTER screenshots (desktop 1440 + H5 390, light + dark), review, fix, retake; gallery on :8791
- [ ] Push branch, CI green
- [ ] final.md + summary.md
