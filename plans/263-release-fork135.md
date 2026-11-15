# 263 — Release fork.135: 订阅「全部用户」视图 (260) + 规则集链接 (261) — panel only

用户 11-15 TW 看完 261 v3 截图（「很不错很漂亮」）后：两个小改动（节点多时的「走哪里」+ 节点图标颜色；编辑器「客户端」标签 +
「通用」改名「节点链接」）做完后「直接全部安排发布」，不再需要截图确认。只升面板，不做 agent OTA。

- 分支 `maintenance/3.0.27-fork.135` = tag `3.0.27-fork.134`（30527a06，plan 259/262 线路监测温和关闭）+ merge
  `feature/3.0.27-sub-rules-link`（含 260 的 3427a529、261、v3 UI、UI 跟进 2 = 9c062d46）。无冲突，259 的文件与 fork.134 逐字节相同。
- 数据库：GORM 自动建新表 `user_sub_ruleset`（其它不变）。回滚 = 换回 fork.134 镜像，新表留着不用。

## Checklist
- [ ] (A) UI 跟进 2 在 feature 分支完成、推送、CI 绿（plan 261「UI 跟进 2」）
- [ ] 新 worktree flvxx-rel135 从 fork.134 合并 feature 分支，核对 259 改动保留
- [ ] 全量测试：go-backend build/vet/test（SQLite）+ PG 18 repo/handler/contract、go-gost/x test、go-gost build/test、npm run build
- [ ] 推送分支，CI 绿；打 tag `3.0.27-fork.135`，Images + Release（Latest、10 个附件、compose 钉版本、脚本 PINNED_VERSION、gost sha256）
- [ ] 生产备份（rollback 目录 + 镜像 tag + DB 备份校验）+ prune 只留 2 份
- [ ] 升级前：真实订阅数 / routing 是否 NULL（只读）；建管理员临时订阅（routing NULL），经 CF 用 6 个 UA 拉取存档
- [ ] 升级面板到 fork.135，健康检查
- [ ] 升级后 1：临时订阅同样 6 个 UA 内容逐字节一致
- [ ] 升级后 2：真实订阅数 / routing 不变；`user_sub_ruleset` 存在且为空
- [ ] 升级后 3：临时订阅加一条规则集链接（公开 .list，一次获取）+ 预览计数；经 CF 拉 Clash / sing-box / Surge；mihomo -t、sing-box check；
      `/s/rs/u/<id>.list` / `.json` 200 + 规则数；未知 id 404；SSRF 抽查（127.0.0.1 / 169.254.169.254 / 节点 IP）拒绝且不入库
- [ ] 升级后 4：大陆（leikwan-sh /root/mh-test）mihomo 直连下载规则集链接并启动（一次）
- [ ] 升级后 5：生产 UI（管理员）我的订阅 ｜ 全部用户、查看、编辑器分流 → 自己写 → 规则集链接、H5 390 走哪里分组
- [ ] 升级后 6：删除临时订阅，`user_sub_ruleset` 行被清理，无残留
- [ ] 面板日志干净、内存前后、节点在线不变、规则数量 / 状态不变
- [ ] 清理 worktree（rel135、sub-ruleslink、sub-admin）+ 构建缓存；台账 260 / 261 → 已发布；memory 更新
