# 184 — 最优延迟彩色小标签 + 隧道手动「显示地区」

Base: `3.0.27-fork.104` (`maintenance/3.0.27-fork.104-socks-strict`, a50e3896).
Branch: `feature/3.0.27-latency-tag-region`. BUILD ONLY (release + prod data step in the next batch, after the user
approves the screens).

User request (2026-11-01, session 9db8c70d):
1. Rules page 「中转 xx → 出口 xx · 38 ms」: the ms becomes its own small coloured tag (< 100 green, 100–180 amber,
   > 180 red; a range uses the larger number). Same on the 隧道 page 最优 chips.
2. Tunnels get a manual 「显示地区」 (admin), default 自动 (= exit-node region, port-forward = entry region). Users pick
   tunnels by region, so e.g. an HK-exit tunnel built for Taiwan (HKIX → Hinet) must sit under 台湾.

## Design

### Part A — latency tag (frontend only)
- `utils/latency-tone.ts`: thresholds `LATENCY_GREEN_BELOW_MS = 100`, `LATENCY_RED_ABOVE_MS = 180`; tone of the
  displayed (rounded) number; `<1 ms` = green; range → larger number.
- `components/latency-tag.tsx` `LatencyTag`: tinted chip in the existing palette (`landingToneClass` green/amber/red),
  「含落地」 mini chip attached inside the tag. `LATENCY_TAG_STYLE = "sibling" | "nested"` build-time constant:
  A sibling (default) = separate chip right after the route pill (same line, wraps with it);
  B nested = chip inside the pill with its own background (screens only, for comparison).
- Used by: rules page `RuleBestPickLine` (single + multi dropdown trigger), `PickItemRow` (dropdown rows),
  tunnel page `BestPathLayerChip` and its per-owner detail rows. Non-applied states (等待探测, 节点离线 …) show no tag;
  「规则最优已自动关闭」 chip and candidate chips (当前 / 不可达 colours) unchanged.

### Part B — tunnel 显示地区 override (backend + frontend, additive DB)
- `tunnel.region_override varchar(8) not null default ''` (AutoMigrate, SQLite + PostgreSQL). Empty = automatic.
- `loadTunnelRegions` (same single query, one extra column `t.region_override`) → `deriveTunnelRegions`: override set
  → `ExitRegions = [override]`, `AutoExitRegions` = the real exit regions. Every consumer of `exitRegions` (tunnel page
  groups / filter pills / region chip / colours, rule picker sections, user tunnel list) follows automatically.
- Admin `/tunnel/list` additionally returns `regionOverride` + `autoExitRegions` (real exit). User lists
  (`/tunnel/user/tunnel`) return only the chosen region — no real exit, no node names/IPs.
- Create / update accept `regionOverride` (absent on update = keep), validated (two-letter ISO country code or empty).
- New admin endpoint `POST /api/v1/tunnel/region {id, regionOverride}`: DB-only, NO node push. Reason: `/tunnel/update`
  redeploys the whole tunnel (every rule's UpdateService → live connections drop, see plan 155). The edit form uses it
  when 显示地区 is the only change; the release worker uses it for the prod data step.
- Entry split (plan 155) twin inherits the override; merge keeps the original's. Backup raw export/import carries the
  column (raw table dump; old backups → default ''). Typed 1.x export gets `regionOverride` (import of 1.x is refused
  anyway). Federation payloads carry no regions (checked).
- Admin tunnel form: 「显示地区」 select (「自动（按出口：香港）」 / 「自动（按入口：…）」 for 端口转发 + the node region list),
  hint 「用户选隧道时按这个地区分组；不影响实际线路」. Admin tunnel page: overridden tunnels show 「手动」 on the region chip
  (or alone in region-tree boxes where the chip is hidden); tap → 「实际出口：香港」 (tap only, plan 156).
- Frontend fallback (exitRegions missing) respects `regionOverride`.

## Tasks
- [x] Plan doc + branch/worktree, WIP push
- [x] A1 latency tone helper + LatencyTag component (variant constant)
- [x] A2 rules page pick line + dropdown rows
- [x] A3 tunnel page 最优 chips + detail rows
- [x] B1 model column + normalize/validate + deriveTunnelRegions override + list outputs
- [x] B2 create/update/region endpoint + split twin + typed backup field
- [x] B3 unit tests (deriveTunnelRegions type 1/2 override) + contract tests (create/update/region/list, invalid code,
      non-admin view)
- [x] B4 frontend: form select, 手动 marker, fallback, region-only save via /tunnel/region
- [x] Gates: go build/vet, go test ./... (SQLite), PostgreSQL contract, tsc/build/lint vs base
- [x] Demo panel + screens gallery :8791 /latency-tag-region/ (390 + 1440, light + dark, A vs B)
- [x] Resource numbers (tunnel list latency before/after, bundle delta)
- [x] release-notes.md for the release worker, push, CI green
- [x] Summary

## Result (build, 2026-11-01)
- Backend + frontend done; `loadTunnelRegions` keeps its single query (one extra column `t.region_override`).
- Tests: unit + contract (SQLite + PostgreSQL) green; `TestExternalNodesBothDialects` and the
  `internal/leikwan/testapi` vet warning fail identically on unmodified fork.104 (pre-existing).
- Local prod-DB copy (read-only copy, deleted after): migration clean, all 40 tunnels' regions identical to fork.104
  before the data step; the data step (4, 61 → TW; 57, 105 → HK; 39 → JP via /tunnel/region) changed only those 5.
- Gallery `/root/mmwx-preview/latency-tag-region/` (:8791), 50 screens, 64/64 browser checks; variant A (sibling)
  is the default, B (nested) for comparison — user decides.
- Release notes for the batch release worker: `runs/latency-tag-region/release-notes.md` (panel only, no OTA, additive
  column, data step via /tunnel/region, rollback = panel rollback.sh, column can stay).

## r2 对齐隧道写法 (latency-tag-align, 2026-11-01)
User (session e42e8ad8): the rules-page 最优 line must read like the 隧道 page chips — 「中转最优：东京B → 出口最优：新加坡A」
+ the round-1 ms tag; keep ALL of round 1 as built (incl. the ms tag on the 隧道 chips). Frontend only.
- Pick line: each layer `${label}最优：${name}`, joined by 「→」; multi 「出口最优：2 个节点」; ms tag (A sibling / B nested)
  unchanged, 含落地 stays in the tag. Non-applied states keep their text (no ms tag).
- Always tappable (tap/click, never hover): the single-pick line also opens the popover — hint (`RULE_BEST_PICK_HINT`)
  + item row(s); ChevronDown on every line; `stopPropagation` as before.
- Dropdown rows: 「中转最优 东京B → 出口最优 新加坡A」 + ms tag.
- Phone: wraps only between hops; a long node name truncates inside its hop (full name in the popover); no horizontal
  scroll in the rule card.

- [x] R2-1 Pick line wording + always-tappable popover (single + multi), dropdown row wording
- [x] R2-2 Gates: tsc / build / lint vs round 1; backend untouched (git diff)
- [x] R2-3 Demo panel + gallery :8791 /latency-tag-align/ (before/after, A vs B, 390 + 1440, light + dark, tunnel chips)
- [x] R2-4 Bundle delta vs round 1 and vs fork.104
- [x] R2-1b Phone fix: the pick line's box is capped at the card width (`max-w-full`) — before, a long node name pushed
      the pill past the card edge (also on fork.104); now the name truncates inside its hop
- [ ] R2-5 release-notes.md + push + CI green
- [ ] R2-6 Summary
