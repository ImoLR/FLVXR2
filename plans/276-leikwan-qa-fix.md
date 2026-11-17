# 276 — 利群 QA 修复（F01–F16）

Source: `/root/flvx-workers/runs/leikwan-qa/qa-report.md` (plan 275). Base `3.0.27-fork.144`, branch
`feature/3.0.27-leikwan-qa-fix`. Panel only unless F08 needs the agent.

## Checklist
- [ ] F01 delete user cascades 利群 rules (LeiKwan forward + auto 备线 + grant + auto 利群汇聚 sub); API failure refuses the delete; confirm dialog count; missing user → block 「用户已删除」
- [ ] F02 clear BackupError on successful retarget; 「重试备线」 retargets existing 备线 (fallback delete + recreate); row 「备线还指向旧落地」
- [ ] F03 grace ends only when the rendered sub had ≥1 of the user's 利群 nodes
- [ ] F04 user page amber grace notice (desktop + H5)
- [ ] F05 refuse deleting 外部节点 / panel landing node used by 利群 rules; inUse counts 利群 rules
- [ ] F06 user update / disable / expiry / flow reset / renewal → `leiKwanHostKick()`
- [ ] F07 each loop reconciles auto-备线 pause/resume with the block state
- [ ] F08 sskeys Remove for forward ids on the host no longer wanted
- [ ] F09 user 诊断 备线 row 「备线（FLVX 隧道）」, no internal wording
- [ ] F10 name too long → 「规则名称太长」; bad requestId own message; input maxLength
- [ ] F11 409 forward_quota_exceeded → 「利群名额已满，请联系管理员」
- [ ] F12 grant status 0 → block 「授权已停用」; link refused
- [ ] F13 entry address no wrap; F14 H5 dark 换落地 readable
- [ ] F15 plan-272 doc text; F16 sub card + 取消授权 dialog text
- [ ] Tests: go test ./..., contract F01/F02/F03/F05/F06/F12, vet, npm run build
- [ ] Lab re-run s3/s8/s19/s22 + F06 timing (before/after)
- [ ] Gallery /root/mmwx-preview/leikwan-qa-fix/
- [ ] Release (after 274 + 273 are done): rebase, tests, tag, CI, prod backup + upgrade, verify
- [ ] Ledger + memories

## Notes (not in scope: QA risks)
- R1 one ultra-client per 利群 rule on each of the 4 entries (25–50 MB each) — count memory before raising quota.
- R2 prod 利群 quota 7/8.
- R3 百度云广州 journalctl follower RSS 102 MB — watch.
- R4 阿里广州 nft shows ports as `0x2720 [invalid type]` — display only.
- R5 阿里广州 / 火山广州 warm overrides for 263/264/280/282 (all-entries rules) — fine.
- R6 agent WS reconnects cluster in the night and on releases — normal.
