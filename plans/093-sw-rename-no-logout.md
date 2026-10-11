# 093 — SW 改名且保留登录（fork.44）

基于 fork.43 分支头 `b72eb357`，分支 `maintenance/3.0.27-fork.44-sw-rename-no-logout`。严格保留现有更新逻辑、toast/overlay、license/info kill switch；不修改后端、代理、安装脚本、业务数据或 memory。未跟踪计划 048 保持不动。

## 任务清单

- [x] 阅读计划 092、前次总结、项目约束、发布/备份及 Chromium 内存规则，建立分支与本计划。
- [x] 删除 stale reset 的 Clear-Site-Data；生成/注册新 SW 文件，旧 URL 映射同一内容，两者 no-cache，明确排除 precache。
- [x] 提取 fork.11、fork.38 dist 并删除下载镜像；扩展运行目录 harness 支持旧 immutable SW 与固定旧 SW 的 edge 模拟。
- [x] 完成 tsc/build/precache/lint 对照及真实 nginx 验证 E。
- [x] 完成浏览器 A（原生缓存及 edge 模拟）、B、C、D；保留登录、无循环、二次访问、toast/idle 证据。
- [x] 推送分支，CI 全绿后打 fork.44 标签；核验镜像工作流、Latest release 及资产。
- [ ] 生产备份与回滚点，prune 保留两份，升级并验证健康及节点上报。
- [ ] 核验 Cloudflare 实际响应头，观察目标手机 IP 最多 30 分钟并如实记录。
- [ ] 清理临时资源，完成中文总结与最终计划提交、推送。

## 验证与执行记录

证据目录：`/root/flvx-workers/runs/sw-rename-no-logout/`。浏览器串行，启动门槛 MemAvailable ≥750 MiB，systemd scope `MemoryMax=650M MemorySwapMax=0`。不新增仓库前端测试设施；原生 Chromium 未复现的情况与人工 edge 模拟分开报告。

- 实现只改 `vite.config.ts` 与 `nginx.conf`：使用插件 filename 自动改变 main.tsx 既有 registerSW 的生成 URL；旧 `/sw.js` 使用 nginx 精确 location alias 指向同一生成文件，避免复制步骤/顺序和副本预缓存风险。两者同目录，scope 不变。
- 已安装 vite-plugin-pwa 1.3.0 的 `RegisterSWOptions` 与 `dist/client/build/register.js` 未提供 `updateViaCache` 透传，仅传 scope/type 给 Workbox；按要求跳过，不加补丁或替换现有注册流程。

- `extract.log`：fork.11、fork.38 镜像均存在，已提取 dist 后删除下载标签和临时提取容器；fork.43 从当前生产容器只读提取。扩展 `server.cjs` 支持 1 年 immutable 及固定 fork.11 `/sw.js`，日志记录来源/哈希；浏览器脚本使用只在 seed 阶段写入的有效格式测试 token，不会在刷新时补写。

- `E-nginx.json`：fork.43 镜像挂载旧/新 dist 及配置，真实 nginx 对照全部通过。D3F=200/JS/no-store/无 Clear-Site-Data；现存 Cch4wiA5 字节与缓存头不变；SW 两 URL=200/JS/no-cache/字节一致；其他缺失 JS=404；index/version/manifest 头不变；经真实现有后端只读验证 license/info 仍 401 + Clear-Site-Data。两次临时 nginx 容器均已删除。

- `tsc.log`、`build.log`、`build-validation.json` 全通过，生成/注册 `/flvx-sw.js`，四个指定文件均不在 precache。`lint-comparison.json`：基线和最终均 96 errors / 3583 warnings，未退步（eslint 因已有错误退出 1）。首次分支 CI 38153057496 四项成功。

- `A-native.json`：Chromium 153.0.8010.12 在 fork.11 SW 带一年 immutable 后仍请求 origin `/sw.js`（切换 fork.43 阶段 2 次），页面更新至 fork.43，未复现手机缓存永久锁死。随后 fork.44 由 `/flvx-sw.js` 控制，token 不变，2 分钟 0 新请求/0 额外刷新，第二次访问正常。首轮因构建尚未完成而中止，原始记录保留 `A-native-attempt1.json`，完整重跑通过。
- `A-edge.json`：固定返回 fork.11 `/sw.js` 时，完整 precache 页面仍停 fork.11；随后主动删除 D3F 的 CacheStorage 条目并清 HTTP cache 来模拟生产缺失项（仅测试 profile），fork.43 使用当前无 CSD 热修头，reset 后页面到 fork.43。继续固定旧 `/sw.js`，切 fork.44 后仅请求新 SW，token 保留，2 分钟 0 新请求/循环，第二次访问无 D3F。此为明确人工 edge/缺失缓存模拟，不声称本机自然复现手机故障。

- `B-fork38.json` 与 `C-fork43.json`：旧 SW 通过 `/sw.js` alias 获取新内容，随后注册 URL 变为 `/flvx-sw.js`，token 保留，更新 toast 各 1 次，二次访问不重复。fork.43 自然再访问先由 build-freshness 成功恢复，因此没经过 `/sw.js`（`C-natural-visit.json` 的失败是 harness 过严的路径断言，token/新 SW/toast 正常）；C 补测直接触发旧注册的标准 `registration.update()`，兼容路径通过。未改应用更新逻辑。

- `D-fresh.json`：新 profile 首次只注册 `/flvx-sw.js`，无 reset，60 秒空闲 0 新请求。所有浏览器均串行、受 650M/0 swap scope 限制且启动前通过 ≥750 MiB 门槛；`browser-memory-events.txt` 各次 oom/oom_kill 均 0。全部 A–E 和本地静态检查通过后进入发布门禁。

- 发布门禁 CI [38153881353](https://github.com/ImoLR/FLVXR2/actions/runs/38153881353) 四项全绿后，已创建并推送 annotated tag `3.0.27-fork.44`，指向 `0399916d`；镜像工作流 [38154032852](https://github.com/ImoLR/FLVXR2/actions/runs/38154032852) 正在运行。

- 镜像工作流 38154032852 全部必要步骤成功；[fork.44 Release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.44) 为 Latest、非预发布，10 个资产与 fork.43 一致；compose 镜像标签、两份脚本 PINNED_VERSION/仓库、amd64/arm64 SHA256、离线 ZIP 完整性全部通过（`release-validation.json`）。

- 生产备份 `/opt/flvx-svc/rollback/pre-fork44-20261011T161726Z` 已完成 SQLite 在线备份 quick_check=ok、compose/.env、fork.43 本地镜像标签、当前 nginx 无 CSD 热修配置及 rollback.sh。回滚脚本在旧容器启动前复制热修配置，常规回退不覆盖数据库。已执行 prune-backups.sh，仅保留 pre-fork44 与 pre-fork43 两份标准备份；明细见 `backup.json` / `prune-backups.log`。

- 首次部署 helper 沿用的正则误校验 fork.42，在写入 compose 后、拉取/启动 fork.44 前失败；自动 rollback.sh 已重建 fork.43 容器并恢复无 CSD 热修（`deploy-attempt1.log`、`deploy-attempt1-rollback.txt`、`evidence/rollback43-d3f.headers`）。已将 helper 断言修为 fork.43 后重试；未改产品代码，未恢复/覆盖生产数据库。
