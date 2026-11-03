# 199 — Hotfix: 设置 page too wide on phones after fork.109 (Telegram card rows) → 3.0.27-fork.110 (panel only)

Found in the fork.109 post-checks (release-batch-1103, plan 198), 11-03 ~11:00 TW, admin 设置 page at 390 px on prod:
every settings card was 436 px wide in a 366 px column, so the right ~70 px of all cards (incl. buttons such as 删除 /
switches / 保存) were cut off on phones. Business (rules, nodes, landing, subscriptions) not affected.

Cause: plan 196 「Telegram 通知」 card machine rows use `min-w-0 flex-1` + a `truncate` (nowrap) name line. A nowrap
line still counts fully toward the block's min-content width; long prod names + the 「转发面板 · 救命版同机」 chip gave
the card a min-content of ~436 px, and the settings grid column (grid item min-width:auto) grew to fit it. The demo
data had short names, so it did not show there. Hiding the card in the live page brought the column back to 318 px.

Fix: `w-0 min-w-0 flex-1` on the row text blocks (width 0 → no min-content contribution; flex-1 still fills the row).
Same hardening on the plan-195 probe settings rows (fit today, but a long display name would trigger the same).
Frontend only, 2 files, 3 class names. No backend / DB / agent change.

- Base: `3.0.27-fork.109` (50631bbe) → branch `maintenance/3.0.27-fork.110-tgbot-h5`; tag `3.0.27-fork.110`.

## Checklist
- [x] Reproduce on prod (headless Chromium, admin, 390 px): all 7 cards 436 px in a 366 px column; Telegram card is the cause
- [x] Fix + live check of the built dist (local server :18899 → prod API, writes faked): cards 318 px @390, 288 @360, 604 @1440
- [x] tsc 0 errors, vite build ok, eslint on the 2 changed files clean
- [ ] Push → CI green; tag → images green; release validated (Latest, assets = fork.109, pins, amd64+arm64)
- [ ] Backup → upgrade `/opt/flvx-svc` (keep the plan-195 compose lines) → health → prune
- [ ] Post-check on prod: card widths at 390, screenshots, bot/probe state unchanged, business + 救命版 unchanged
- [ ] Mark plan complete
