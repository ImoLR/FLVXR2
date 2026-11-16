# 268 — 利群授权精简 + 每条专线自选备线隧道（fork.139，只发面板）

基线：tag `3.0.27-fork.138`（10dbcd0a），分支 `feature/3.0.27-leikwan-grant-backup`，worktree `/root/projects/flvxx/flvxx-leikwan-grant`。
发布分支 `maintenance/3.0.27-fork.139-leikwan-grant`，tag `3.0.27-fork.139`，只换面板（不 OTA）。

## 用户决定（11-17 01:00–02:10 台湾）

1. 授权只留 **主入口 + 备线开关**。流量 / 到期 / 规则数从授权弹窗和 API 去掉（DB 列保留、忽略）。
   - 到期：跟用户的到期（用户过期 → 不能建利群规则；规则显示「已到期」）。
   - 规则数：利群规则算进用户的总规则数（user.num），和普通规则一样检查。
   - 流量：第二步才有字节，这次哪里都不显示流量。
2. 每条利群专线一条备线隧道，管理员从**任意隧道**里选（和建规则时一样按线路分组列出）。
   - 备线出口 = 规则的落地（自动备线规则，目标 = 规则落地，266 已做）。
   - 路径固定经前置（用户主入口机）：选择器里一行路径
     「用户 → 百度云广州（前置）→ Mkcloud 深港ixp 1 → … → 落地」；面板有前置 → 备线入口的延迟就显示，> 30 ms 琥珀色提醒；
     没数据不新增探测。提示一行「备线经主入口转发，用户地址不变」。
   - 换备线隧道：新规则用新的；现有**自动**备线给「按新备线重建」（管理员、每条专线、要确认）；
     **链接**的导入备线（148/595/601/587/599/619）永远不自动移动。
   - 管道隧道「利群·广港·AWS HK」没有备线，不动。
3. 界面只加不改，和 266 一样的样子；桌面 + H5 390，浅色 + 深色；截图放 /root/mmwx-preview/leikwan-grant/。

## 检查清单

- [x] 读 memory / 266 / 267 / 发布流程；建 worktree；只读查生产（导入已由用户跑过：4 条专线、1 个授权、7 条 linked 规则）。
- [x] 后端：授权 API 忽略 num/flowGb/expTime，视图不再返回；建利群规则检查用户状态/到期 + 总规则数（user.num 计入利群规则）；
      普通规则的总规则数也计入利群规则；主页「已用规则」加上利群规则数。
- [x] 后端：备线隧道可选任意隧道；line/list 给出线路分组、路径（入口→中间→出口）、前置→备线入口延迟（探针延迟簿，已有数据才给）；
      每条专线统计「不在当前备线隧道上的自动备线」；新增 `line/backup-rebuild`（只动自动备线，先建新再删旧，链接的不动）。
- [x] 前端：授权弹窗只留主入口 + 备线；授权表/授权卡去掉流量、到期、规则上限；专线弹窗备线隧道按线路分组 + 路径行 + 延迟 + 提示；
      专线表「按新备线重建」（确认）；用户规则区到期显示「已到期」、不能建。
- [x] 测试：契约测试（SQLite + PostgreSQL）覆盖授权忽略字段、用户到期拦截、总规则数计入利群、任意隧道、自动备线用专线隧道、
      重建、链接不动；`go test ./...`、`go vet ./...`、`npm run build`；CI 绿。
- [x] 实验室截图（桌面 + H5 390，浅色 + 深色）→ /root/mmwx-preview/leikwan-grant/。
- [ ] 发布：分支 + tag fork.139、CI + 镜像 + 资产；等夜间 OTA 结束且 04:20 后换面板；备份 + 回滚脚本，留 2 份；
      验证 30 在线、规则数不变、利群汇聚页、导入预览、真实授权弹窗只剩主入口 + 备线。
- [ ] 账本 268 行、memory（fork-release-and-deploy、leikwan-panel-step1）、总结 runs/leikwan-grant-backup/summary.md。

## 记录

- 生产只读查询（11-17 02:20 台湾）：用户已跑过导入 — 专线 广港→测试·263备线（端口转发）、沪日→mk深港ixp1→HK（端口转发）、
  广台/福台→mk深港ixp1→TW（**隧道转发**，所以「任意隧道」本来就需要）；授权 1 个（imolr，主入口 百度云广州，备线开）；
  7 条利群规则全是 linked 备线（148/601/619/595/587/599）。这些数据不动。
- 实现：`countUserRules` 加上 `leikwan_rule`（总规则数 = 普通 + 利群，自动备线归管理员不算）；新 `CheckLeiKwanRuleLimit`
  （状态/到期对所有人，规则数只管普通用户，和普通规则一样管理员豁免）；授权 save 仍接受 num/flowGb/expTime（旧缓存前端）但不存、
  视图不返回；`line/save` 备线隧道只要存在即可；`line/list` 的 backupTunnels 带 入口节点 id / 中间 / 出口 / 线路分组 /
  前置→入口延迟（探针延迟簿 5 分钟内的边，两个方向都看，> 30 ms 标 far）、`fronts`（授权里用到的主入口，没有就整组）、
  每条专线 `backupStats{auto,linked,stale}`；新 `line/backup-rebuild`（先建新再删旧，失败保留旧；缺的补建；专线不建备线时删自动备线；
  linked 永不动）。主页 `userInfo.leikwanRuleCount` → 「已用规则」= 普通 + 利群。实验室钩子 `FLVX_LEIKWAN_LAB_EDGES`
  只在 build tag `leikwanlab` 里。
- 测试：新契约测试 TestLeiKwanGrantBackupContract（SQLite + PostgreSQL 都跑了）、handler 单测 TestLeiKwanBackupTunnelViewsLatency /
  TestLeiKwanPlanBackup；266 的 TestLeiKwanLinePanelContract 照样过。`go vet ./...` 0；`go test ./...`（带 PG DSN）全部 ok —
  第一次跑 repo 和 contract 两个包撞到默认 10 分钟超时（PG 慢，不是断言失败），`-timeout 60m` 重跑 554 s / 528 s 通过。
  前端 tsc 0 错、改动文件 eslint 0 警告、`npm run build` 通过。
- 实验室（/root/lgb-lab，unshare -n -m 只有回环，生产库在线备份副本，利群 token 换假的，假利群 API）：
  test 用户授权 阿里广州 + 备线 → 建规则 → 自动备线在 测试·263备线；广港改成 mk深港ixp1→HK 后 stale 1 → UI 重建 →
  自动备线搬到新隧道、旧的删掉；linked 7 条全部没变；授权对话框 0 个输入框（只剩主入口下拉 + 开关）；用户到期 → options/create 都提示
  「账号已到期，不能新建利群规则」，规则显示「已到期」；主页 leikwanRuleCount=1；利群写入只有建规则那 1 次；页面 JS 错误 0。
  截图 36 张：/root/mmwx-preview/leikwan-grant/（index.html 中文说明）。
