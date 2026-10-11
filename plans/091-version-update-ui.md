# 091 — 移动端版本与更新反馈（fork.42）

基于 `maintenance/3.0.27-fork.41-tunnel-tls-default` / `36b72cdb`，仅实现指定的前端版本显示、更新前提示和更新后 toast。不修改更新策略、后端、代理、安装脚本或生产业务数据。

## 任务清单

- [x] 阅读项目约束、发布/备份流程，创建 fork.42 分支；保留未跟踪的计划 048。
- [x] 移动端 admin/H5 菜单显示 `siteConfig.version`，桌面及既有 WebView app_version 保持原样。
- [x] 两个现有刷新入口显示「发现新版本，正在更新…」，不增加刷新等待。
- [x] 启动时记录 `flvx-last-build`（build/version），全局一次性成功 toast，跳过 dev/WebView。
- [x] tsc/build/precache/lint 与 fork.41 基线验证。
- [x] Playwright (a) d41→新版本、(b) A→B、(c) 首次访问及 60 秒空闲对照、(d) 移动/H5/桌面截图、深色模式。
- [x] 升级前用持久 Chromium profile 安装真实生产 fork.41 SW。
- [x] 推送分支、CI 通过、tag fork.42、镜像工作流与 release 资产核验。
- [x] 生产在线备份、镜像回滚标签，运行 prune-backups.sh 保留最新两份。
- [x] 升级生产，健康/节点/version.json 及真实旧→新 E2E 验证。
- [x] 完成中文总结、回滚说明及最终计划提交。

## 行为与已接受边界

- 仅在刷新 guard 成功后或 onNeedRefresh 调用 updateSW(true) 前显示更新提示；不改定时器、guard、更新触发或 SW 策略。
- localStorage 保存 JSON `{build: __APP_BUILD_ID__, version: siteConfig.version}`。已有 build 不同时提示；没有记录且 navigation.type 为 reload 时提示；随后保存当前 build，后续访问不重复。
- 接受极少数误报：首次访问新版本且尚无记录时，手动刷新也可能显示「已更新到 v…」。覆盖 fork.42 之前没有 build 记录的自动刷新路径。
- Playwright 脚本和证据仅存 `/root/flvx-workers/runs/version-update-ui/`。Chromium 串行，启动前 MemAvailable ≥750 MiB，systemd scope MemoryMax=650M / MemorySwapMax=0。

## 验证与发布记录

- `npx tsc --noEmit`、三次 `npm run build`（A/B/最终菜单位置）通过；最终主 JS 2,748,021 字节，远低于 5 MiB，`version.json` 不在预缓存。
- `eslint src`：fork.41 和最终代码均为 96 errors / 3583 warnings；未增加现有告警。
- (a) d41→A 与 (b) A→B 都只有一次自动 reload，toast 各出现一次，下一次访问静默；B 刷新前有更新指示器 DOM 证据。深色 toast 截图已检查。
- (c) 全新 profile 正常访问无提示；fork.41 与 fork.42 登录页可见空闲 60 秒均为 0 个新增请求。
- H5 初次截图发现页底版本会被固定底部导航遮挡，最终放在抽屉顶部「导航」下；admin 移动抽屉页底灰字，桌面 SidebarUpdateButton 原样保留。
- 证据目录：`/root/flvx-workers/runs/version-update-ui/`；最终截图/发布/生产记录继续补充。
- 最终菜单复验通过（390×844）：H5 版本位于顶部，未被底部导航遮挡。1366×900 桌面侧栏 DOM 与 fork.41 完全一致（除版本字符串），截图 `mobile-h5-menu.png` / `mobile-h5-menu-dark.png` / `desktop-sidebar.png`。
- 明/暗进度条与成功 toast 已查看截图，`pulse` 动画有效；`visual-extra.cjs` 为截图在测试环境将原有 CacheStorage 清理步骤延长 1 秒，主 A→B 验证未做此延迟，生产代码没有新增等待。WebView 无 toast，原有 app_version 1.0.3 保留。
- 真实生产旧 profile 已安装 fork.41 SW（登录页，无登录），旧 bundle `index-CP6Ydy6P.js`；见 `prod-seed.json` / `prod-before-fork41.png`。
- 最终源码 `npx tsc --noEmit` 再次通过。发布提交 `1d8666bf`，分支 CI [38135305378](https://github.com/ImoLR/FLVXR2/actions/runs/38135305378) 全绿后推送 annotated tag `3.0.27-fork.42`。
- 镜像/发布工作流 [38135444992](https://github.com/ImoLR/FLVXR2/actions/runs/38135444992) 成功。[Release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.42) 为 Latest、非预发布；10 个资产与 fork.41 同名齐全，两个 compose 镜像标签、两个脚本 PINNED_VERSION/仓库、amd64/arm64 SHA256 和离线 ZIP 完整性均通过。
- 生产回滚点 `/opt/flvx-svc/rollback/pre-fork42-20261011T114457Z/`：compose/.env、SQLite 在线备份（quick_check=ok）、fork.41 两个本地镜像标签、元数据及 rollback.sh。业务计数 node/user/tunnel/forward/forward_port/chain_tunnel = 27/10/53/28/28/181。
- 分块备份因持续指标写入反复重试，已删除本任务未完成的 `pre-fork42-20261011T114314Z` 临时目录，改用 sqlite3 默认整库在线备份成功；没有写入生产 DB。
- 已运行 prune-backups.sh，保留上述新备份与 `pre-tunneltls-20261011T062620Z`；删除旧 `pre-fork41-20261011T062508Z` 及关联 fork.40 镜像/本地标签。详见运行目录 `prune-backups.log`。
- 2026-10-11 11:45:26 UTC 开始升级生产；前后端均为 fork.42，后端 healthy。schema 和上述业务计数均未变；26 个原在线节点均有重启后指标，24 仍原有离线，没有代理 OTA 或业务数据修改。
- 真实生产 E2E 成功：旧 SW 提供 `index-CP6Ydy6P.js` → 一次 reload → `index-DnAESV-M.js`，仅一次「已更新到 v3.0.27-fork.42」，再访问静默。`version.json` 与 localStorage 均为 `3.0.27-fork.42-mv3qnq1t`。截图 `prod-after-fork42-toast.png`，证据 `prod-upgrade.json` / `prod-next-visit.json`。
- 独立 urllib 公网探测曾获 403；浏览器和 Node 实际请求均 200，最终健康检查采用 Node fetch，通过；访问控制配置未改。
- 中文总结及全部回滚/清理说明：`/root/flvx-workers/runs/version-update-ui/summary.md`。常规版本回退执行新备份目录下 `rollback.sh`，无需恢复 DB。
