# 175 — Rule page: copy-link modal pill + admin user pick keeps groups collapsed + rule hierarchy inside open group (BUILD ONLY)

Base: tag `3.0.27-fork.99` (prod). Branch `feature/3.0.27-rule-row-tree`. Frontend only, no DB / backend change.
Follow-up to plans 163 (grouped by tunnel), 164 (compliance ack), 173 (accordion / sticky header / footer).

## Requests (user 2026-10-31 TW)
1. 规则页「复制链接」modal looks like the 落地 page 「节点链接」 modal: tinted Pill (直连 green / 经规则转发 purple),
   name, mono address, same card layout. Single-link case still copies directly.
2. Admin picking another user (alone) is NOT filter mode: groups start collapsed + plan-173 accordion. Real filters keep
   "all matches open", also combined with a user pick.
3. Inside an open tunnel group: drop the repeated tunnel name, give rules a parent → child tree structure
   (desktop grouped table + 390 cards), keep 173 accent/sticky/footer, drag/checkbox/copy-link/badges/flow/actions.

## Design notes
- Item 1: `pages/landing/link-card.tsx` (`LandingLinkCard`, `copyToClipboard`, `linkAddress`, `LandingClientConfig`) is the
  one card both dialogs render. Rule links are always 经规则转发 (chain links carry no kind → `forward`; landing links
  are filtered to this rule's `forward` entries); 直连 shows only for a link whose kind really is `direct`. The rule
  dialog switches from `flvx-landing-text` (16px scale-up) to `flvx-landing` so the pill/address sizes match the 落地
  page. One link with a URL still copies at once; a link-less entry (Snell: client configs only) opens the dialog
  instead of copying "". No API change.
- Item 2: `userPickActive` (admin picked another user) still counts in `activeFilterCount` (筛选(N) badge + 重置 stay)
  but is subtracted for `groupFilterActive`. Saved accordion key stays `userId:tunnelKey` (max one, page-wide): opening
  a group of user B replaces the admin's own saved group; 全部用户 shows that group open; the admin's own view then
  starts all collapsed (its saved key is not shown there).
- Item 3 (variant A = tree line, default): `--flvx-tree-x` = header padding + half chevron (30px desktop / 22px phone);
  header `::after` stub under the chevron (stays on the sticky header), desktop first column `::before` through line
  + `::after` elbow into the checkbox, last row a rounded └; phone cards indented right of the line, elbow at the
  card's first row. Rows inside a group: tunnel name + 倍率 line removed from the phone cards (strategy chip moves to
  the name row); desktop grouped rows never showed it; 列表 / 卡片 keep it. Desktop rule name text-sm medium, subtle
  zebra + teal hover; paused rules fade their content (opacity .6 on cell content / card body, not actions).
  Variant B (card-in-card + left rail) is only a CSS mock injected in the gallery screenshots, not in the code.

## Checklist
- [x] Plan doc
- [x] Item 1: shared link-list renderer (landing modal + rule copy-link modal), kind per link
- [x] Item 2: user pick ≠ filter mode for auto-expand; reset badge/count decision; saved open key across user switch
- [x] Item 3: tree hierarchy in grouped desktop table + grouped mobile cards; tunnel name dropped in grouped rows only
- [x] tsc 0 errors / eslint forward.tsx errors 16 = base 16, warnings 646 → 639, 0 messages in changed lines;
      landing.tsx / chain-links.tsx / link-card.tsx 0/0 / vite build OK
- [ ] Demo panel (synthetic DB, loopback netns) + headless checks + screenshots 390/1440 light/dark (a–g) + gallery :8791
- [ ] Commit + push, branch CI green
- [ ] release-notes.md + summary.md / final.md
