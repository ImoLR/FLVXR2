# 146 — Landing onboarding hints (spinning light ring)

Base: `maintenance/3.0.27-fork.85-landing-compact` (7f4b1022, fork.85 = newest release at start).
Branch: `feature/3.0.27-landing-onboarding-hints`. Build + screenshots + perf numbers only, no tag / release.

## Goal
New 落地 users don't know what to do after creating a node. Three spots get a 15 s "new" hint: a thin ring
with a small comet dot running around the element, then it disappears:
1. 落地 → 我的节点 card/table button 「用于新规则」 (newest active node the user owns).
2. Rule dialog 「目标类型」 select.
3. Rule list 「复制链接」 on the landing / 我自己的节点 rule that was just created (table, card, H5 card).

## Rules
- Client-side only: `localStorage["flvx.hint.v1.<userId>"]`, per hint `{done, trigger, played}`. No API.
- Hints 1+2 armed by a successful 落地 node create (`create-node.tsx`); hint 1 only if the node is the user's own.
  Hint 3 armed by a successful landing/chain rule create (trigger = forward id).
- Armed & not done → plays once for 15 s when the element is first on screen (IntersectionObserver), marked
  played at start; replays only after the next trigger. Click/open → done forever. At most one ring at a time.
- After 15 s the ring element is unmounted. Reduced motion → static ring.
- Animation: transform/opacity-only rotation of a conic-gradient layer inside a static masked ring; one lap
  (2.4 s) then a 2.6 s rest with no animation running (3 laps in 15 s); no rAF/interval. Reduced motion →
  static ring, no animation.

## Checklist
- [x] Plan doc
- [x] `utils/onboarding-hints.ts` state store + `components/onboarding-ring.tsx` (hook + ring) + CSS in globals.css
- [x] Bridge Select: optional `triggerOverlay` (relative wrapper around the native select / custom trigger)
- [x] Spot 1: landing.tsx actions(node) 「用于新规则」
- [x] Spot 2: rule-target.tsx 目标类型 select
- [x] Spot 3: chain-links.tsx ChainLinkButton (forward.tsx table/card/H5 unchanged call sites)
- [x] Triggers: create-node.tsx (hints 1/2), forward.tsx create success paths (hint 3)
- [x] tsc / lint / build vs base (no new errors/warnings), bundle delta
- [x] Perf measurement in Chromium (normal + 4× throttle, with/without ring, after 15 s)
- [x] Screenshots desktop 1440 + H5 390, light + dark, 3 spots mid-spin ×2, after 15 s, after click; frame strip
- [x] Gallery published under mmwx-preview `/landing-onboarding-hints/`
- [x] Push branch, CI green
- [x] final.md / summary.md

## Result (2026-10-24)
**Not done: performance threshold missed** (brief go/no-go: > 2 % renderer CPU unthrottled → no release).
- Code complete, CI green (40061067736), 106/106 browser checks, 62 screenshots.
- Renderer CPU while the comet runs ≈ +2.7 % (落地 page) / +3.1 % (rule dialog). This equals the cost of
  ANY running animation on these pages (a 1 px transparent element or the existing `animate-spin` loader
  cost the same): it is the compositor's 60 Hz frame cost of the page, not the ring's drawing. No per-frame
  Layout/Paint; rest phases and after 15 s = 0. With lap+rest: 15 s average +2.0 % / +2.6 %.
- Without GPU acceleration the software compositor process is ~98 % busy during each lap because the
  surrounding glass card (`backdrop-filter: blur`) is re-blurred every frame (≈3.5 % with the blur off).
- Left for the user to decide (accept, static ring, fewer laps). Numbers: runs/landing-onboarding-hints/final.md.

## Release (fork.88, gold ring)
User 2026-10-24 ~14:35 TW, after seeing the gallery and the cost (~3 % renderer CPU on the viewer's own device
for ≤ 15 s, server 0): ring colour gold instead of blue, otherwise ship as is → **2 % perf gate waived by the user**.
Branch `maintenance/3.0.27-fork.88-landing-onboarding-hints` from fork.87 head e4b5a801, plan 146 commits
cherry-picked without conflicts. Panel only, frontend only, no DB change, no OTA.
- [x] Cherry-pick plan 146 onto fork.87 (no conflicts; backend/agent untouched)
- [x] Gold colour: only `--flvx-ring` / `--flvx-ring-head` (light #f2a900 / #ffd84d, dark #fcc419 / #ffe9a8);
      glow, band, reduced-motion ring follow the variable; animation/timing/triggers/sizes unchanged
- [x] tsc 0 errors, vite build ok, eslint on changed files = fork.87 base (698 = 698, new files clean)
- [x] Gold gallery (spin frames + strip, light/dark, desktop/H5) → preview `/landing-onboarding-hints-gold/`
      (62 shots, 106/106 browser checks, 0 page errors, 0 saves; run dir runs/release-landing-onboarding-hints-gold)
- [x] Quick perf re-check of one view (record only): 落地·全部节点 1× — renderer 15 s avg 2.22 %, lap peak 2.85 %,
      after 15 s 0 %, no Layout/Paint per second (blue build: 2.1 % / 2.9 %) → same cost, gate waived by the user
- [x] Push, CI green (40079035413), tag `3.0.27-fork.88` → 2e93f2a7, Images 40079415294, release Latest,
      10 assets = fork.87 set, 13/13 release checks
- [x] Validated DB backup + rollback dir `pre-fork88-20261024T090759Z` (quick/integrity ok) + prune (pre-fork86 +
      fork.85 images removed); `/opt/flvx-svc` upgraded 2026-10-24 17:08 TW, healthy after 9 s
- [x] Prod checks: version.json `3.0.27-fork.88-mvm5qpcj`; served `index-CQMJBKbw.css` = local build, gold vars
      present; backend healthy; 30/30 online, agent versions unchanged; 30/30 reconnect redeploys 0 updated/0 failed;
      16/17 config tables + landing settings/servers identical (landing_node only last_counter_at); 10 min after:
      backend 27 MiB / 2–4 % CPU (fork.87: 37–42 MiB / 3–4 %), DB 57.11 MB, 29 log lines (fork.87: 54, user access)

Rollback: `bash /opt/flvx-svc/rollback/pre-fork88-20261024T090759Z/rollback.sh` (→ fork.87, no `--restore-db`).
