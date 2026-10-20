# 113 — Rule landing target: one place creates nodes (mmwx-style)

Branch `feature/3.0.27-landing-target` (from plan 108 head 40e42ed8). No release until the user approves screenshots;
ships together with 108.

## Why
Today a landing node can be created in two places: on the landing page, and implicitly in the rule dialog via
「落地服务器（自动建节点）」. The rule path ALWAYS creates a new node (never reuses), the node becomes a normal
「我的节点」 entry that counts against the node cap and survives rule deletion, and editing a rule creates yet another one.
On 线路机 tunnels nothing is auto-detected: an SS node or SS protocol is just refused.

mmwx (miaomiaowuX) keeps one object — the node. A forward chain either binds an existing node (any protocol, creates an
entry-variant link) or creates a chain-owned VLESS node; users never get nodes created behind their back.

## Target model
- Landing nodes are created in ONE place (the create-node dialog), reachable from the landing page and from the rule dialog.
- A rule only REFERENCES a landing node (except 108 「我自己的节点」 chain nodes, which are rule-owned implementation details).
- One node may be used directly and by several rules; billing already counts the landing once.

## Checklist
- [x] Rule dialog: remove 「落地服务器（自动建节点）」; 目标类型 = 手动填写 / 我的落地节点 / (线路机) 我自己的节点
- [x] 我的落地节点 picker grouped by server; incompatible nodes shown disabled with reason (线路机 → Reality only, stopped,
      server unavailable); empty state
- [x] 「＋ 新建落地节点」 in the picker opens the shared create-node dialog (owner = rule owner, protocol locked on 线路机),
      auto-selects the new node
- [x] Reuse hint in the create dialog when the user already has a compatible node on that server (「使用它」 / 「仍然新建」)
- [x] Backend `landingServerId` = reuse-or-create (API compat for stale frontends), per-user single-flight, never another
      user's or a chain-internal node
- [x] Landing node lists show 「规则 N」 (one grouped query); delete refusal names the referencing rules
- [x] Rule rows targeting a landing node offer 「复制链接」 (entry-variant link) — verify existing, add if missing
- [ ] Tests (handler + repo + acceptance runner extension), full go test vs baseline, frontend build/lint
- [ ] Resource numbers, screenshot gallery /landing-target/, Chinese summary

## Round 2 evidence (Taiwan time, UTC+8)

- 2026-10-20 17:28: plan commit `6d94adac` pushed with `-u origin`; CI [39400967038](https://github.com/ImoLR/FLVXR2/actions/runs/39400967038) passed.
- Clean detached baseline at `6d94adac`, under the run directory `tmp/baseline`: `GOMAXPROCS=2 go test -count=1 -p 1 ./...` completed with the same 15 historical failures/subtests. Handler failures: `TestBuildFederationServiceConfig_NonTLSProtocol_NoNodelay`, `TestRunNodeRenewalCycleJob_AdvancesOverdueAnchorTimes`. Evidence: `backend-baseline.log` / `backend-baseline-failures.json`. No stash used.
- `e6c976b6`: local acceptance runner T1 added; Python AST and diff checks passed. Runtime lab evidence pending.
- `53af2286`: shared create dialog, grouped owner picker, disabled reasons, reuse hint and landing-rule copy-link action implemented. Build, lint and screenshot evidence pending. Node chip/delete-refusal UI implemented; backend evidence pending.
- All Round 2 logs and gallery source: `/root/flvx-workers/runs/landing-target-r2/`. No release, tag, production DB access or agent modification.

- `cd3bfe67`: backend targeted tests passed (`backend-targeted.log`): lookup isolation/oldest-active reuse/caps/protocol change/concurrent submits/create-update rollback/Reality constraint/immutable port-protocol/delete names/per-IP links. PostgreSQL optional cases skipped without a test DSN.
- Confirmed clean baseline list queries (Query + Row callbacks, `queries-before-confirmed.log`): native 1/6 nodes = 5/15; shared 1/6 = 7/27. After: native 7/7, shared 8/8, admin and user. `forwardCount` uses exactly one grouped query. Reuse lookup has a composite index; no data migration.
- Frontend `npm run build` passed; `eslint-comparison.json`: historical errors 96 → 96, new errors 0. Three small changed components: 0 errors / warnings. Independent copy of existing dependencies with an identical package.json used, no dependency upgrade.
- Clean baseline worktree removed after preserving logs; only its temporary probe and dependency symlink were deleted.
