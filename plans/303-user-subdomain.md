# 303 — 用户专属域名 (one private CNAME per user per entry node) — BUILT 11-24 TW, waiting for screenshot approval

Task `user-subdomain` (coordinator 22b5afb7, 11-24 TW). BUILD + screenshots only
(no tag, no release, no prod change). Base: `maintenance/3.0.27-fork.167-entry-domain`
(fork.167 = plan 302). Branch `feature/3.0.27-user-subdomain`, worktree `flvxx-303`.

## Goal

Every user gets their own `<random>.imgamer.top` for each entry node they use,
a CNAME → the node's DDNS main domain (DNS only, TTL 120). Same speed (only DNS
differs). The node IP changes → everybody follows (one A/AAAA change). One user
can be moved (改指 his name to another node) or dropped (his record deleted →
his clients cannot resolve → no SYN to the entry, no vmsilo source slot).
User 停用 / 到期 → record deleted, name kept (暂停); 启用 / 续期 → same name back.
User deleted / no longer on the node → record deleted, name retired forever.

## Design

- Node toggle 「用户专属域名」 in the node DDNS section (default off); needs DDNS
  on with a 主地址 record. Saved with the node (`ddns.userSubdomain`, nil =
  unchanged). Enabling is refused when the zone has `*.<zone>` (deleted names
  would still resolve).
- Main domain = the 主地址 record (AAAA and A share a name → it; one family →
  it; two different names → the AAAA one). Names are only created once that
  record is synced (exists in CF) — a CNAME to a missing name would be NXDOMAIN
  and resolvers cache that for 30 min.
- Table `user_subdomain` (one row per name, never deleted; label unique forever):
  user, node (current CNAME target), origin node, label/domain, provider/zone/
  record ids, state active | paused | retired + reason, sync status
  pending | synced | failed, error/backoff, unused_since, blocked (admin 删除).
- Desired state (pure function, unit-tested), per non-retired name:
  user gone → retired(user_deleted); target node gone → retired(node_deleted);
  node toggle off / no synced main domain → paused(node); user has no forward
  port / native landing on the target node → paused(unused), retired(unused)
  after 24 h; user expired → paused(expired); user status ≠ 1 → paused(disabled);
  else active. New names: every non-admin, active user with a forward port or a
  native landing on an enabled node and no live/paused name targeting it (and
  no admin-deleted tombstone for that pair). Refused near the CF record quota
  (reserve 5 records for admin DDNS) → user keeps the main domain, node ⚠.
- CF: CNAME `name → main domain`, proxied false, TTL 120, comment `flvx-user`.
  Delete only by stored record id after a GET confirms name + `flvx-user`
  comment; records the DB does not know are never touched. Zone listing
  (1 call/zone) every 10 min only while some node has the toggle on: drift
  repair, wildcard check, record count; quota via `GET /zones/{id}/dns_records/usage`
  (fallback cap 200, marked 估计).
- A name is handed out (sub / 复制链接 / 转发 地址) only while active + synced.
  Entry hosts equal to the node's main domain (or its current IP) become the
  owner's name; key / detail of subscription items keep the main domain
  (stored items stay valid). Plan 302: a name on an IPv6-only main domain is
  IPv6-only too → NekoBox / Surge / Base64 / unknown clients keep the literal,
  mihomo / Stash / sing-box get the name.
- Reconcile: debounced kick (5 s) after admin/user mutations + expiry job, plus
  every 10 min; no work and no CF call when no node has the toggle on.
- Admin per name: 改指 (CNAME → another enabled node where the user has a
  forward), 换新名 (old retired, new label), 删除 (retired + no auto re-create
  for that user/node until 重新分配). Sync failure / quota / wildcard → ⚠.
- UI (add-only): DDNS section toggle; node card pill 「专属域名 N」 (⚠ variant);
  drawer 「专属域名 · 节点」 grouped by user with tree line; admin user edit
  section 「专属域名」; DNS 服务商 card line 「记录 37 / 200 · 专属域名 25」.
  Normal users see no new UI.

## Checklist

- [x] Model + migration (`user_subdomain`, `node.user_subdomain_enabled`)
- [x] CF client: CNAME create/update, delete, zone record list (paged), usage
- [x] Desired-state planner (pure) + unit tests
- [x] Reconcile engine (kick + 10 min loop, CF apply, backoff, ⚠ state)
- [x] Node save: toggle + wildcard / main-domain validation
- [x] Hand-out: subs, 复制链接 (landing / chain / external), forward list + package inIp, landing 直连
- [x] Admin API: list (node / user), 改指, 换新名, 删除, 重新分配, 立即同步
- [x] Node list view + provider list stats
- [x] Frontend: DDNS toggle, node pill, drawer, user edit section, provider card line
- [x] Contract tests (SQLite + PG) with fake CF API
- [x] go test ./... base vs branch, npm build
- [x] Lab (fake CF) + screenshots → gallery :8791/user-subdomain/
- [x] One real CF end-to-end check (`flvxtest-` name, deleted afterwards)
- [x] Ledger row 待发布 + summary
- [ ] Release (after the user approves the screenshots) + turn the toggle on for 沪日首发 / 沪美

## Results (11-24 TW)

- Commits on `feature/3.0.27-user-subdomain` (base fork.167 46847063): cc6ff2ba backend,
  c82d0fdc UI, 2cbed542 count/dialog fixes, e5fc9343 opt-in real-CF e2e test, + this doc.
- go test ./... : base fork.167 29/29 packages ok (SQLite); branch 30/30 ok with
  PostgreSQL contract tests on (throwaway postgres:18), 0 failing tests.
  New: internal/usersub (planner), tests/contract TestUserSubdomainContract (SQLite + PG,
  stateful fake CF: create, 停用/启用, 到期/续期, wildcard refusal, 改指, 换新名, 删除 +
  重新分配, unused → paused → retired after 24 h, user delete, toggle off/on, quota cap,
  drift repair, foreign records never touched, labels never reused),
  handler TestSubUserSubdomain (sub output: mihomo / Stash / sing-box get the name,
  NekoBox / Hiddify / Base64 / Surge / unknown byte-identical literal on an IPv6-only
  main domain; dual-stack → name for all; keys unchanged; paused → main domain).
- npm run build ok (tsc clean, eslint clean on the new files).
- Lab (/root/us303-lab: netns loopback only, fake CF with a 37-record zone, synthetic
  users named like prod): 33/33 checks; gallery http://104.145.236.29:8791/user-subdomain/.
- Real Cloudflare (one check): zone imgamer.top 39 records, quota 200 via
  /dns_records/usage, no wildcard; flvxtest-1124001436.imgamer.top CNAME created
  (DNS only, TTL 120, comment flvx-user), re-pointed, deleted; DeleteUserRecord on
  vmsilo-huri.imgamer.top refused (record untouched); zone back to 39.

## Known limits / decisions to confirm

- IPv6-only entries (沪日首发 / 沪美): a name on an AAAA-only main domain is AAAA-only,
  so plan 302's split applies — NekoBox (default IPv6 路由 off), Surge, Base64 apps and
  unknown clients keep the IPv6 literal in their subscription. Deleting their name does
  not stop those clients; only mihomo / Stash / sing-box subscriptions and hand-typed
  names benefit. Both current sub users there (test-ns-fx, liuchen) use NekoBox.
- 暂停 deletes the record: on 续期 / 启用 resolvers that cached NXDOMAIN (SOA min 1800 s)
  may need up to 30 min before the name works again for that user.
- Unused → paused at once, retired after 24 h (a deleted + re-created forward or a
  group swap keeps the same name). Toggle off → paused (names kept, restored on).
- Admins get no names. 删除 blocks auto re-create for that user/node until 重新分配.
- 5 records reserved below the CF quota for admin DDNS records.
