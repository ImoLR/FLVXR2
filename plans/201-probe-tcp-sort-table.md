# 201 — 探针「TCP」排序 + 默认表格视图

Base: branch `feature/3.0.27-sub-source-cap2` (plan 200, unreleased; prod = fork.110) · branch
`feature/3.0.27-probe-tcp-sort` · build only (ships in the next batch release together with plan 200).

User 2026-11-03: no special phone layout for now (「暂时先不要改」). Only:
1. Public probe page (`vite-frontend/src/probe/`): sort option **TCP** (order 默认 / CPU / 网速 / TCP / 到期; 到期 stays last because it only shows when the snapshot has expiry days) — by the TCP
   connection count each server already shows, descending, offline servers last, ties keep the default order
   (stable sort, same as CPU / 网速). Works in 卡片 and 表格; persisted in `flvx-probe-sort` (`"tcp"` allowed).
2. Default view = **表格** for everyone (desktop + phone). A stored choice in `flvx-probe-view` is kept.

No backend change: `probe.Node.TCP` (`json:"tcp"`) is already in the snapshot (`go-backend/internal/probe/types.go`).
Phone toolbar: with 到期 shown, the extra TCP button pushed 卡片/表格 past the screen edge at ≤390 px (at 360 px the
表格 button ended at 389 px). The toolbar wrapper now has `flex-wrap justify-end`: on narrow phones 卡片/表格 drops to a
second line (right-aligned); 414 px and desktop stay on one line, unchanged. No other layout change.

Resource efficiency: sorting is client-side over the existing snapshot — no new requests, timers or fields.

## Checklist
- [x] Verify snapshot carries the TCP count (no backend change)
- [x] `SortKey` + stored list + 排序 segmented get `tcp`; sort uses `n.tcp`
- [x] Default view `"table"` (stored choice still honoured)
- [x] tsc / build / lint vs base (no new errors)
- [x] Phone toolbar overflow from the extra button → wrap (measured 360/375/390/414)
- [x] Screens desktop 1440 + H5 390, light + dark (gallery :8791 /probe-tcp-sort/), demo data only — 40/40 checks pass
- [ ] Push + CI green
- [ ] Release notes `runs/probe-tcp-sort-table/release-notes.md`
