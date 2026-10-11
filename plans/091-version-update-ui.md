# 091 — 移动端版本与更新反馈（fork.42）

基于 `maintenance/3.0.27-fork.41-tunnel-tls-default` / `36b72cdb`，仅实现指定的前端版本显示、更新前提示和更新后 toast。不修改更新策略、后端、代理、安装脚本或生产业务数据。

## 任务清单

- [x] 阅读项目约束、发布/备份流程，创建 fork.42 分支；保留未跟踪的计划 048。
- [x] 移动端 admin/H5 菜单显示 `siteConfig.version`，桌面及既有 WebView app_version 保持原样。
- [x] 两个现有刷新入口显示「发现新版本，正在更新…」，不增加刷新等待。
- [x] 启动时记录 `flvx-last-build`（build/version），全局一次性成功 toast，跳过 dev/WebView。
- [ ] tsc/build/precache/lint 与 fork.41 基线验证。
- [ ] Playwright (a) d41→新版本、(b) A→B、(c) 首次访问及 60 秒空闲对照、(d) 移动/H5/桌面截图、深色模式。
- [ ] 升级前用持久 Chromium profile 安装真实生产 fork.41 SW。
- [ ] 推送分支、CI 通过、tag fork.42、镜像工作流与 release 资产核验。
- [ ] 生产在线备份、镜像回滚标签，运行 prune-backups.sh 保留最新两份。
- [ ] 升级生产，健康/节点/version.json 及真实旧→新 E2E 验证。
- [ ] 完成中文总结、回滚说明及最终计划提交。

## 行为与已接受边界

- 仅在刷新 guard 成功后或 onNeedRefresh 调用 updateSW(true) 前显示更新提示；不改定时器、guard、更新触发或 SW 策略。
- localStorage 保存 JSON `{build: __APP_BUILD_ID__, version: siteConfig.version}`。已有 build 不同时提示；没有记录且 navigation.type 为 reload 时提示；随后保存当前 build，后续访问不重复。
- 接受极少数误报：首次访问新版本且尚无记录时，手动刷新也可能显示「已更新到 v…」。覆盖 fork.42 之前没有 build 记录的自动刷新路径。
- Playwright 脚本和证据仅存 `/root/flvx-workers/runs/version-update-ui/`。Chromium 串行，启动前 MemAvailable ≥750 MiB，systemd scope MemoryMax=650M / MemorySwapMax=0。

## 验证与发布记录

待完成。
