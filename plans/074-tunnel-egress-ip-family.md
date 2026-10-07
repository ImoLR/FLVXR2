# 074 — 隧道逐跳连接地址与节点出站 IP 家族

范围：逐跳显式 v4/v6 优先于来源节点能力；节点新增出站 IP 家族；自动选择不再固化；逐节点下拉框；修复 best-exit 自定义地址参数。发布 `3.0.27-fork.26` 并升级 `/opt/flvx-svc`，不修改生产节点/隧道配置，不改代理。

- [x] 阅读规则与发布记忆，从 `39d5e8fb` 创建指定分支，建立运行目录。
- [x] 后端模型/API/选择逻辑/自动保存/相关读取及 best-exit 修复，Go 单元测试通过并提交。
- [x] 节点出站 IP 与逐节点连接类型 UI，TypeScript/build 通过并提交。
- [x] 生产在线 SQLite 副本逐隧道逐跳对比 fork.25 与新逻辑；记录并解释全部差异。
- [x] 隔离本地 paneld:16365 验证表单保存、自动空值、IPv4 链配置；1440/390 截图。
- [x] 后端全量测试与 19 个已知失败基线集合一致；推送分支并确认 CI Build Check 绿色。
- [x] 注解标签 fork.26，镜像构建成功；校验 Latest/非预发布/资产集/compose/脚本/gost SHA256。
- [x] 建立生产回滚点，升级 backend/frontend。
- [x] 生产只读验证健康、指标、新列默认值、API 权限、JS；列出候选节点/隧道。
- [ ] 中文总结写入运行目录，提交 `docs(plan): mark fork26 rollout complete` 并推送。

运行目录：`/root/flvx-workers/runs/tunnel-egress-family/`。临时目录均位于此目录；重型进程串行运行。

## 检查与回归记录

- 后端定向测试通过：22 项拨号矩阵、创建/更新保存自动空值、节点 CRUD/list/控制面与列默认值、best-exit 及诊断目标一致。
- 审核全部 connect_ip_type 读取：列表空值返回 `""`；诊断/质量探测/路径延迟均调用同一选择函数，无需各自改写。远程记录走 repository 的统一 NodeRecord 映射，未设置出站值即自动。
- `connect_ip` 在当前 buildTunnelChainConfig/诊断中不覆盖 host（已有相关基线失败）；本次只修复 best-exit 把 IP 当类型的错误，不引入新的自定义 IP 语义。
- 保留已有自动选择存量值，不做清理/迁移。新节点字段也纳入既有备份导入/导出，避免备份恢复丢失。

## 发布与回滚记录

详见以下记录及运行目录 backup.json、release/verification.json、prod-after.json。

- 前端 `tsc --noEmit` 与 `npm run build` 通过，主 JS 2,751,911 B，距 5 MiB 限制剩 2,490,969 B；未调整依赖或打包策略。

- 生产副本 2026-10-07 16:03 UTC：quick_check=ok，28 节点、51 隧道、166 chain_tunnel 行；使用 fork.25 与当前源码提取的 Go 函数比较全部 138 个相邻跳节点组合，差异 **0**（old→new 表为空），意外差异 **0**。运行目录 gate-input.json / gate-result.json / dial_gate.go 可复核。
- 后端全量测试失败集合与已知基线完全一致：19/19，无新增、无缺失；原有 connect_ip 相关失败保留。

- 本地隔离 namespace + SQLite 副本，paneld 仅 127.0.0.1:16365，合成节点 901–904 与隧道 76；模拟节点记录真实 AES WebSocket AddChains。所有构建、Go 测试、paneld、Chromium 串行运行。
- 浏览器阶段使用本地 API 的真实响应快照，生成原始保存 payload；关闭 Chromium 后通过本地 API 执行，数据库与 API 重读确认：出站 dual、自动空类型、各节点独立 v4/v6 均正确。来源 901 仅 v6 入口且 egress=''，显式 v4 下发 `192.0.2.2:20502`。详见 local-api-checks.json。
- 1440/390 节点表单、中继表单、多出口表单共 6 张首轮截图：screens/seed-*.png；3 个逐节点下拉框，未标记出站提示不阻止保存，无页面错误、遗漏请求或横向溢出。后续浏览器曾因可用内存低于 900 MiB 被守卫阻止启动；待内存恢复后，真实保存结果在 1440/390 重新加载全部通过，新增 screens/save-*.png 6 张，无错误。
- CI Build Check [37649719753](https://github.com/ImoLR/FLVXR2/actions/runs/37649719753) 四项成功（后端、前端、PG 契约、代理构建）。

- 标签前最新提交 CI [37650449769](https://github.com/ImoLR/FLVXR2/actions/runs/37650449769) 四项成功；注解标签 `3.0.27-fork.26` 指向 `cb67b884`，构建发布 run [37650781150](https://github.com/ImoLR/FLVXR2/actions/runs/37650781150)。

- 额外核对 best-exit 调用修复影响：生产副本 `strategy=best` 为 0 行、自定义 `connect_ip` 为 0 行，当前运行链路不受该调用修正影响。

- Release 2026-10-07T16:34:00Z 发布，Latest、非预发布，10 资产与 fork.25 同名，全部 SHA256/digest、两份 compose 镜像、安装脚本 PINNED_VERSION/REPO、两架构 gost 及 offline zip 内代理二进制校验通过。GHCR backend/frontend 均有 amd64/arm64。

- 生产回滚点 `/opt/flvx-svc/rollback/pre-fork26-20261007T161704Z`：compose/.env、366,944,256 B 在线数据库（quick_check=ok、62 表 counts）、fork.25 本地镜像标签及 ROLLBACK-METADATA.md。安装已验证 v6 compose，仅改两个镜像版本和 FLUX_VERSION；pull/up backend/frontend 完成，backend healthy。

- 生产 backend/frontend 于 2026-10-07 16:36:17/22 UTC 启动 fork.26；backend healthy，前端 HTTP/资源正常；25 节点指标推进且晚于重启时间。管理员节点/隧道列表 28/51 条，非管理员节点列表 HTTP 200/code=403/data=null，未泄露新字段。
- 新列 `egress_ip_family varchar(10) NOT NULL DEFAULT ''` 存在，28/28 节点均为空；节点地址及隧道/chain_tunnel 记录未改变。初次比较把随遥测更新的 node.updated_time 当作静态配置而报差异；逐字段核实仅该时间变化，排除运行时字段后全部一致，证据 prod-node-diff.json。
- 线上主 JS `/assets/index-B2RxiZAI.js` 为 2,754,644 B，包含 fork.26 与新的出站/逐跳选择器文案，仍低于 PWA 5 MiB。
- 生产候选仅 IPv6 入口节点：48「Leikwanhost 沪台IPLC」、50「vmsilo 沪港」；其作为双栈下一跳前驱的隧道组合为 0。未更改这两节点设置，未调用生产隧道保存/重新部署接口。服务重启时原有自动重连配置下发正常完成。
