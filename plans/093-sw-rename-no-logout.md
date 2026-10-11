# 093 — SW 改名且保留登录（fork.44）

基于 fork.43 分支头 `b72eb357`，分支 `maintenance/3.0.27-fork.44-sw-rename-no-logout`。严格保留现有更新逻辑、toast/overlay、license/info kill switch；不修改后端、代理、安装脚本、业务数据或 memory。未跟踪计划 048 保持不动。

## 任务清单

- [x] 阅读计划 092、前次总结、项目约束、发布/备份及 Chromium 内存规则，建立分支与本计划。
- [x] 删除 stale reset 的 Clear-Site-Data；生成/注册新 SW 文件，旧 URL 映射同一内容，两者 no-cache，明确排除 precache。
- [x] 提取 fork.11、fork.38 dist 并删除下载镜像；扩展运行目录 harness 支持旧 immutable SW 与固定旧 SW 的 edge 模拟。
- [ ] 完成 tsc/build/precache/lint 对照及真实 nginx 验证 E。
- [ ] 完成浏览器 A（原生缓存及 edge 模拟）、B、C、D；保留登录、无循环、二次访问、toast/idle 证据。
- [ ] 推送分支，CI 全绿后打 fork.44 标签；核验镜像工作流、Latest release 及资产。
- [ ] 生产备份与回滚点，prune 保留两份，升级并验证健康及节点上报。
- [ ] 核验 Cloudflare 实际响应头，观察目标手机 IP 最多 30 分钟并如实记录。
- [ ] 清理临时资源，完成中文总结与最终计划提交、推送。

## 验证与执行记录

证据目录：`/root/flvx-workers/runs/sw-rename-no-logout/`。浏览器串行，启动门槛 MemAvailable ≥750 MiB，systemd scope `MemoryMax=650M MemorySwapMax=0`。不新增仓库前端测试设施；原生 Chromium 未复现的情况与人工 edge 模拟分开报告。

- 实现只改 `vite.config.ts` 与 `nginx.conf`：使用插件 filename 自动改变 main.tsx 既有 registerSW 的生成 URL；旧 `/sw.js` 使用 nginx 精确 location alias 指向同一生成文件，避免复制步骤/顺序和副本预缓存风险。两者同目录，scope 不变。
- 已安装 vite-plugin-pwa 1.3.0 的 `RegisterSWOptions` 与 `dist/client/build/register.js` 未提供 `updateViaCache` 透传，仅传 scope/type 给 Workbox；按要求跳过，不加补丁或替换现有注册流程。

- `extract.log`：fork.11、fork.38 镜像均存在，已提取 dist 后删除下载标签和临时提取容器；fork.43 从当前生产容器只读提取。扩展 `server.cjs` 支持 1 年 immutable 及固定 fork.11 `/sw.js`，日志记录来源/哈希；浏览器脚本使用只在 seed 阶段写入的有效格式测试 token，不会在刷新时补写。

- `E-nginx.json`：fork.43 镜像挂载旧/新 dist 及配置，真实 nginx 对照全部通过。D3F=200/JS/no-store/无 Clear-Site-Data；现存 Cch4wiA5 字节与缓存头不变；SW 两 URL=200/JS/no-cache/字节一致；其他缺失 JS=404；index/version/manifest 头不变；经真实现有后端只读验证 license/info 仍 401 + Clear-Site-Data。两次临时 nginx 容器均已删除。
