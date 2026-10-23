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
- [ ] Backend tests: unit + SQLite/PostgreSQL contract + `-race` on new code; full suite vs base
- [x] Plan identity gate on a read-only prod copy (base vs branch incl. seeding) + compile timing
- [ ] Frontend: 内置 list (global page, profile editor, node-rules read-only), switches, whitelist editor
- [ ] Frontend: preset library hook, dropdowns read the library, covered-by-builtin hint, 「管理预设…」 modal
      (list / edit / delete confirm / reorder / new / restore)
- [ ] Frontend: rule editor 「同时保存为预设」 + 「插入时再选出站」 (admin contexts only)
- [ ] Frontend: dropdown-menu collision/max-height/scroll fix; check bridge Select listbox
- [ ] Frontend: tsc / build / lint (no new errors), bundle size vs 5 MiB
- [ ] Playwright: dropdown placement at 390×844 + 1440×900 (top / middle / bottom, inner scroll), existing
      dropdowns before/after
- [ ] Screenshots desktop + H5, light + dark; review; publish gallery on :8791
- [ ] Push branch, CI green; final.md + summary.md

## Notes / results

- Baseline (fork.76 base, `go test -p 1 ./...`): 19 FAIL lines = the known 15 failing tests + 4 backup-export
  subtests (same set as plan 136). Run dir `base.fails`.
- Plan identity (prod DB read-only online backup → two copies; base tree vs branch, branch opens the copy and
  runs the seed migration first): servers 1–5 (native 1/5, shared 2/3/4) plan + mmwx desired fingerprints
  byte-identical, 3 runs; branch copy shows global.forced = 4 (all disabled), presets +10.
- Compile time per server (data load + compile, median of 25 × 3 runs): before 402–554 µs, after 343–568 µs
  (noise; no extra query in the compile path — the switches come from the same settings row read).
