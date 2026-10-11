# 093 — SW 改名且保留登录（fork.44）

基于 fork.43 分支头 `b72eb357`，分支 `maintenance/3.0.27-fork.44-sw-rename-no-logout`。严格保留现有更新逻辑、toast/overlay、license/info kill switch；不修改后端、代理、安装脚本、业务数据或 memory。未跟踪计划 048 保持不动。

## 任务清单

- [x] 阅读计划 092、前次总结、项目约束、发布/备份及 Chromium 内存规则，建立分支与本计划。
- [ ] 删除 stale reset 的 Clear-Site-Data；生成/注册新 SW 文件，旧 URL 映射同一内容，两者 no-cache，明确排除 precache。
- [ ] 提取 fork.11、fork.38 dist 并删除下载镜像；扩展运行目录 harness 支持旧 immutable SW 与固定旧 SW 的 edge 模拟。
- [ ] 完成 tsc/build/precache/lint 对照及真实 nginx 验证 E。
- [ ] 完成浏览器 A（原生缓存及 edge 模拟）、B、C、D；保留登录、无循环、二次访问、toast/idle 证据。
- [ ] 推送分支，CI 全绿后打 fork.44 标签；核验镜像工作流、Latest release 及资产。
- [ ] 生产备份与回滚点，prune 保留两份，升级并验证健康及节点上报。
- [ ] 核验 Cloudflare 实际响应头，观察目标手机 IP 最多 30 分钟并如实记录。
- [ ] 清理临时资源，完成中文总结与最终计划提交、推送。

## 验证与执行记录

证据目录：`/root/flvx-workers/runs/sw-rename-no-logout/`。浏览器串行，启动门槛 MemAvailable ≥750 MiB，systemd scope `MemoryMax=650M MemorySwapMax=0`。不新增仓库前端测试设施；原生 Chromium 未复现的情况与人工 edge 模拟分开报告。
