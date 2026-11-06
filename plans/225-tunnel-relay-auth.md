# 225 — Tunnel relay auth (P0) + chain P1/P2 + Bestvm US OTA

Branch: `feature/3.0.27-tunnel-relay-auth` (from prod tag `3.0.27-fork.119`) → release branch
`maintenance/3.0.27-fork.120-tunnel-relay-auth`, tag `3.0.27-fork.120`.
Run dir: `/root/flvx-workers/runs/tunnel-relay-auth/`.
Approval: user 2026-11-06 ~14:30 TW 「其他的一些小问题还有你说的一些比较严重的问题，也赶紧修了吧」 (P0 + P1-1 + P1-2 + 8 P2
from `runs/chain-live-test/final.md`, Bestvm US OTA, Breadcloud US offline check). Cadence waived for this P0.

## Findings (before any change)
- Every type-2 tunnel's middle/exit relay service `{tid}_tls` has no auth: open proxy, reaches exit loopback
  (Core gRPC API on 127.0.0.1:<random>, other loopback services), `unix`/`serial` networks, bypasses entry billing.
- gost relay auth exists in every live agent (fork.95/103/104/116/117): connector `auth` sends `UserAuthFeature`;
  handler `auth` → authenticator. A handler WITHOUT an auther ignores the feature → connectors can send auth first.
- Agents: chain update = atomic swap (fork.100+, all tunnel nodes are fork.103+); service update = listener restart,
  accepted connections stay.
- Exit target restriction cannot be done without an agent change: bypasses are referenced by name only and no WS command
  registers one. → agent-side guard in fork.120 (handler metadata, inert on older agents), live on Bestvm US after OTA.
- LeiKwan `PipePing` writes an unauthenticated relay request and expects `StatusBadRequest`; an auth-enforcing landing
  answers `StatusUnauthorized` → pipe probe/diagnose would fail. Pipe tunnels enforce only when the entry agent ≥ fork.120.
- Federation (remote nodes) unused on prod (0 remote nodes); federation-built relays stay unchanged.
- Prod forward targets: 0 loopback/private/own-IP targets on relay tunnels (enum_targets.py).
- Breadcloud US + us年付 (143.20.196.27/.111, same /24) silent since 11-06 08:18:45 TW, same second; gateway .1
  unreachable from dev box / Bestvm US / Breadcloud JP; prefix still announced (AS201667) → provider side.

## Design
- Credential per tunnel: user `flvx-t<tid>`, password = HMAC-SHA256(JWT_SECRET, "flvx-tunnel-relay-v1:<tid>")[:32 hex].
  Computed when building payloads; never stored, never in API responses.
- Connectors (entry/middle chains) always send auth (no-op against old handlers).
- `tunnel.relay_auth` (0/1): handlers (middle + exit) require auth and exits get `flvx.targetGuard` only when 1 AND the
  tunnel is enforceable (no remote nodes; pipe entry agent ≥ fork.120). New tunnels start at 1.
- Admin API `POST /api/v1/tunnel/relay-auth` {tunnelIds|all, enforce}: per tunnel (locked) verify/push connectors on all
  entry+middle nodes first (diff, must succeed), then flip the flag, then push handlers. enforce=false = rollback path.
- Rollback: enforce=false for all → image swap to fork.119 → `UPDATE tunnel SET relay_auth=0`.

## Lab results (runs/tunnel-relay-auth/work/lab, 100 ms probes + one 2 MB/s long download)
- fork.103 agents (prod version): upgrade (connectors) 0 fail; enforce 1 probe (≤0.1 s), long download kept;
  clean rollback (enforce=false, then fork.119) 0 fail; dirty rollback (direct image swap) 1.6 s outage.
- fork.120 exit: enforce 0 fail; no-auth/wrong-auth → Unauthorized; right auth → loopback/Core API Forbidden, public OK;
  dirty rollback 5.0 s outage → rollback.sh switches relay auth off first.
- P1-2 cannot be panel-only (old agents reject plan rules that name a chain outbound; skipping chain inbounds would let
  屏蔽大陆网站 block google@cn on chains) → agent-side split in fork.120 (Bestvm US now, others at their next OTA).

## Tasks
- [x] Baselines (business, landing, subs, 救命版, panel CPU/RSS, API latency, agent CPU/RSS/TTFB on one rule)
- [x] P0 panel: credential, connector auth, handler auth gated by relay_auth, enforce API, new tunnels = 1
- [x] P0 agent: relay target guard (metadata) + PipePing accepts Unauthorized; unit tests
- [x] P0 lab: auth connector ↔ no-auth handler; order with live downloads; rollback order; interruption measured
- [x] P1-1 chain port pick: loopback port free on all exits, independent of public ranges
- [x] P1-2 forced 放行谷歌/苹果 on chain inbounds → chain upstream
- [x] P2-1 诊断: non-admins see node names only
- [x] P2-2 reject panel host IPs in chain upstream validation
- [x] P2-3 hide chain-only landing servers in admin grant list
- [x] P2-4 phone edit dialog 「当前节点」 truncation
- [x] P2-5 「首次使用需要安装落地程序」 only when an install happens
- [x] P2-6 Core idle on chain-only server with 0 chain nodes — NOT doable panel-side (no agent command stops Core; enabled:false keeps it running for final counters) → 还没解决
- [x] P2-7 Core CPU cap on chain-only servers
- [x] P2-8 pause removes chain inbound, resume recreates
- [x] Contract/unit tests (relay auth config, credential absent from APIs, P1-1, P1-2, pause)
- [x] Gates: go build/test, SQLite + PostgreSQL contract, tsc/build/lint, screens P2-1/4/5
- [x] Ledger check (226), release fork.120, CI + images + assets
- [x] Prod: backup, deploy, canary test tunnel, groups with checks, interruption per rule
- [x] Bestvm US OTA + scan guard check
- [x] Attack re-test (no-auth relay, Core API via relay, billing bypass)
- [x] Ledger 已发布, prune backups, final.md + summary.md

## Result (2026-11-06)
- Released `3.0.27-fork.120` (tag → 3c774fe1; test-only follow-up d466d939 on this branch). CI 42185182618 + Images
  42185183338 green; release assets = fork.119 set, agent `gost 3.0.27-fork.120`.
- Gates: go build ok; go vet same as fork.119 (leikwan testapi unkeyed fields); backend suite (SQLite + PostgreSQL)
  24 packages ok, 1 failure = the old diagnosis test asserting hop IPs for a normal user (the P2-1 change) → test fixed
  in d466d939 and green; agent landing/socket/relay tests ok; tsc 0; eslint 16 errors = fork.119 baseline.
- Prod: backup `/opt/flvx-svc/rollback/pre-fork120-20261106T085032Z` (rollback.sh switches relay auth off first),
  deploy 16:56 TW, canary + 6 groups 17:01–17:17 TW: 37/38 relay tunnels enforced, tunnel 80 (利群管道) waits for
  火山广州 ≥ fork.120. Measured interruption ≤ 0.25 s per tunnel (1–2 failed 250 ms probes at the switch).
- Bestvm US OTA fork.103 → fork.120 17:18 TW (back in 5 s), Core upgrade confirmed (no nodes on it), ⚠ gone;
  禁止端口扫描 has nothing to guard there (chain-only, 0 landing nodes).
- Attack re-test: no-auth / wrong credential → Unauthorized on every probed exit/middle; Core API via relay refused
  (Unauthorized; with the credential Forbidden on Bestvm US).
- P2-4 follow-up (8486afe5, frontend only) built + verified, rides the next batch (ledger 225b).
- Not done: P2-6 (needs an agent change), PipePing-gated tunnel 80, fleet OTA for the target guard / P1-2.
