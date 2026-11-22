# 295 — 利群汇聚 入口机 offline state + easy 换主入口 + per-entry capability on forwards

Task `leikwan-entry-status` (user 11-22 ~23:55 TW 「检查一下然后赶紧修复」). Base tag 3.0.27-fork.161,
branch `fix/3.0.27-leikwan-entry-status`, worktree `/root/projects/flvxx/flvxx-295`. Panel only, no OTA.

## Problem (prod fork.161)
- 入口机 tiles show green 「运行中」 for offline 火山上海 / 火山广州: tile state = `node.leikwanStatus` snapshot, `node.online` ignored.
- ⚠ only on 火山上海: it comes from a recent 「同步失败：节点连接已断开」 (15-min window); offline itself is no attention reason.
- 换主入口 is hidden behind 授权 row → 修改 → select.
- Forward chips (预热 …) show the 专线/rule config, not what the owner's 主入口 can actually do (host profile files).

## Checklist
- [x] Backend: offline entry = attention reason 「节点离线」 (error, since-when), replaces the 同步失败 duplicate while offline; one helper feeds card ⚠ / 「N 台需要处理」 / H5 count
- [x] Backend: per-host capability helper (profile files → 预热池 / 首包过滤 / 快切触发 / 手机备线 + front build label) shared with lkGen
- [x] Backend: grant options + host list return online / build / capabilities per entry
- [x] Backend: switch preview API (user + candidate entry → per-forward keep/lose, from lkRuleEff + profile files)
- [x] Backend: rule list returns per rule the owner's 主入口 name / online / capability (admin + user)
- [x] Backend: check lkGen push to old + new host after grant save
- [ ] Backend tests (attention reason, capabilities, preview, rule entry caps); `go test ./...` vs base failure set
- [x] Frontend: offline = red dot + 「离线」 on tile, H5 row, drawer, grant options, diagnose / other entry-state spots
- [x] Frontend: 「换主入口」 on grant row + clickable 主入口 chip → grant dialog; entry options show online/build/capabilities; offline disabled
- [x] Frontend: dialog preview of the user's forwards on the picked entry
- [x] Frontend: forward-row chips (预热 / 首包过滤 / 快切 / 手机备线) grey 「入口不支持」 + tooltip; admin + user, desktop + H5; user view shows 主入口 离线
- [x] `npm run build` clean
- [x] Lab on prod-DB snapshot (entries online/offline, with/without warm), screenshots desktop + H5 390 light + dark → gallery :8791/leikwan-entry-status/
- [ ] Release next free fork (162 expected): tag, CI + Images + assets, Latest
- [ ] Prod backup + rollback script, prune backups, swap, verify (nodes, forwards, 利群 tables, log 15 min, 5 busiest forwards), prod screenshots
- [ ] Ledger row 295, memories, tell manian-to-flvx brief owner, cleanup lab/worktree

## Notes (as built)
- Attention: `leiKwanHostManageViewAt` puts 「节点离线」 first (level error, `offlineSince` = node.updated_time of the
  离线 flip); a recent 「同步失败」 is dropped while the node is offline. `attentionLevel` comes from the backend
  (`lkAttentionLevel`), the UI only falls back to the old string rule.
- Capabilities (`leikwan_entry_caps.go`): learned profile files → 预热池 / 首包过滤 / 日志快切 / 快切触发 / 手机备线,
  client label from the agent report (stable → 稳定版). Unlearned host = 能力未知 (nothing greyed).
- Rule rows (admin): `entryLimits` = what the rule config wants (lkGen `generic` on the owner's 主入口) that the
  主入口 cannot run; 手机备线 only for rules that have a phone port on some entry. Users get `entry.online` only
  (the user page never showed 预热, plan 270).
- Preview `POST /api/v1/leikwan/grant/preview {userId, mainEntryNodeId}` (admin, read-only, no kick): lkGen
  `effective` (incl. per-host pinned values) + files on the current and the new 主入口, per rule changes + kept.
- grant/save refuses moving a user onto an offline entry (keeping an offline current entry still saves). The
  existing kick after grant/save still pushes every online 利群入口 (old + new) within ~2 s.
- Select: the 主入口 picker uses the bridge `grouped` listbox (rich rows; the native select cannot show pills).
- Diagnose wording 「入口机不在线」 → 「入口机离线」.
