# 092 — 旧 SW 缺失 workbox-window chunk 恢复（fork.43）

基于 fork.42 分支头 `69f83503`，分支 `maintenance/3.0.27-fork.43-stale-sw-chunk-reset`。仅修改 nginx 的旧 workbox-window 缺失文件处理、静态 reset ES module 和对应 globIgnores；不修改现有更新逻辑、后端、代理、安装脚本或业务数据。保留未跟踪的计划 048。

## 任务清单

- [x] 阅读项目约束、发布/备份和 Chromium 内存规则，建立分支及计划。
- [x] 提取 fork.16、fork.24、fork.42 dist，确认旧动态导入形状；移除临时旧镜像。
- [ ] 持久 Chromium profile 复现旧 SW 缺失 chunk 卡死（30 秒及第二次访问），保存差异和证据。
- [ ] 实现缺失 workbox-window chunk 的 no-store / Clear-Site-Data reset 与 10 分钟 sessionStorage guard。
- [ ] 验证真实 nginx 响应、现存 chunk/其他资源不变，tsc/build/precache/lint 不退步。
- [ ] 验证同一卡死 profile 恢复、两分钟不循环、第二次访问正常；fork.42 更新 toast 与全新 profile 60 秒空闲回归。
- [ ] 推送分支，CI 全绿后创建 fork.43 tag；镜像工作流、Latest release 和资产核验。
- [ ] 生产在线备份/回滚点，prune 保留最新两份；升级、健康和节点上报验证。
- [ ] Cloudflare 响应核验，观察指定手机 IP 最多 30 分钟。
- [ ] 清理临时资源，完成中文总结与最终计划提交。

## 验证边界

- 证据与临时 harness 仅存 `/root/flvx-workers/runs/stale-sw-chunk-reset/`，不新增仓库前端测试设施。
- Chromium 串行，启动前 MemAvailable ≥750 MiB，systemd scope `MemoryMax=650M MemorySwapMax=0`。
- 缺失旧 chunk 返回 `Clear-Site-Data: "storage"`，受影响旧客户端会退出登录一次，此行为已接受。
- 每完成一个步骤即更新清单并提交；发布/生产记录随完成补充。

## 执行记录

- 已提取 fork.16/24 dist 并删除两份下载的镜像和临时容器；fork.42 dist 从当前生产容器只读提取。`import-shape.txt` 确认旧构建仅命名导出 `Workbox`，`register()` 必须返回 Promise，其余调用为 addEventListener/messageSkipWaiting。
