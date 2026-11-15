# 263 — Release fork.135: 订阅「全部用户」视图 (260) + 规则集链接 (261) — panel only

用户 11-15 TW 看完 261 v3 截图（「很不错很漂亮」）后：两个小改动（节点多时的「走哪里」+ 节点图标颜色；编辑器「客户端」标签 +
「通用」改名「节点链接」）做完后「直接全部安排发布」，不再需要截图确认。只升面板，不做 agent OTA。

- 分支 `maintenance/3.0.27-fork.135` = tag `3.0.27-fork.134`（30527a06，plan 259/262 线路监测温和关闭）+ merge
  `feature/3.0.27-sub-rules-link`（含 260 的 3427a529、261、v3 UI、UI 跟进 2 = 9c062d46）。无冲突，259 的文件与 fork.134 逐字节相同。
- 数据库：GORM 自动建新表 `user_sub_ruleset`（其它不变）。回滚 = 换回 fork.134 镜像，新表留着不用。

## Checklist
- [x] (A) UI 跟进 2 在 feature 分支完成、推送、CI 绿（plan 261「UI 跟进 2」）
- [x] 新 worktree flvxx-rel135 从 fork.134 合并 feature 分支，核对 259 改动保留
- [x] 全量测试：go-backend build/vet/test（SQLite）+ PG 18 repo/handler/contract、go-gost/x test、go-gost build/test、npm run build
- [x] 推送分支，CI 绿；打 tag `3.0.27-fork.135`，Images + Release（Latest、10 个附件、compose 钉版本、脚本 PINNED_VERSION、gost sha256）
- [x] 生产备份（rollback 目录 + 镜像 tag + DB 备份校验）+ prune 只留 2 份
- [x] 升级前：真实订阅数 / routing 是否 NULL（只读）；建管理员临时订阅（routing NULL），经 CF 用 6 个 UA 拉取存档
- [x] 升级面板到 fork.135，健康检查
- [x] 升级后 1：临时订阅同样 6 个 UA 内容逐字节一致
- [x] 升级后 2：真实订阅数 / routing 不变；`user_sub_ruleset` 存在且为空
- [x] 升级后 3：临时订阅加一条规则集链接（公开 .list，一次获取）+ 预览计数；经 CF 拉 Clash / sing-box / Surge；mihomo -t、sing-box check；
      `/s/rs/u/<id>.list` / `.json` 200 + 规则数；未知 id 404；SSRF 抽查（127.0.0.1 / 169.254.169.254 / 节点 IP）拒绝且不入库
- [x] 升级后 4：大陆（leikwan-sh /root/mh-test）mihomo 直连下载规则集链接并启动（一次）
- [x] 升级后 5：生产 UI（管理员）我的订阅 ｜ 全部用户、查看、编辑器分流 → 自己写 → 规则集链接、H5 390 走哪里分组
- [x] 升级后 6：删除临时订阅，`user_sub_ruleset` 行被清理，无残留
- [x] 面板日志干净、内存前后、节点在线不变、规则数量 / 状态不变
- [x] 清理 worktree（rel135、sub-ruleslink、sub-admin）+ 构建缓存；台账 260 / 261 → 已发布；memory 更新

## Results（11-15 TW）
- (A) feature 分支 9c062d46（UI 跟进 2 + 协调者追加的「客户端」标签 /「节点链接」），CI 43704461387 绿；截图 -v4 48 张在 /sub-rules-link/ 最上面。
- 合并：fork.134（30527a06）+ merge f673e046 无冲突；259 的 9 个文件与 fork.134 逐字节相同；go-gost / 安装脚本 / CI 未改。
- 测试：go-backend build/vet OK，SQLite `go test ./...` 全过；PG 18 repo / handler / contract 全过；go-gost/x 27 包全过；go-gost build（-V OK）+ test 全过；
  npm run build OK。分支 CI 43704701235 绿。
- tag `3.0.27-fork.135` → 513f7cb7；Images 43705511834 绿；Release = Latest、非预发布、10 个附件同 fork.134、compose 钉 fork.135、两个脚本
  PINNED_VERSION / REPO 对、gost sha256 + `-V` = 3.0.27-fork.135。
- 备份 `/opt/flvx-svc/rollback/pre-fork135-20261115T110046Z`（quick_check / integrity_check ok，fork.134 镜像本地 tag）；prune 后保留 pre-fork134 + pre-fork135。
- 升级 19:00:54–19:01:04 TW（后端 19:00:58 启动），healthy，0 次重启；`user_sub_ruleset` 自动建表。
- 升级前：真实订阅 3 条（用户 60、1、78），routing 全 NULL。临时订阅「临时测试135」（id 23，管理员，落地 keekao + 自己用，routing `[]`
  —— fork.134 新建订阅一定带默认 8 个分流，`[]` 在代码里和 NULL 走同一条路，用它代替；另外又拉了默认 8 个分流的状态）。
- 升级后：
  1. 经 CF：`[]` 状态 6 个 UA（clash-verge / mihomo / sing-box / Surge / Stash / v2rayN）+ 默认分流状态 3 个 UA，9 份内容全部逐字节一致。
  2. 真实订阅 3 条、routing / 最近拉取都没变（只读）；`user_sub_ruleset` 存在且为空。
  3. 规则集链接：获取 blackmatrix7 Netflix.list（一次，面板从 AWS HK 下载）→ 1156 条（域名 3 / 后缀 30 / 关键词 4 / IP 段 1119，忽略 1），预览计数一致；
     经 CF 拉 Clash / sing-box / Surge 都带「📌 Netflix链接」；mihomo -t、sing-box 1.14.3 check 通过；`/s/rs/u/<id>.list` 200（1156 行）、`.json` 200
     （1156 条）；未知 id 404。SSRF：127.0.0.1、169.254.169.254、FLVX 节点 IP（Mkcloud 深港ixp 1）都被拒绝，提示「地址无效：不能指向内网、本机、面板或节点的地址」，没有入库。
  4. 大陆（leikwan-sh，一次）：经 CF 拉订阅 1.06 s；mihomo 直连下载 15/15 规则集（含规则集链接）3.4 s；baidu → GeoSite(cn) 直连 200；
     www.netflix.com 命中 RuleSet/link-… 进「📌 Netflix链接」组（再从上海连 keekao 落地超时 —— 节点自身问题，fork.132 时就这样）。
  5. 生产 UI（管理员，无写入）：「我的订阅 ｜ 全部用户」、全部用户 3 个用户、查看（无链接 / token，分流里显示规则集链接行）、编辑器「客户端」标签 +「节点链接」、
     分流 → 添加 → 自己写 → 规则集链接、H5 390 走哪里分两组；页面报错 0。
  6. 删除临时订阅（旧链接 404）；规则集行按设计等 30 分钟后由下一次 GC 清理（见下）。
- 面板日志 0 个 error；后端内存 40 MiB → 37 MiB；28/28 节点在线、版本不变；51 条规则状态、链式代理记录、真实订阅前后完全一致。
- 第 6 步收尾：临时订阅 19:04:06 TW 删除（旧链接 404）；规则集行在 19:33:17 TW 管理员下一次打开订阅列表时被 GC（表 0 行，`/s/rs/u/<id>.list` 404），无残留。
  注意：规则集只有在获取 30 分钟后（subLinkPending）才会被 GC —— 设计如此（给「获取后还没保存」留时间）。
- 旁注：19:12:34 TW 管理员在界面上（经 CF，台湾 IP）自己删除了旧测试订阅 16「rb1107b-sub」（全部用户 → 查看 → 删除），不是发布脚本做的；
  如需恢复，行在 pre-fork135 备份的 gost.db.validated 里。
