# 280 — 「规则」→「转发」 wording + separate 利群转发数 per user

Base: tag `3.0.27-fork.148` (7b830a69). Branch `maintenance/3.0.27-fork.150-forward-rename` (rebased onto fork.149 = plan 282 before release).
Panel only (go-backend + vite-frontend); go-gost untouched → no agent OTA.

## User decisions (11-18, voice)
1. A forwarding row called 「规则」 is unclear → call it **「转发」** everywhere in the UI (incl. 利群汇聚).
2. 落地 card: 「用于新规则」 → 「添加转发」; 「路由规则」 button + dialog title → 「设置路由」 (dialog contents keep 规则).
3. New per-user **利群转发数** on the 用户 edit page under 总转发数 (not in the grant dialog).

## Behaviour of 利群转发数
- New column `user.leikwan_num` (int, NOT NULL, DEFAULT 3; additive AutoMigrate → existing rows get 3).
- 总转发数 (`user.num`) now counts normal forwards + pending 「我自己的节点」 chain creates only (利群 rules removed).
- 利群 rules count only against `leikwan_num`; `RuleLimitUnlimited` sentinel (≤0 or ≥99999 = 不限) shared with num.
- Only new creates are refused; rows already over the limit stay. Admin actor exempt (as today).
- Error: 「已达到利群转发数上限（N 条），请删除不用的利群转发后再添加」.
- API: admin user create/update accept `leikwanNum` (create default 3; update absent = keep), user list +
  dashboard userInfo return `leikwanNum`; 利群 options return `ruleLimit {used, limit}`.

## Checklist
- [x] Backend: model column + repo split (countUserRules w/o 利群, CheckLeiKwanRuleLimit vs leikwan_num)
- [x] Backend: user create/update/list/dashboard `leikwanNum`; 利群 options `ruleLimit`
- [x] Backend: user-facing 规则→转发 strings (forwards only) + contract tests that assert them
- [x] Go tests: split counts, at-limit refusal, unlimited sentinel, admin exempt, migration default 3 on existing DB
- [x] Frontend wording, file by file (table below) + keep list
- [x] Frontend: 用户 edit 利群转发数 (desktop + H5), dashboard 利群转发 x / N, 利群 user page 已用 x / N + disabled add
- [x] `npm run build`, `go vet`, `go test ./...` (SQLite + PG)
- [x] Post-build grep: every remaining 「规则」 in UI text is in the keep list
- [x] Screenshots desktop + 390 px → gallery http://104.145.236.29:8791/forward-rename/
- [x] Ledger row 280 (built)
- [ ] Release fork.150: push, tag, CI, images, 10 assets, Latest
- [ ] Prod: backup + rollback script, previous fork on migrated prod-DB copy, prune, swap, checks
- [ ] Ledger → 已发布, memory updated, worktree + caches removed

## Results (build)
- `go vet ./...` clean; `go test -p 1 ./...` (SQLite + PG `FLVX_POSTGRES_TEST_DSN`): 28/28 packages ok after updating two
  contract asserts that still expected the old wording (TestLeiKwanQAFixContract 规则名称太长/利群规则,
  TestTunnelSplitEntryPortForwardAndRefusalsContract 规则在该入口上监听) — both caused by this change, not pre-existing.
- New tests: TestLeiKwanNumSplitFromForwardNum, TestLeiKwanNumMigrationDefault (repo, SQLite + PG),
  TestUserLeiKwanNumAPIContract (SQLite + PG); TestLeiKwanGrantBackupContract rewritten for the split.
- `npm run build` (tsc + vite) ok. Remaining 「规则」 in src (non-comment) = keep list only.
- Lab (scrubbed prod-DB copy, isolated netns, :18585/:18587): 21 + 8 checks ok, 0 page errors, 20 screenshots
  → http://104.145.236.29:8791/forward-rename/ (IPs / domains masked in the rendered text).

## Wording changes (old → new)
Rule: 「规则」 meaning a forward (Forward rows, chain 「我自己的节点」 rules, 利群 rules, their counts) → 「转发」.
Full per-line log of the frontend edits: /root/flvx-workers/runs/forward-rename/work/fe-changes.json (306 lines);
backend edit list: /root/flvx-workers/runs/forward-rename/work/backend_rename.py (+ 9 follow-up port/WG strings).

| Old | New | Where |
|-----|-----|-------|
| nav 规则 | 转发 | layouts/admin.tsx (sidebar), layouts/h5.tsx (bottom bar) |
| 新增规则 / 编辑规则 / 复制规则 | 添加转发 / 编辑转发 / 复制转发 | forward.tsx dialog title |
| 修改现有规则配置 / 创建新的规则配置 | 修改现有转发 / 添加新的转发 | forward.tsx dialog subtitle |
| 规则名称 / 请输入规则名称 | 转发名称 / 请输入转发名称 | forward.tsx, leikwan rule-actions.tsx, backend line_chain/leikwan_line |
| 暂无规则配置 / 还没有(创建)任何规则配置…开始创建 | 暂无转发 / 还没有(添加)任何转发…开始添加 | forward.tsx |
| N 个规则 / N 项规则 / N 条规则 | N 条转发 | forward.tsx, tunnel.tsx, panel-sharing.tsx, node.tsx, leikwan pages |
| 全部规则 / 搜索筛选规则 / 规则名称 (模糊) | 全部转发 / 搜索筛选转发 / 转发名称 (模糊) | forward.tsx |
| 获取规则列表失败 / 正在刷新规则列表 | 获取转发列表失败 / 正在刷新转发列表 | forward.tsx |
| 规则状态异常 / 规则数据 / 导入·导出规则数据 | 转发状态异常 / 转发数据 / 导入·导出转发数据 | forward.tsx |
| 规则诊断结果 / 规则服务 | 转发诊断结果 / 转发服务 | forward.tsx |
| 规则最优 (+ 已自动关闭/重新开启) | 转发最优 | forward.tsx, tunnel.tsx, best-pick.tsx, switch-margins-card.tsx, backend tunnel_rule_best notice |
| 关联规则 / 规则主人 / 迁移规则 / 保留规则 | 关联转发 / 转发主人 / 迁移转发 / 保留转发 | tunnel.tsx delete dialogs |
| 落地 card 用于新规则 | 添加转发 | landing.tsx |
| 落地 card 路由规则 (N) | 设置路由 (N) | landing.tsx |
| dialog header 路由规则 · 节点名 | 设置路由 · 节点名 | landing/node-rules.tsx (loading + loaded header) |
| 「我的节点 → 路由规则」 / 「路由规则」里选这个出站 | 「我的节点 → 设置路由」 / 「设置路由」里选这个出站 | egress-global.tsx, egress-outbound-editor.tsx (references to the button) |
| 将此节点用于新规则后，每个规则入口… | 用此节点添加转发后，每个转发入口… | landing.tsx |
| 经规则转发 / 经规则「x」 | 经转发 / 经转发「x」 | landing.tsx, link-card.tsx, egress.tsx |
| pill 规则 N (落地 card) | 转发 N | landing.tsx |
| 链式规则 | 链式转发 | landing.tsx, backend line_chain / repository_line_chain |
| 「我自己的节点」规则 | 「我自己的节点」转发 | backend (line_chain, mutations, repository_*), line_machine |
| 利群规则 (everywhere) | 利群转发 | leikwan admin/user pages, rule-actions, grant-dialog, settings-modal, sub-dialog, diagnose-modal, chain-proxy, link-card, landing-external.ts, sub-link, user.tsx, backend leikwan_* |
| 新增利群规则 (buttons) / ＋ 建规则 / 在这条专线建规则 | 添加转发 / ＋ 添加转发 / 在这条专线添加转发 | leikwan admin-page.tsx, user-page.tsx |
| modal title 新增利群规则 / submit 建规则 / toast 利群规则已建好 | 添加利群转发 / 添加 / 利群转发已添加 | leikwan rule-actions.tsx |
| 专线 · 规则 / H5 tab 规则 N / LkField 规则 | 专线 · 转发 / 转发 N / 转发 | leikwan admin-page.tsx, user-page.tsx |
| 利群专线的规则…不进隧道页和规则页 / 跟「规则」页分开 | …转发…不进隧道页和转发页 / 跟「转发」页分开 | leikwan pages |
| 规则数、流量、到期跟账号走 | 转发数、流量、到期跟账号走 | leikwan pages |
| grant dialog 「规则数和到期跟用户：利群规则算进用户的总规则数…」 | 「利群转发数和到期跟用户：利群转发数在用户编辑里设，不占总转发数；用户到期后不能再添加。」 | grant-dialog.tsx (behaviour changed) |
| 名额…建规则时才扣 / 建不了新规则 | …添加转发时才扣 / 加不了新转发 | grant-dialog, shared.tsx, rule-actions.tsx |
| 用户 edit 规则数量 | **总转发数** (user called it 总规则数 → 总转发数) | user.tsx edit dialog |
| user list 规则数 / H5 card 规则数量 | 转发数 / 转发数量 | user.tsx |
| 主页 已用规则 / 规则配额 / 已超出规则上限… | 已用转发 / 转发配额 / 已超出转发上限，删除部分转发后才能添加 | dashboard.tsx |
| 订阅 tag/group 规则 | 转发 | sub-link editor.tsx, routing.tsx, admin-detail.tsx |
| 我的转发规则 / 用户的转发规则 / 我的利群汇聚规则 | 我的转发 / 用户的转发 / 我的利群转发 | sub-link editor.tsx, admin-detail.tsx |
| 全部利群规则（自动跟随） | 全部利群转发（自动跟随） | sub-link editor.tsx, admin-detail.tsx, backend sub_link.go |
| 规则页面精简模式 | 转发页面精简模式 | config.tsx |
| 规则 #id / 规则「x」 | 转发 #id / 转发「x」 | panel-sharing.tsx, backend port holders, tunnel delete, cn_landing |
| 规则端口段 (节点 forward port range) | 转发端口段 | enable-button.tsx, server-settings.tsx, backend landing ports |
| RuleLimitError 已达到规则数上限（N 条），请删除不用的规则后再新建 | 已达到转发数上限（N 条），请删除不用的转发后再添加 | backend repository_rule_limit.go |
| 账号已到期，不能新建利群规则 | 账号已到期，不能添加利群转发 | backend |
| package 规则 N (shop / myhome) | 转发 N | shop.tsx, myhome.tsx |
| WG 规则 / WG 隧道规则 (WG forwards) | WG 转发 / WG 隧道转发 | backend control_plane / mutations |
| 测试规则：预热只在… | 测试转发：预热只在… | backend leikwan_import |
| default landing name 规则落地 | 转发落地 | backend landing.go (fallback when the request has no name) |

Non-obvious picks:
- 「个规则 / 项规则」 → 「条转发」 (「个转发」 reads badly).
- 用户 edit input label 规则数量 → 「总转发数」 (not 转发数量), so it pairs with the new 「利群转发数」 and the hint 「不占总转发数」.
- 「经规则转发」 → 「经转发」 (avoids 转发转发); 「转发规则」 → 「转发」; 「内网转发规则」 → 「内网转发」.
- 「规则只能转发到 X」 → 「转发的目标只能是 X」 (node.tsx 线路机 hints).
- 用户删除 dialog 「N 条利群规则…（利群转发 + 备线）」 → 「N 条利群转发…（利群那边的转发 + 备线）」 (the old text used 利群转发 for the vendor side).
- 利群 「建规则」 submit button → 「添加」 (next to 「保存」 in swap mode).

## 「规则」 kept on purpose
- 设置路由 dialog contents: 我的规则, 服务器规则, 全局规则 / 强制规则 / 默认规则, 封锁规则, 出站规则, 「没有命中任何规则的流量走这里」,
  「已达到规则上限」, 「规则的修改还没有保存」, toast 「路由规则已保存，正在同步」 (inner text of the dialog).
- Egress / routing editors: 添加规则, 编辑规则 · x, 规则名称, 启用/停用/删除规则, 规则操作, 规则 N, 规则预设, 安全规则, 内置规则,
  默认防滥用规则, 防滥用规则, 节点规则 (landing node routing rules + their limits 节点规则：每个节点最多…), 用户规则 (global 用户规则上限),
  「可被你的规则覆盖」, admin ⚠ 「路由规则应用失败」, 「N 个节点的路由规则」, 「用于全局规则」.
- Subscription 规则集 / 规则集链接 / 规则集地址 / 不支持的规则 / 「规则用 App 自己的」 / 「不带分流和规则，由客户端自己的规则决定」 /
  「使用旧版规则：国内直连 / 全部走代理」: these are routing rule sets (Surge/Clash .list/.yaml domain/IP rules), not forwards —
  **the brief's 「规则集链接 → 转发集链接」 was not applied** (it would mislabel a real rule set).
- 限速规则 / 总限速规则 (retired speed rules), nftables 规则 / WG·nftables 规则清理 (kernel rules), backend 按规则分流 (clash rule mode).
- Backend log lines (log.Printf: 利群规则 屏蔽/备线, 删除用户…利群规则, [rule-best], [speed-ref-cleanup]) — not user-facing.
- Code comments, identifiers, API paths, JSON fields, DB columns, localStorage keys: unchanged.
