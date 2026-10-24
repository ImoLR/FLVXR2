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
- Animation: transform-only rotation of a conic-gradient layer inside a static masked ring; no rAF/interval.

## Checklist
- [ ] Plan doc
- [ ] `utils/onboarding-hints.ts` state store + `components/onboarding-ring.tsx` (hook + ring) + CSS in globals.css
- [ ] Bridge Select: optional `triggerOverlay` (relative wrapper around the native select / custom trigger)
- [ ] Spot 1: landing.tsx actions(node) 「用于新规则」
- [ ] Spot 2: rule-target.tsx 目标类型 select
- [ ] Spot 3: chain-links.tsx ChainLinkButton (forward.tsx table/card/H5 unchanged call sites)
- [ ] Triggers: create-node.tsx (hints 1/2), forward.tsx create success paths (hint 3)
- [ ] tsc / lint / build vs base (no new errors/warnings), bundle delta
- [ ] Perf measurement in Chromium (normal + 4× throttle, with/without ring, after 15 s)
- [ ] Screenshots desktop 1440 + H5 390, light + dark, 3 spots mid-spin ×2, after 15 s, after click; frame strip
- [ ] Gallery published under mmwx-preview `/landing-onboarding-hints/`
- [ ] Push branch, CI green
- [ ] final.md / summary.md
