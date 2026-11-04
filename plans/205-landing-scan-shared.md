# 205 — 「禁止端口扫描」 on mmwx-shared landing servers + no delete-and-recreate — BUILT (build only)

Base: tag `3.0.27-fork.112` · branch `feature/3.0.27-landing-scan-shared` · build only (release = release-1104b worker).
Brief: `/root/flvx-workers/runs/landing-scan-shared/brief.md` (user 2026-11-04 ~15:10 TW: 「该发布发布该修复的修复」).
Contract: `/root/flvx-workers/contracts/landing-scan-guard-v1.md` §9 (mmwx Custom v1.4.84/v1.4.85, Helper v0.7.18/v0.7.19,
FLVX shared Core f6f5844; capability present on all four shares as of 11-04 16:25 TW).

## Design
1. Shared servers reuse the native path (plan 202):
   - capability: share list `scan_guard.versions` contains 1 (stored in `shared_caps` like the other caps);
   - desired: `scan_guard` = the same object as native (`enabled, identities, thresholds, seen`), built by the same
     helper; identities = `flvxl-<id>` of every node in that desired (all protocols — contract §9.2: the Core counts any
     listed tag); joins the desired content hash (any change, incl. `seen`, moves the revision);
   - status: `scan_guard {state, hits, blocked}` from the existing once-a-minute status poll (no extra request) →
     `RecordLandingScanStatus` (same event table, node disabled, seen mark, admin notice / TG, user message);
   - 解除禁用: same endpoint; node `enabled:true` + revision bump → Helper sends `scan_unblock`;
   - row state: 生效中 / 全局已关闭 / 共享 Core 需升级 / 检测异常 / 等待同步; share without the capability keeps
     「共享服务器暂不支持」 and gets nothing new (payload + hash byte-identical).
2. Recreate gap: a user with an open scan block (a scan-disabled node, or a blocked event whose node was deleted, not yet
   lifted by an admin) cannot create a new landing node (native or shared, incl. the rule page's auto-create); message
   「…因检测到端口扫描（原因）已被禁用，暂不能新建落地节点，如有疑问请联系管理员」. Deleting the node keeps the event.
   Admins are not limited (own nodes or creating for a user). Admin lifts a deleted node's block from the event list
   (「解除禁用」 on the row). Open block events are never pruned by retention.

## Checklist
- [x] Read contract §8/§9 + mmwx final.md, plans 202/204, native code paths
- [x] landingcustom: share capability, desired `scan_guard`, status `scan_guard` (never fails the poll)
- [x] repo: shared desired + hash, RecordLandingScanStatus for shared, user block check in create, event unblock,
      retention keeps open blocks, counts
- [x] handler: shared status → record path, row state for shared, event unblock endpoint, settings log
- [x] UI: per-share state chips, event row 「解除禁用」 for deleted nodes, texts that said 仅自有落地
- [x] Tests (fail on fork.112 where applicable): shared enable / ingest / unblock / old Custom no-op / malformed report;
      recreate refused (native + shared + rule auto-create), admin unaffected, admin unblock lets them create again;
      SQLite + PG
- [x] Gates: backend build/vet/test SQLite + PG, go-gost/x tests, frontend tsc/build/lint vs base
- [x] Release notes + summary in `/root/flvx-workers/runs/landing-scan-shared/`; push; CI green (41816746571 on a7e7c03a)

## Results
- Commits: 5b5aa1cb (feature), a7e7c03a (行为检测 header text). Panel only: `go-gost/` untouched → no OTA; no DB schema change.
- New tests: handler `TestSharedLandingScanGuard` (SQLite + PG), `TestLandingScanRecreateRefused`; contract
  `TestLandingScanSharedAndUserBlockContract` (SQLite + PG); landingcustom `TestStatusScanGuardReport`,
  `TestShareScanGuardCapability`. A probe using only fork.112 APIs fails on fork.112 (4 assertions: create while blocked,
  create after delete, no scan_guard to an advertising share, relayed hit ignored) and passes on the branch.
- Gates: go build ok; go vet only the pre-existing leikwan/testapi unkeyed-field notes (file untouched); go test ./...
  SQLite + PostgreSQL 25/25 packages ok; go-gost/x 20/20 ok (needs a short TMPDIR: unix socket paths > 108 bytes fail
  `bind: invalid argument` in landing tests), go-gost ok; frontend tsc 0, build ok, eslint 94 errors / 3793 warnings =
  fork.112 per file.
- UI check (synthetic DB, loopback-only netns, `runs/landing-scan-shared/screens/`): 17 screens, 33 checks, 0 failed,
  0 page errors (admin 内置 chips per share, event list 解除禁用 incl. deleted node, restricted user cards, after lift).
- Resources: no new Custom request (status stays one GET per share per 60 s; test asserts no read within the minute);
  desired +~200 B per capable share; `SharedLandingDesired` (already every 5 s) reads one settings row more only for
  capable shares; create / user server list +2 queries, 0.33 ms / 0.35 ms at the 5000-event retention cap (SQLite).
- Prod (read-only 11-04): guard ON (defaults); shares Boil Hinet 马年 2 nodes, PQS TW 1, Breadcloud US 年付 0, Dmit T1 0;
  0 scan-blocked nodes, 3 events all lifted → no user is restricted by this release.
