# 286 — 落地页（管理员）「落地服务器」按地区分组 +「全部节点」按用户分组 + 全部节点手机版

Task `landing-admin-groups` (coordinator 23c0d9d9, 11-19 TW). Base: tag `3.0.27-fork.154`, branch
`feature/3.0.27-landing-admin-groups`, worktree `/root/projects/flvxx/flvxx-286`.
**Build + screenshot gallery only** — no merge / tag / release / prod change (user reviews the screens first).

## Goal
Admin 落地 page gets hard to scan as servers / nodes grow. Normal-user tabs (可用落地 / 我的节点) stay exactly as they are.

1. 落地服务器 (admin): automatic region groups (flag + region + 「N 台」 + problem-only summary), cards unchanged.
2. 全部节点 (admin): groups by owner (admin first, then users by name), collapsed by default, columns aligned.
3. 全部节点 on phones / H5: real list rows (h5-expand-row: dot + name + one protocol tag + ＋/−), no wide table.

## Decisions
- Region of a server (admin list only): `regionOverride` > native `node.region` > GeoIP of `publicHost`
  (embedded DB-IP via `geoip.DetectNodeAddresses`, DNS for domains) > ''.
- DNS never blocks the list: per-host in-memory cache (6 h; failed lookups retried after 10 min), stale values are
  served while a background refresh runs; a cold host waits at most 300 ms (shared deadline for the whole list), then
  shows under 未设置地区 until the background lookup lands. A changed publicHost is a new cache key (and is warmed on save).
- New column `landing_server.region_override varchar(8) not null default ''` (AutoMigrate, additive). Written only by
  `POST /api/v1/landing/server/region {id, regionOverride}` (admin, one column, no revision bump, no agent / 妙妙屋 push),
  like the tunnel 「显示地区」 (plan 184). `/landing/server/save` keeps the stored value.
- Admin server views get `region` (effective), `regionAuto` (without override), `regionOverride`; non-admin views unchanged.
- Admin node list gets `userAdmin: true` on nodes owned by an admin account (header tag 「管理员」); non-admin unchanged.
- Landing servers are not part of backup export/import → nothing to add there.
- UI groups are client-side over already loaded data (no extra requests per render). Collapse state per browser in
  localStorage (try/catch), as explicit open/closed choices on top of the defaults (servers: open, users: closed, a
  single user group: open). Jumps (⚠ 查看, 扫描 查看节点, 创建节点 → 全部节点, 设置 save) open the target group.

## Checklist
- [x] Worktree from fork.154, disk ≥ 5 GB (go-build cache entries > 12 h trimmed: 3.9 → 7.1 GB free)
- [x] Backend: model column + migration
- [x] Backend: region cache + resolution order + admin view fields
- [x] Backend: `/landing/server/region` endpoint + save keeps override
- [x] Backend: admin node view `userAdmin`
- [x] Backend tests: order, cache non-blocking (fake resolver), override save/clear/validation, non-admin no fields,
      migration SQLite + PG
- [x] Frontend: shared group header + region groups on 落地服务器
- [x] Frontend: 服务器设置 「显示地区」 select (region-only save path)
- [x] Frontend: 全部节点 user groups (desktop table, aligned columns)
- [x] Frontend: 全部节点 mobile rows (＋/−, one open)
- [x] `npm run build`, eslint (0 warnings on changed files), tsc
- [x] `go test ./... -timeout 30m` (SQLite + PostgreSQL via FLVX_POSTGRES_TEST_DSN): all packages ok after updating the
      TestLandingUserServerViewContract admin field list (the only failure, caused by the 3 new fields — not pre-existing);
      contract package re-run alone: ok (542 s). CI run 44534783016 green (frontend, backend, PG contract, agent).
- [x] Screenshot gallery `/root/mmwx-preview/landing-admin-groups/` (http://104.145.236.29:8791/landing-admin-groups/,
      22 shots, shots.cjs 33 checks 0 failures; normal-user 可用落地 / 我的节点 pixel-identical to fork.154)
- [x] Push branch, ledger row (状态: 已构建，待用户看截图), cleanup (lab data dir, test PG container, scratch, dist),
      summary /root/flvx-workers/runs/landing-admin-groups/summary.md. Worktree kept for the release task
      (vite-frontend/node_modules = symlinks to the main checkout + qrcode from runs/landing-admin-groups/npm-extra).

Status: BUILT, not merged / tagged / released; prod untouched. Release = separate task after the user approves the screens.

## Notes (build)
- Prod check (dev box DNS + embedded DB-IP, 11-19): Boil Hinet 马年 / Boil Hinet 158 / PQS TW / Tcscloud Seednet → TW,
  Boil HKT 99u → HK, **Dmit T1 (154.3.39.138) → HK** (DB-IP), NexKR → KR, Bestvm US → US, Bestvm JP B → JP. Native
  servers use node.region (KR / US / TW / JP set on prod). Dmit T1 is a US machine → the release sets its 「显示地区」
  = US via `POST /api/v1/landing/server/region {"id":4,"regionOverride":"US"}` (then TW ×4, US ×2, HK, JP, KR).
- `/landing/server/save` uses a strict decoder; `regionOverride` is `json:"-"` on the model, so it is never accepted
  there (the dialog's save helper whitelists fields and never sends it); the region select saves through
  `/landing/server/region` first, and a region-only change skips the full save (no revision bump, no push).
- Desktop 全部节点: one `table-fixed` per group with the same `<colgroup>` (52 px tree/dot column + 10 / 15 / 12 / 9 /
  13.5 / 10 % + 操作 rest), `min-w-[1080px]` (no clipped pill / header at ≥ 1440 with the sidebar open; narrower
  windows scroll sideways like the old table, which already scrolled at 1440). Rows 89–149 px (old 129 px).
- Phones / H5 (`useH5Mode`): rows reuse the rules-page phone tree (`flvx-rule-tree-cards` / `-node`, space-y-3).
- Lab: /root/flvx-workers/runs/landing-admin-groups/lab (setup.sh, seed.py synthetic DB, fetch.sh real API dump +
  region API smoke test, mock.py demo states, shots.cjs 30+ checks, trunc.cjs / measure.cjs). Data dir /root/lag286-lab.
