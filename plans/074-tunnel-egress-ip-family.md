# 074 — 隧道逐跳连接地址与节点出站 IP 家族

范围：逐跳显式 v4/v6 优先于来源节点能力；节点新增出站 IP 家族；自动选择不再固化；逐节点下拉框；修复 best-exit 自定义地址参数。发布 `3.0.27-fork.26` 并升级 `/opt/flvx-svc`，不修改生产节点/隧道配置，不改代理。

- [x] 阅读规则与发布记忆，从 `39d5e8fb` 创建指定分支，建立运行目录。
- [x] 后端模型/API/选择逻辑/自动保存/相关读取及 best-exit 修复，Go 单元测试通过并提交。
- [x] 节点出站 IP 与逐节点连接类型 UI，TypeScript/build 通过并提交。
- [ ] 生产在线 SQLite 副本逐隧道逐跳对比 fork.25 与新逻辑；记录并解释全部差异。
- [ ] 隔离本地 paneld:16365 验证表单保存、自动空值、IPv4 链配置；1440/390 截图。
- [ ] 后端全量测试与 19 个已知失败基线集合一致；推送分支并确认 CI Build Check 绿色。
- [ ] 注解标签 fork.26，镜像构建成功；校验 Latest/非预发布/资产集/compose/脚本/gost SHA256。
- [ ] 建立生产回滚点，升级 backend/frontend。
- [ ] 生产只读验证健康、指标、新列默认值、API 权限、JS；列出候选节点/隧道。
- [ ] 中文总结写入运行目录，提交 `docs(plan): mark fork26 rollout complete` 并推送。

运行目录：`/root/flvx-workers/runs/tunnel-egress-family/`。临时目录均位于此目录；重型进程串行运行。

## 检查与回归记录

- 后端定向测试通过：22 项拨号矩阵、创建/更新保存自动空值、节点 CRUD/list/控制面与列默认值、best-exit 及诊断目标一致。
- 审核全部 connect_ip_type 读取：列表空值返回 `""`；诊断/质量探测/路径延迟均调用同一选择函数，无需各自改写。远程记录走 repository 的统一 NodeRecord 映射，未设置出站值即自动。
- `connect_ip` 在当前 buildTunnelChainConfig/诊断中不覆盖 host（已有相关基线失败）；本次只修复 best-exit 把 IP 当类型的错误，不引入新的自定义 IP 语义。
- 保留已有自动选择存量值，不做清理/迁移。新节点字段也纳入既有备份导入/导出，避免备份恢复丢失。

## 发布与回滚记录

待执行。

- 前端 `tsc --noEmit` 与 `npm run build` 通过，主 JS 2,751,911 B，距 5 MiB 限制剩 2,490,969 B；未调整依赖或打包策略。
