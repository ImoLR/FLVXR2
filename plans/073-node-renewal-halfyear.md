# Plan 073 — 半年节点续费兼容与 fork.25 发布

范围：保留数据库规范值 `halfyear`，不迁移或修改生产数据；不改变 7 天提醒窗口、卡片设计、treeshake、依赖或代理版本。

- [x] 阅读发布/自主工作流程和目录规则，从 `2a9b3924` 建立 `maintenance/3.0.27-fork.25-renewal-halfyear`。
- [x] 修复所有不匹配的续费周期读取点并增加 Go 回归测试。
- [x] 仅开启 minify，更新两份 AGENTS 构建说明，记录主 JS 大小。
- [x] 完成 Go 相关包、全量基线对比、TypeScript 与生产构建验证。
- [x] 生产数据库在线备份到运行目录；本地副本验证半年提醒、节点显示、编辑预选及保存不变；桌面/手机检查四页并截图。
- [x] 推送分支，CI Build Check 通过；创建注解标签 fork.25，验证镜像构建及全部 release 资产。
- [x] 建立生产回滚点，安装 v6 compose，升级 backend/frontend。
- [ ] 生产只读 API、指标、资源及桌面/手机浏览器验证。
- [ ] 写中文总结，提交 `docs(plan): mark fork25 rollout complete` 并推送。

## 全仓大小写检索结果

- 前端 `pages/node/renewal.ts`：snapshot、标签、月份换算统一规范化。
- 前端 `pages/dashboard/use-dashboard-data.ts`：补全半年类型及共享规范化。
- 前端 `pages/node.tsx`：编辑表单加载规范化，避免已存 `halfyear` 无选项。
- 前端 `api/types.ts`：接口类型接受数据库返回的 `halfyear` 和旧 `halfYear`。
- 后端 `repository_mutations.go`：RefreshNodeExpiryReminder 的半年分支错误；AdvanceNodeRenewalCycles 也需大小写兼容。
- 后端 `repository.go`：ListNodesWithTrafficResetDue 的 SQL 读取需大小写兼容。
- 后端 `handler/mutations.go`：已 lower-case 写入 `halfyear`，无需修改。
- 代理 `go-gost/x/traffic/cycle.go`：已接受两种拼法，无需修改。

## 验证与发布记录

待执行。运行产物：`/root/flvx-workers/runs/renewal-halfyear/`。

- 新增 `TestNodeHalfYearRenewalReaders`：三种大小写的手动提醒推进 6 个月、自动推进 6 个月、流量重置候选查询及保存周期不变均通过。

- Go repository 包通过；全量后端失败集合与 `/root/flvx-fork10/base.fails` 完全一致（19/19，无新增/缺失）。`npx tsc --noEmit`、`npm run build` 通过。
- 实测 fork.24 线上主 JS 5,242,299 B（余 581 B）；本地 minify 构建 2,752,478 B（余 2,490,402 B），treeshake 不变。

- 本地数据库在线副本 quick_check=ok；隔离网络 namespace，paneld 仅 127.0.0.1:16365。节点 31 副本设置 3 天内到期；1440/390 下提醒显示半年付，节点信息正常，编辑预选半年，UI 保存后 DB 仍 halfyear。主页/规则/隧道/节点 8 次加载均无 console/page errors、空白页或拦截请求；截图见运行目录 screens。测试代理补齐只读路由及 WebSocket 后重跑通过，未修改应用来绕过检查。

- CI Build Check [37643873263](https://github.com/ImoLR/FLVXR2/actions/runs/37643873263) 四项通过；注解标签 `3.0.27-fork.25` 指向 `b37b72fe`。Images [37644121003](https://github.com/ImoLR/FLVXR2/actions/runs/37644121003) 成功；Release 2026-10-07 15:38:37 UTC 发布，Latest、非预发布，10 个资产与 fork.24 同名，全部 digest、两个安装脚本 PINNED_VERSION/REPO、v4/v6 compose 镜像引用、两架构 gost SHA256 及 offline zip 内二进制均通过；远端两镜像均有 amd64/arm64。

- 生产回滚点 `/opt/flvx-svc/rollback/pre-fork25-20261007T153231Z`：compose/.env、online SQLite 366,944,256 B（quick_check=ok，62 表 counts）、旧镜像 local tags、ROLLBACK-METADATA.md 全部保留。已安装验证后的 v6 compose，FLUX_VERSION=fork.25，pull/up backend/frontend 成功，backend healthy。未执行生产 DB 修改、迁移或代理升级。
