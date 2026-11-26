# 315 — App 授权「我的入口」 + 专线头「端内」不挤名字 + 订阅节点名固定

User 2026-11-26 evening (voice, after trying im.gamer App 1.0.10-panel on fork.174). Build + screenshots only:
no release, no OTA, no production change. Branch `feature/3.0.27-app-grant-entry` from fork.174 (e604e65d),
worktree `/root/projects/flvxx/flvxx-315`. Gallery `/root/mmwx-preview/app-grant-entry/`
(http://104.145.236.29:8791/app-grant-entry/).

## A. 「端内」 tag breaks the 专线 header on phones
- H5 `AdminPhone` 专线 header: the name never wraps (nowrap, no shrink), the header stays one line at 360 px /
  130 % text. 端内 moves into the right cluster, just before the count bubble.
- If it does not fit at 360 px / 130 %, the route pill leaves the H5 header and goes to the expanded area's
  chip row (`LineChips`). Alternative rendered for comparison: 端内 as a small second line under the name.
- Check the other plan 306/310 tags on H5 (rule rows 到落地, 诊断) for the same wrap.

## B. App: connecting 手机本地 lives in 授权, 入口机 → 手机本地 is info only
- 授权 tab top card 「我的入口」 (App + admin's own grant only): dropdown 手机本地 + server 利群入口.
- Under it when 手机本地 is picked: setup + connect panel (tips first time, checklist, 快捷设置, 连接 / 断开 +
  stage block like the old App, 专线状态, 更多 = 运行日志 / 换面板地址, short notes).
- 入口机 → 手机本地 (H5 row + desktop drawer): info only + 「连接和设置在 授权 → 我的入口」.
- Browsers and normal users: unchanged (pixel check vs fork.174).

## C. Subscription node names stay the same when the entry changes
- `user_subscription.entry_names` (nullable bool): NULL / true = today's names (byte-identical output),
  false = fixed names (forward name; 「 · 入口名」 only when the same forward is in this sub through ≥ 2 entries).
- New subscriptions (editor, 利群汇聚 生成订阅) default = fixed names (switch off); existing rows stay NULL.
- Editor switch 「节点名带入口名」 + hint; read-only in the admin 全部用户 view.

## Checklist
- [ ] Read code, plan doc
- [ ] Lab: prod-DB snapshot (read-only copy), fork.174 + branch binaries/dists, bridge mock
- [ ] A: H5 专线 header (recommended + alternative), check rule rows / 诊断
- [ ] B: 授权 「我的入口」 card + dropdown + 换主入口 confirm for another server entry
- [ ] B: setup / connect panel (tips, checklist, 快捷设置, stage block, 专线状态, 更多)
- [ ] B: 入口机 → 手机本地 info only (H5 row + desktop drawer/tile)
- [ ] C: DB column + model + repo + handler (create defaults, update, views)
- [ ] C: naming in SubPanelLinks / resolve (landing / chain / external forwards)
- [ ] C: editor switch + admin view read-only
- [ ] C: golden tests (old unchanged, fixed: single entry, entry switched, two entries)
- [ ] C: prod-copy count of subscriptions whose names would change
- [ ] go test SQLite all + PG handler/repo/contract; npm build + lint/tsc as CI
- [ ] Screenshots + browser-unchanged pixel check + gallery
- [ ] Push, CI green
- [ ] Ledger row 315, summary

## Decisions
(filled while building)
