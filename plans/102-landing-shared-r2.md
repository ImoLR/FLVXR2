# landing-shared r2：异步删除与主机报告 DNS 容错

基线：`7957db66`，分支 `feature/3.0.27-landing-nodes`。仅修复 plan 101 第二期 B 的两个审查缺陷；不发布、不打 tag、不部署、不访问真实 Custom，不修改其他栈或只读 Custom 仓库。

- [x] 阅读上一轮总结、项目规则、流程记忆及相关代码，核对工作树与基线。
- [x] 修复异步删除：首次接受返回 pending，后台复用确认条件收尾，保持最终计费/端口安全与 revision 幂等；增加 JSON 状态和「删除中」徽标，隐藏不允许的编辑入口；SQLite/PostgreSQL 回归并提交。
- [x] 修复周期主机报告：DNS 失败保留小写域名及缓存地址，继续报告/轮询/推送，不触发通信退避；原生启用严格核验不变；SQLite/PostgreSQL 回归并提交。
- [x] 串行运行全部落地测试、带 PostgreSQL 的完整后端测试（精确对比原 15 项失败）、后端及前端构建。
- [ ] 最终静态 dist + 本地合成数据采集桌面/H5「删除中」截图与 index.html。
- [ ] 补充 plan 101 核验记录、清理专用临时资源、提交并仅推送指定 feature 分支，确认 CI 通过，写中文总结。

资源约束：重型作业串行；GOMAXPROCS=2、go test -p 1、NODE_OPTIONS=--max-old-space-size=768；临时文件只在 `/root/tmp-work/landing-shared-r2/`；回环 PostgreSQL 专用容器结束后删除；单 Chromium 且启动前 MemAvailable ≥ 900 MiB。证据保存在 `/root/flvx-workers/runs/landing-shared-r2/`。

删除定向核验：SQLite/PostgreSQL 的后台 tick、异步单次删除、重复请求 revision 不变、最终计费一次、端口保留/释放、暂停及撤销语义、未应用节点删除通过；证据 `landing-shared-r2/delete-tests.log`。

DNS 定向核验：SQLite/PostgreSQL 的 `.invalid` 域名、其他域名/IP、原生报告集合、缓存地址保留、同 tick 两台共享服务器轮询/推送、无退避/同步错误及原生严格核验通过（`dns-tests.log`）。周期报告选择静默容错，不新增日志或 UI 警告；只有 Custom 请求失败仍走既有通信错误路径。

完整核验：全部落地定向测试 41 项通过，另补跑真实 Core 的 SS2022/VLESS 离线配置校验通过。完整后端测试含 PG 恰为原 15 项失败，新增/消失均 0；后端构建、前端 TypeScript/Vite/PWA 构建通过（2955.28 KiB）。代码提交 `1671b1e0` CI https://github.com/ImoLR/FLVXR2/actions/runs/39129159090 四项通过。
