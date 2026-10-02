# 中国大陆落地地址拦截计划

## 范围

在面板后端识别并拒绝中国大陆（仅 `CN`）落地地址，自动暂停并标记已有或漂移到中国大陆的规则，并在所有前端规则视图中展示原因。本轮只实现、测试、提交和推送分支，不发布、不打标签、不变更生产环境或节点。

## 检查清单

- [x] 记录 `f65e7e3f` 后端全量测试基线及其已知失败集。
- [x] 嵌入 APNIC 中国大陆 IPv4/IPv6 CIDR 数据，提供刷新生成器，并实现含 DNS 注入的快速落地地址校验器及单元测试。
- [x] 增加 forward 拦截标记字段、仓储方法和列表 API 字段，并在创建、更新、恢复及其他写入/重新启用路径统一执行校验。
- [x] 增加启动后及每 10 分钟巡检，正匹配时可靠落库暂停/标记，DNS 瞬时失败不暂停；有效编辑清标并自动恢复，覆盖作业与处理器测试。
- [x] 在创建/编辑表单落地地址字段下展示后端错误，并在桌面/H5、卡片/表格规则视图中展示拦截警告。
- [x] 运行后端全量测试并与基线比较，运行前端生产构建，完成范围审计。
- [x] 推送实现分支并写入中文交接总结（不发布、不打标签、不部署）。

## 测试记录

- 基线（`f65e7e3f`）：`cd go-backend && go test ./... -count=1`；共 19 个已知失败（12 个顶层测试、7 个子测试），分布于 federation 非 TLS metadata、renewal 时区、connectIp 重建/诊断/恢复、federation dual-panel、backup/migration、service monitor；其余包通过。
- 新增定向测试：`go test ./internal/cnlanding ./internal/http/handler ./tests/contract -run 'TestChecker|TestRunCNLanding|TestRedeployTunnelSkipsPausedCNBlockedForward|TestForwardUpdateClearsCNBlockAndResumesAutoPausedRule|TestForwardMainlandLandingRejectionContracts' -count=1`，通过。
- 实现后全量：`cd go-backend && go test ./... -count=1`；第一次出现 1 个额外的端口占用瞬时失败，该用例单独连续运行 5 次全部通过；再次全量运行与基线的 19 个失败名称逐项一致，无新增失败。
- 前端：`cd vite-frontend && npm run build`，通过（3194 个模块完成转换）。
- 范围审计：`git diff --check f65e7e3f..HEAD` 通过；未修改 `go-gost/`、安装脚本、协议过滤、依赖或生产环境，未纳入非本任务的 `plans/048-aws-hk-fork4-agent-canary.md`。

## Rollout（Round 2：3.0.27-fork.10）

- [x] 合并 `maintenance/3.0.27-fork.10-svc-monitor-admin`（计划 056，服务监控仅管理员）到本分支（合并提交 `2dc51519`），范围审计通过，无 node_modules。
- [x] 后端全量测试与 `f65e7e3f` 基线对比：19 个失败名称逐项一致，无新增；定向测试（cnlanding、巡检、合约、服务监控仅管理员）通过；前端 `npm run build` 通过。
- [x] 升级前预览生产 27 条 forward：无 wg_path 规则，按当前 DNS 无一命中中国大陆。
- [x] 打 annotated tag `3.0.27-fork.10` 并推送（`0055663c`）。
- [x] CI Build Check（37016380197、37016360163）与 Build and Push Images（37016383581）通过。
- [x] 校验 release（非 prerelease、Latest、资产与 fork.9 一致、compose 镜像、PINNED_VERSION/REPO、gost sha256）。
- [x] 生产备份/回滚点 `/opt/flvx-svc/rollback/pre-fork10-20261002T140611Z/`（quick_check ok，镜像 tag `local/flvxx-*:pre-fork10-20261002T140611Z`）。
- [x] 升级 `/opt/flvx-svc` 到 fork.10。
- [x] 生产验证：健康、22/22 在线节点 node_metric 新鲜、新列存在、首次巡检 0 条命中（与预览一致）、列表 API 含 `cnBlocked`、非管理员创建服务监控 403 且无数据写入；恢复拒绝在隔离沙箱（fork.10 镜像 + 备份副本，`--network none`）中验证。
- [x] 中文总结报告（`/root/flvx-workers/runs/cn-landing-block-r2/summary.md`）。
