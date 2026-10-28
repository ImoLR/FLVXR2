# 158 — limits-server-conns: per-server 入站连接数 + user totals incl. landing (BUILD ONLY)

Design: `/root/flvx-workers/contracts/limits-unify-decisions.md` §2 (user decisions 2026-10-28, binding).
Branch `feature/3.0.27-limits-conns` from `origin/feature/3.0.27-limits-pools` (plan 157, b4776edb on fork.94).
Build only: no tag, no release, no prod writes, no OTA. Screens go to the user before a later batch release.
Contract for the mmwx side: `/root/flvx-workers/contracts/landing-user-limits-v1.md` (supersedes landing-conn-mode-v1 §6).

## What the pinned Core can do (verified in Core 2bf03c9a, `common/mmwxcustom/connection/tracker.go`)

- Per port: `max_total_connections` (in+out sockets), `max_inbound_connections`, `max_inbound_online_ips`.
- **Management groups**: `management_mappings` (identity inbound_tag+user → group, an identity in at most one group)
  + `management_limits` (group → `max_inbound_connections`, `max_inbound_online_ips`, …). Admission checks the group
  and the port; the snapshot reports per group `inbound_current` and `inbound_online_ips` (every active or
  30 s-grace source with its active lease count). → one user's several nodes on one server CAN share one limit.
- Limits are "positive = limit": 0 / null = unlimited, so "full" (budget 0) cannot be expressed as a number;
  `blocked_identities` rejects new requests (and closes the identity's sockets when newly blocked).
- No way to exclude sources (our relay IPs) from a group's counting. A Core change would be needed for that
  (mmwx Core release + agent re-pin) → not done here, see 「需要用户决定」.

## Design (as built)

### Data (add-only, SQLite + PostgreSQL, rollback to fork.94 ignores it)
- `user_server_quota.max_inbound_connections` INT NOT NULL DEFAULT 0 (0 = 不限) — per user per server 入站连接数,
  same row as the traffic quota (key `node:<node id>` = FLVX node incl. native landing server, `landing:<id>` = shared).
- `node.landing_user_limits` INT NOT NULL DEFAULT 0 — agent capability (landing Core user groups v1).
- Shared capability `user_limits {versions:[1]}` stored in the existing `landing_server.shared_caps` blob.

### Counting
- 用户总连接数 = inbound only: forward entries (agent quota group `user-<id>`, as today) + landing inbound on capable
  landing servers (native: agent Core group `landing-user-<id>`; shared: Custom report). Landing inbound whose source
  is one of our FLVX node IPs (relay of a forward rule) is NOT counted again (connections and IPs) — relay dedupe.
- 用户总 IP 数 = distinct client IPs across entries + landing direct IPs (relay IPs excluded).
- Per-server 入站连接数 (row): entry server = the user's forward inbound connections entering there; landing server
  = the user's inbound connections on that server's Core **as the server receives them (relayed ones included)**,
  because the Core cannot tell relays apart at admission (see 「需要用户决定」 #1). A node that is both entry and
  native landing has one row covering both.

### Coordinator (`handler/quota_groups.go`, extended, same 1 s push cadence)
- Slots per limited user: forward slot per entry node, native landing slot per capable landing node, shared slot per
  capable share. A user is limited when 总连接数/总 IP 数 > 0 or any row has 入站连接数 > 0.
- Budgets (per slot s on server key k): total `B_t = T − (U − u_s)` (deduped usage), server `B_s = C_k − (W_k − w_s)`
  (server-scope usage), IPs `B_ip = Tip − |IPs elsewhere not active in s|`; −1 = unlimited, floored at 0.
  Forward slots get `min(B_t, B_s)` as today's single number. Landing slots get `maxConnections = B_t`
  (agent adds its live relayed count), `serverMaxConnections = B_s`, `maxClientIps = B_ip` and `relayIps` = number of
  our nodes that dial this user's nodes on that server (linked rules), so relay sources always keep an IP slot.
- Agent Core group limit: `L = min(B_t + relay_live, B_s)`; IPs `L_ip = B_ip + max(relay IPs seen, relayIps)`;
  L = 0 with nothing open → identity blocked (exact "full"); otherwise ≥ 1.
- Live usage (admin user list 总连接 / IP) includes landing direct usage.

### Agent (`go-gost/x/landing`, capability `landing_user_limits = 1`)
- Desired (only to capable agents; older agents get byte-identical payloads): per node `quota_group`, top-level
  `user_limits {relay_sources, groups[{group, max_connections, max_client_ips, server_max_connections}]}` (static
  upper bounds used until the first dynamic budget). Joins the revision signature.
- `SetQuotaGroups`: budgets named `landing-user-*` go to the landing manager (authoritative list per push), others
  to the service registry as before. Applied with one `PUT /v1/config` on the control socket (no CLI, no restart),
  coalesced, using the last snapshot taken by the existing 5 s collect — no new poller.
- Report: `QuotaGroups` gains `landing-user-*` entries (direct connections, direct active IPs, relayConnections).

### Shared (contract landing-user-limits-v1)
- Capability → per-node limits sent as `conn_mode: "inbound"`, budgets exchanged via `PUT /shares/{id}/user-limits`
  (response = latest per-user usage), at most every 5 s on change and every 10 s otherwise, only while needed.
- No capability → FLVX sends nothing new, shared usage is not counted (today's behaviour).

### Plan-141 selector retired
- Server total = always 入站+出站 (Core global total). A native server whose total was stored 仅入站 keeps it until
  the admin saves the server again (notice in the dialog), so nothing tightens silently.
- Every per-node limit = 入站 (native agents with landing_conn_mode ≥ 1; shared only with user_limits v1, else
  入站+出站 as before). Stored numbers are kept; nodes that were 入站+出站 now count 入站 (looser, never stricter).
- UI: no 计算方式 selectors; labels say (入站) / (入站+出站); 服务器总连接数 shows the machine cap (整机保护上限) next to
  it and warns when the total exceeds it.

## Checklist

- [x] Plan doc
- [x] Model + AutoMigrate: `user_server_quota.max_inbound_connections`, `node.landing_user_limits`
- [x] Save/get per-server 入站连接数 (validation, upsert, user dialog payload, 入口/落地 rows only in UI)
- [x] Forward services attach the user quota group when a per-server cap exists on that entry; resync on toggle
- [x] Coordinator: slots, per-server caps, landing budgets, relay reserve, live usage incl. landing; tests
- [x] Agent: landing Core management groups, budget apply path, usage report, capability; tests (unit + netns)
- [x] Panel → native agent desired: quota_group / user_limits gated by capability, revision signature
- [x] Shared: contract `landing-user-limits-v1.md`; client types, capability, desired conn_mode, exchange, usage;
      fake-Custom contract test (SQLite + PostgreSQL); today's behaviour without capability
- [x] Retire plan-141 selector (backend semantics + migration notes), machine cap hint data
- [x] Frontend: row field, server settings labels/hint/warning, node labels, user total hint; tsc/build/lint
- [x] Full `go test ./...` SQLite + PostgreSQL vs base; go-gost / go-gost/x tests
- [x] End-to-end on a local panel + real agent + real Core (netns): per-server cap, total incl. landing, relay dedupe
- [x] Resource numbers (backend RSS/CPU, agent RSS/CPU before/after)
- [x] Screens 390 + 1440, light + dark → `/root/mmwx-preview/limits-server-conns/`
- [x] Push branch, CI green
- [x] Summary → `/root/flvx-workers/runs/limits-server-conns/summary.md`

## Results

- **Panel + two real agents + pinned Core e2e** (netns, offline, synthetic data; `runs/limits-server-conns/lab/e2e.py`):
  alice's rule on entry A relays into her landing node on B; she also connects to B directly.
  P1 总连接数 3 → 2 relayed + 1 of 3 direct accepted, admin view 3 connections / 1 IP (relays counted once);
  P2 landing server 入站连接数 3 → 2 relayed + 1 of 2 direct (the Core counts relays in server scope);
  P3 entry server 入站连接数 1 → 1 of 2 via the rule, 2 of 2 direct; P4 总 IP 数 1 → direct .9 ok, direct .10 refused,
  the rule from .9 ok (shared IP), admin view 1 IP; P5 (recorded) rule first then direct from the same IP → refused
  at the landing (Core cannot be told which IPs are already counted; see summary).
- **Agent + Core netns test** (`user_limits_netns_test.go`): IP reserve keeps the relay's slot; 4 relays + exactly 2 of 4
  direct over two nodes of one user with budget 2; server cap 7 with 6 open → 1 of 2; another user's node 3/3; full
  idle group blocked 0/1, budget 1 → 1/2.
- **Resources** (fresh start, same activity: limits on, 2 relayed + 2 direct held, 60 s): fork.94 → branch: panel
  0.40 → 0.45 % CPU, 34.2 → 34.9 MiB; entry agent 0.27 → 0.22 %, 49.2 → 49.1 MiB; landing agent 0.27 → 0.27 %,
  49.3 → 49.8 MiB; landing Core 0.12 → 0.12 %, 65.3 → 65.8 MiB; fds equal. Coordinator pass 1.7 ms for 200 limited
  users × 4 slots (≤ 1 pass/s). No new pollers: budgets ride SetQuotaGroups, usage the 1 s metric report, landing group
  state the existing 5 s collect snapshot; shared exchange ≤ 1 PUT / 10 s per capable share.
- **Rollback**: the fork.94 panel boots on the branch-migrated DB (lab), agents online, no errors.
- **Tests**: full `go test ./...` SQLite 19/19 and PostgreSQL 19/19 (base was 19/19 too); go-gost/x `go test ./...`
  18/18 packages, go-gost 1/1; frontend tsc 0 errors, build ok, lint no new warnings. CI run 40701242871 green.
- **Screens**: `/root/mmwx-preview/limits-server-conns/` (28 shots, 56/56 checks, 0 page errors).
