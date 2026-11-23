# 303 — 用户专属域名 (one private CNAME per user per entry node)

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

- [ ] Model + migration (`user_subdomain`, `node.user_subdomain_enabled`)
- [ ] CF client: CNAME create/update, delete, zone record list (paged), usage
- [ ] Desired-state planner (pure) + unit tests
- [ ] Reconcile engine (kick + 10 min loop, CF apply, backoff, ⚠ state)
- [ ] Node save: toggle + wildcard / main-domain validation
- [ ] Hand-out: subs, 复制链接 (landing / chain / external), forward list + package inIp, landing 直连
- [ ] Admin API: list (node / user), 改指, 换新名, 删除, 重新分配, 立即同步
- [ ] Node list view + provider list stats
- [ ] Frontend: DDNS toggle, node pill, drawer, user edit section, provider card line
- [ ] Contract tests (SQLite + PG) with fake CF API
- [ ] go test ./... base vs branch, npm build
- [ ] Lab (fake CF) + screenshots → gallery :8791/user-subdomain/
- [ ] One real CF end-to-end check (`flvxtest-` name, deleted afterwards)
- [ ] Ledger row 待发布 + summary
