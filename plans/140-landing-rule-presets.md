# 140 — Landing rule presets (出站路由 follow-ups after the fork.75 phone test)

Base: `origin/maintenance/3.0.27-fork.76-qa-fixes` (fork.76, newest released). Branch
`feature/3.0.27-landing-rule-presets`, worktree `/root/projects/flvxx/flvxx-landing-rule-presets`.
Build + tests + screenshots only — **no tag, no release, no prod change, no OTA** (UI waits for the user's
screenshot approval). Run dir `/root/flvx-workers/runs/landing-rule-presets`.

## Decisions

- 内置 becomes a real list (global page, profile editor, user node-rules page):
  - 安全规则「禁止访问内网 / 回环 / 链路本地 / 云元数据 / 组播」: `landingegress.BlockedCIDRs` shown collapsed
    (「展开 N 条」), LOCKED. The agent enforces the same list itself on every inbound (incl. line-chain copies).
  - 屏蔽 SMTP 25（TCP） / 屏蔽 BT: new nullable columns `landing_egress_setting.builtin_smtp` /
    `builtin_bt` (NULL = on = today). Effective per node = global switch AND the profile's 防滥用默认 toggle.
    Saving a switch = one debounced re-sync of every landing server (existing `scheduleLandingLayerApply`).
  - 来源白名单: FLVX node address count (auto) + admin whitelist, editable in place (existing
    `/landing/egress/whitelist/save`). Users see the rows read-only, no switches, no addresses.
- 全局·强制 seeds (4 rules, all `enabled:false`): 屏蔽发信端口 TCP 465/587/2525, 屏蔽 Telnet/SMB 23,135-139,445,
  屏蔽大陆 IP geoip:cn, 屏蔽大陆网站 geosite:cn. Seeded in the **startup migration** (not on GET: a read must not
  write, and two concurrent first GETs would race), guarded by `landing_egress_setting.global_seed_version`;
  deleting them never brings them back. 全局·默认 gets no seeds. Disabled rules never compile → plans identical.
- Preset library: table `landing_egress_preset` (name, description, rules JSON w/o ids, ask_outbound,
  insert_top, sort_order, seed_key). Rules of an ask_outbound preset use the action placeholder `ask`
  (replaced by the outbound picked at insert time). Seeded once at startup (marker
  `landing_egress_setting.preset_seed_version`) with today's 9 presets + new 「禁止访问内网」
  (`geoip:private` → block). 「恢复默认预设」 re-adds missing seed keys only.
- API: `POST /api/v1/landing/preset/list` (any logged-in user; users get only presets without 来源 IP / 来源端口
  and whose fixed outbounds are 允许用户使用); admin-only `POST /api/v1/landing/egress/preset/{save,delete,
  reorder,restore}` and `/landing/egress/builtin/save`.
- Dropdown: `components/ui/dropdown-menu.tsx` content + sub-content get `collisionPadding=8`,
  `max-h-[var(--radix-dropdown-menu-content-available-height)]`, `overflow-y-auto overscroll-contain`
  (Radix dropdown-menu 2.2.1 exposes the variable).

## Checklist

- [x] Plan doc + baseline backend suite on the unmodified base (failure list)
- [x] Backend: builtin switches (model columns, compile filter, dedupe only against emitted builtins, server
      view state, global view `builtin` section, node-rules view `builtin` section, save endpoint + re-sync)
- [x] Backend: 全局·强制 seed migration (marker, idempotent, never re-seeds after delete)
- [x] Backend: preset model/table + seed migration + repo CRUD/reorder/restore + validation + user filtering
- [x] Backend: routes (list for users, writes admin-only) + handler tests (full router, real non-admin JWT,
      403 on writes)
- [x] Backend: sync-scope tests (§3: 内置 + 强制 on native / shared / line-chain copies; 默认 never on chain)
- [x] Backend tests: unit + SQLite/PostgreSQL contract + `-race` on new code; full suite vs base
- [x] Plan identity gate on a read-only prod copy (base vs branch incl. seeding) + compile timing
- [x] Frontend: 内置 list (global page, profile editor, node-rules read-only), switches, whitelist editor
- [x] Frontend: preset library hook, dropdowns read the library, covered-by-builtin hint, 「管理预设…」 modal
      (list / edit / delete confirm / reorder / new / restore)
- [x] Frontend: rule editor 「同时保存为预设」 + 「插入时再选出站」 (admin contexts only)
- [x] Frontend: dropdown-menu collision/max-height/scroll fix; check bridge Select listbox
- [x] Frontend: tsc / build / lint (no new errors), bundle size vs 5 MiB
- [x] Playwright: dropdown placement at 390×844 + 1440×900 (top / middle / bottom, inner scroll), existing
      dropdowns before/after
- [x] Screenshots desktop + H5, light + dark; review; publish gallery on :8791
- [x] Push branch, CI green; final.md + summary.md

## Notes / results

- Baseline (fork.76 base, `go test -p 1 ./...`): 19 FAIL lines = the known 15 failing tests + 4 backup-export
  subtests (same set as plan 136). Run dir `base.fails`.
- Plan identity (prod DB read-only online backup → two copies; base tree vs branch, branch opens the copy and
  runs the seed migration first): servers 1–5 (native 1/5, shared 2/3/4) plan + mmwx desired fingerprints
  byte-identical, 3 runs; branch copy shows global.forced = 4 (all disabled), presets +10.
- Compile time per server (data load + compile, median of 25 × 3 runs): before 402–554 µs, after 343–568 µs
  (noise; no extra query in the compile path — the switches come from the same settings row read).
- Backend suite on the branch (`go test -p 1 ./...`): 15 FAIL lines, all in the base list, 0 new. The base copy
  shows 4 more (installer mirror / image cleanup / chain fixture / explicit-version installer): artefacts of the
  go-backend-only base copy (those tests read files outside go-backend); they pass in the real worktree.
- New tests: landingegress (switches, duplicate-follows-switch, seeds disabled + identical plans, seed presets
  normalized, preset validation, user filtering, sync scope native / chain-only / shared), repo (seed once +
  markers, delete → no re-seed, upgrade with existing rules, CRUD / reorder / restore, SQLite + PostgreSQL),
  handler (switch → one debounced push, plan content, server/node views), contract (full router, real non-admin
  JWT: list filtered, 401 anonymous, 403 on every write; SQLite + PostgreSQL). `-race` clean on all of them.
- Node list query-count guard: the switches are read lazily (only views that show them), the node list keeps 7/8
  queries.
- Frontend: tsc 0 errors; eslint on the changed files: no new warnings vs base (0 errors); build OK. Bundle total
  +13.9 KB (index.js 2,960,631 B, 2.18 MiB under the 5 MiB PWA limit; precache 3120 → 3134 KiB).
- Playwright (headless shell, 650 MiB scope): 89/89 checks after, 16/16 before; dropdown at top / middle / bottom
  on 390×844 and 1440×900 always inside the viewport (top → down, bottom → up, inner touch / wheel scroll, page
  does not move); before: H5 middle menu top = -174 px (off screen). Existing menus (user menu 128×74, rule-row
  menu 128×170) identical before/after. Bridge multi-select listbox (适用落地节点) lives in the dialog's scroll
  area and is fully reachable → not the same problem, unchanged.
- Gallery: 119 shots on :8791 `<token>/landing-rule-presets/`.

## Decided differently / notes

- Seeds run in the startup migration, not on GET (reads never write; no race between two first GETs).
- 「禁止访问内网」 preset counts as covered by the safety rule (UI hint only; compile does not drop it — plans
  stay as they are).
- Line-chain copies get 全局·强制 only from agents with landing_guard ≥ 2 (fork.75 agent code, not OTA'd yet) and
  the default anti-abuse rules only from landing_guard ≥ 1: older agents order chain rules first, so the panel
  cannot fix this alone; the UI says so. Safety rule: enforced by the agent / mmwx Core on every inbound.
- Gallery lives under the preview token dir like every other gallery (`/root/mmwx-preview/<token>/…`).


## Release (fork.78, panel only, no OTA)

User approval 2026-10-23 ~20:50 TW after the gallery: 「可以发布吧」 — released as built, no new scope.

- [x] Branch `maintenance/3.0.27-fork.78-landing-rule-presets` from fork.77 (1c031590); the 7 plan-140 commits
      cherry-picked without conflicts. `git diff <140 branch> HEAD` == `git diff fork.76 fork.77` (fork.77 is
      agent-only, so go-backend/ and vite-frontend/ are byte-identical to the approved 140 branch).
- [x] `go build ./...`; full backend suite: 15 FAIL lines = the same set as plan 140 / fork.76, 0 new.
      Landing / preset / seed tests on SQLite + PostgreSQL (postgres subtests ran, not skipped): all pass.
- [x] Plan identity on a fresh read-only prod copy (fork.77 tree vs this branch, branch runs the startup
      seeding): byte-identical output (sha256 6fc80419…, same as at build time); branch copy: 全局·强制 4 rules
      (0 enabled), 10 presets, builtin switches NULL, markers 1/1. Copies deleted.
- [x] Frontend: tsc 0 errors, build OK (index.js 2,960,631 B), eslint on the changed files 0 errors and no new
      warnings (6 pre-existing in server-settings.tsx).
- [ ] Push branch, CI green; tag `3.0.27-fork.78`, Images green, release validated (Latest, asset set = fork.77)
- [ ] Prod: rollback dir `pre-fork78-<ts>` + validated DB backup, upgrade `/opt/flvx-svc`, prune to 2 backups
- [ ] Prod checks: version, health, nodes, agents unchanged, migration once (+ restart, no double seed), plan
      fingerprints unchanged, new UI strings in the bundle, real-JWT checks, 10-min resource numbers
