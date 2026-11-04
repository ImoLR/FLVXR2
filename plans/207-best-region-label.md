# 207 — best-region-label: rules-page 最优 line names the region interconnect (「日美最优」)

**Base:** tag `3.0.27-fork.112` · branch `feature/3.0.27-best-region-label` · BUILD ONLY (no tag, no release, prod untouched).

## Why
User 2026-11-04 TW evening: users don't understand 「中转最优：Bestvm JP B → 出口最优：Bestvm US」 on the rule line. Show the
region interconnect of the current pick instead — JP relay → US exit = 「日美最优」, HK relay → JP exit = 「港日最优」 — then the
same ms tag + 「含落地」. Logic unchanged (same pick, same ms, same 含落地, same popup, same tap) — wording only.

## Rule
- Regions = `node.region` of the node each 中转 / 出口 layer (最优 **and** fixed) currently uses, in path order (中转, 中转2, …, 出口).
  The entry is not included.
- Consecutive duplicates collapse (JP → TW → TW = 日台). ≥2 regions → short names joined, no arrows (「港日美最优」).
  Exactly 1 region → full name (「香港最优」).
- Short names: one shared table in `go-backend/internal/store/repo/repository_tunnel_region.go` next to the full names
  `regionName()` already uses (one table, full + short).
- A layer node without region / with a code the table doesn't know → that line keeps today's wording.
- Multi-target / multi-entry rows: header line uses the label; when the rows' labels differ, each row carries its own
  (「落地1 · 日美最优 [120 ms]」) and the header says 「最优线路」.
- Node names only in the tap popup; popup hint = 「日美最优 = 经日本中转、美国出口，每分钟…」.
- Non-applied states unchanged; 隧道 page chips (`pages/tunnel/best-path-chips.tsx`) unchanged.

## Data path (no new query / request / timer)
`ListChainNodesForTunnel` (the 60 s path-latency round's own chain query, already joined on `node`) selects one more column,
`node.region` → `ChainNodeRecord.Region` → the rule-best view's groups → `walkRulePick` records the region of the node used
at every layer → `regionLabel` / `regionHint` on each item and on the pick (+ `region` on each hop / layer). Additive JSON
fields; old frontends ignore them, a new frontend on an old backend shows today's wording.

## Checklist
- [x] 1. Shared region table (full + short) + exported `RegionName` / `RegionShort` in repo; `regionName()` uses it
- [x] 2. `ChainNodeRecord.Region` filled by `ListChainNodesForTunnel` (same query, one more column)
- [x] 3. `walkRulePick` region per layer (incl. fixed layers); label/hint builder; pick-level label when uniform
- [x] 4. Go unit tests: builder (dedupe, 1 region, unknown fallback, 3 regions, 中转2) + end-to-end pick via the real round env
- [x] 5. Frontend `best-pick.tsx`: line wording, rows with own label when mixed, popup hint, aria-label
- [x] 6. Gates: go build / vet / test (SQLite), PG contract if repo query touched; frontend tsc / build / lint vs base
- [x] 7. Rule-list API timing before/after on a demo panel with ~40 rules
- [x] 8. Demo panel + screens (desktop 1440 + H5 390, light + dark, line + popup) → gallery `/root/mmwx-preview/best-region-label/`
- [ ] 9. Push, CI green
- [ ] 10. `runs/best-region-label/release-notes.md` + summary; clean demo panels / temp files
