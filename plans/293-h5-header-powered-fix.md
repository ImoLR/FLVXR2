# 293 — H5 header username + Powered badge overlap

Task `h5-header-powered-fix` (11-22, user approved after the fork.160 report). Frontend only, panel-only release.
Base: tag 3.0.27-fork.160, branch `fix/3.0.27-h5-header-powered`.

## Problems
1. H5 top bar shows only 「admi…」 for `admin`: layered `max-width` caps (58 / 54 / 46 px on the user button text)
   from older globals.css media rules cut even short names.
2. H5 个人中心: the floating `.flvx-powered-badge` sits on top of the page footer's own 「Powered by im.gamer」
   (the profile footer is `fixed bottom-20`, so it is always next to the badge).

## Tasks
- [x] Lab: fork.160 paneld + fork.160 dist (base) vs new dist, headless Chromium, before screenshots
- [x] Fix 1: header = brand never shrinks, user button takes the rest; name ellipsizes only when it does not fit
- [x] Fix 2: one signature on pages that render their own Powered footer (hide floating badge there)
- [x] Check other H5 pages + desktop profile for the same overlap
- [x] `npm run build`
- [x] After screenshots (390 + 360, admin / CJK / long name, light + dark, profile bottom, other page bottom, desktop profile) → gallery
- [x] Commit, push, tag next fork, CI + Images + assets + Latest
- [x] Prod backup + rollback script, prune backups, swap
- [x] Prod checks: nodes, forwards, log 15 min, prod H5 screenshots
- [x] Memory + hand-off to manian-to-flvx

## Result (lab)
- Header (390/360): admin unchanged; admin_user and 游戏玩家小明 now shown in full; a 28-char name ellipsizes,
  brand and theme button stay in place (≥ 8 px gap to the wordmark).
- 个人中心 (H5): chosen option = one signature. The page footer (version + Powered by im.gamer) stays; the floating
  badge is not shown on /profile. Overlap true → false at 390/360 light/dark.
- Other H5 pages (dashboard, forward, node, tunnel, user, config): no in-page Powered text, badge unchanged.
- Desktop 个人中心: no overlap (footer text above, badge at the very bottom) → left unchanged.
- Gallery: http://104.145.236.29:8791/h5-header-powered-fix/

## Release (done 11-22 23:46 TW)
- Tag 3.0.27-fork.161 → d8bf4a0a, CI 45038524198 + Images 45038525964 green, Latest, 10 assets, gost sha256 ok.
- Prod swap 23:46:08–23:46:16 TW (frontend only, no DB change, no OTA). Rollback:
  /opt/flvx-svc/rollback/pre-fork161-20261122T154607Z/rollback.sh (→ fork.160). Backups kept: pre-fork160 + pre-fork161.
- Checks: 29/31 nodes online before = after (火山广州 / 火山上海 already offline), 64/64 forwards + 利群 tables
  identical, 5 busiest forwards diagnose/RTT unchanged, panel log 15 min 0 errors, backend RSS 39.9 → 32.6 MiB.
- Prod H5 个人中心 390/360/dark: badge gone, overlap false; dashboard badge unchanged.
- manian-to-flvx brief told: panel = fork.161, OTA target fork.161.
