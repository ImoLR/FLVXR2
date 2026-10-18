# 106 — Landing page font size

Requested by the user on 2026-10-18: text on the new 落地 page is too small, especially the read-only
「妙妙屋共享上限（只读）」 block. The smallest text should be at least the size of the 「妙妙屋接入设置」 card
title (16px), and other text should scale up proportionally.

## Decisions
- Scope: the 落地 page and its own dialogs (create node, links, delete, node details, server settings).
  Landing pieces embedded in existing screens (user edit 落地 section, rule target picker, 节点页「启用为落地」)
  keep their current size so those existing dialogs stay consistent.
- One scoped class `flvx-landing-text` in `globals.css`: every small text class, chip, button, label, input and
  table cell → 16px. Headings scale: page title 24 → 30px, card / dialog titles → 20px, section titles → 18px.
- Chips and table headers don't wrap; node-details first column doesn't wrap.

## Tasks
- [x] Scoped CSS + tag page root and landing dialogs
- [x] Fix wrapping regressions (status chip, node-details labels)
- [x] Build, lint (no new warnings), before/after screenshots desktop + H5
- [x] User approves screenshots (2026-10-18: 可以 發布上去吧)
- [ ] Release (after plan 105's release) + prod upgrade
