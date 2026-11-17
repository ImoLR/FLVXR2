# 276 — 利群 QA 修复（F01–F16）

Source: `/root/flvx-workers/runs/leikwan-qa/qa-report.md` (plan 275). Base `3.0.27-fork.144`, branch
`feature/3.0.27-leikwan-qa-fix`. Panel only (F08 needs no agent change: fork.143+ agents already honour sskeys `remove`).

## Checklist
- [x] F01 delete user cascades 利群 rules (LeiKwan forward + auto 备线 + grant; the auto 利群汇聚 sub goes with the user's subs); a LeiKwan delete failure refuses the user delete; confirm dialog count (single + batch); owner missing → block 「用户已删除」
- [x] F02 successful retarget clears BackupError; 「重试备线」 retargets an existing 备线 (auto: create a new one first, then drop the old one — an unreachable target keeps the old 备线 + error); error prefix 「备线还指向旧落地」 → admin chip, user 「备线更新中」
- [x] F03 grace ends only when the rendered sub had ≥1 of the user's 利群 nodes (`subRender.leikwan`)
- [x] F04 user page amber grace notice (desktop + H5), only while 「只开主入口」 is on
- [x] F05 refuse deleting 外部节点 / panel landing (native + shared path) used by 利群 rules, listing the rule names; `inUse` gets `kind: "leikwan"` entries; landing `forwardCount` counts 利群 rules
- [x] F06 user update / flow reset / quota reset / reset+expiry job → block recompute at once; plus a 1 min light block pass (DB reads only, push only on change)
- [x] F07 every pass syncs auto 备线 pause/resume with the block state (acts only on mismatch, log once per 30 min per failure)
- [x] F08 sskeys `remove` for keys no rule wants (panel-keyed rule with filter off, or a forward gone from the host and not unmanaged); manual-landing keys never removed; prod check: 0 removals
- [x] F09 user 诊断: 备线 row 「备线（FLVX 隧道）」, no tunnel/node names, failures without hop descriptions; exit-skip without 「妙妙屋」 wording
- [x] F10 「规则名称太长（最多 100 字节，约 33 个汉字）」, bad requestId 「请求标识无效，请关闭弹窗后重试」, input clamps to 100 UTF-8 bytes
- [x] F11 409 forward_quota_exceeded → 「利群名额已满，请联系管理员」
- [x] F12 grant status 0 → block 「授权已停用」 (+ 备线 paused by F07); link refused
- [x] F13 entry address unbroken (184 px column, nowrap); F14 换落地 = tinted blue pill
- [x] F15 plan-272 doc text; F16 sub card + 取消授权 confirm text
- [x] Tests: contract `TestLeiKwanQAFixContract` (SQLite + PostgreSQL), unit `TestLeiKwanDesiredForRemovesStaleKeys` + block reasons, all 利群 contract tests, vet, `npm run build`
- [x] Lab re-run s3/s3b/s8/s19/s22 + F06 timing (`runs/leikwan-qa-fix/lab-story-after.txt`)
- [x] Gallery /root/mmwx-preview/leikwan-qa-fix/ (18 PNGs)
- [ ] Full `go test ./...` vs base; CI green
- [ ] Release (after 274 + 273 are done): rebase, tests, tag, CI, prod backup + upgrade, verify
- [ ] Ledger + memories

## Lab before → after (fork.144 QA run → this branch)
| Repro | Before (QA) | After |
|---|---|---|
| s3/s3b 换落地 to unresolvable landing, back | error stays forever (「备线准备中」) | →9902: 「备线还指向旧落地：域名 … 解析失败」; →9901: error cleared |
| s22 重试备线 | returns ok, does nothing, error stays | ok; error cleared; unreachable target keeps the old 备线 |
| s8 pull a sub without 利群 nodes | grace ends, 阿里广州 blocks 10022/10023/10025 | grace stays, 阿里广州 open; the 利群汇聚 sub pull ends it |
| s19 delete user with a 利群 rule | rule/forward/grant/备线 stay, port open on 火山上海 | rule, grant, 备线, LeiKwan forward (404) all gone; no 「?」 grant card |
| F06 disable / expire / renew via user page | not blocked after 2.5 min (next 5 min loop) | DB ≤1 s, 4 hosts follow ≤1 s; direct DB edit ≤60 s |
| F12 grant status 0 | rules run, link works | 「授权已停用」 in 2 s, hosts in 3 s; link refused |
| F08 deleted rule key | stays on 4 hosts | removed from 百度云广州 / 阿里广州 / 火山广州 sskeys.json |

## Notes (not in scope: QA risks)
- R1 one ultra-client per 利群 rule on each of the 4 entries (25–50 MB each) — count memory before raising quota.
- R2 prod 利群 quota 7/8.
- R3 百度云广州 journalctl follower RSS 102 MB — watch.
- R4 阿里广州 nft shows ports as `0x2720 [invalid type]` — display only.
- R5 阿里广州 / 火山广州 warm overrides for 263/264/280/282 (all-entries rules) — fine.
- R6 agent WS reconnects cluster in the night and on releases — normal.
- F07 side effect: an automatic 备线 an admin pauses by hand on the 规则 page is resumed within a minute while its rule is open (auto 备线 follow their rule).
- Resource: the 1 min pass = 4–5 small DB reads (rules, users, quotas, grants, 备线 forwards); host pushes only on a block change.
