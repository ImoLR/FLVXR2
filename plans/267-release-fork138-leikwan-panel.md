# 267 — 发布 plan 266（利群专线入面板 第一步）为 3.0.27-fork.138，只升面板

用户 11-17 ~00:00 台湾：「没什么要改的，直接发布吧，放上去我跑一下看看」→ 按已构建版本发布，不改界面。
不 OTA agent（go-gost 与 fork.137 一行不差）。不在生产上应用导入、不建利群隧道/授权/规则、不写利群 API。

## 检查清单

- [x] 磁盘：go clean -cache、npm cache；flvxx-leikwan-port-skip worktree 已不在。
- [x] 分支 maintenance/3.0.27-fork.138-leikwan-panel（fork.137 + merge origin/feature/3.0.27-leikwan-panel @ 823c8723），无冲突，不碰 go-gost。
- [x] go build / go vet / go test ./...（SQLite）；PG 18 repo/handler/ws/contract + 利群子测试；npm run build。
- [x] 推送、tag 3.0.27-fork.138、CI + Images 绿、Latest 10 个文件。
- [x] 生产库副本上验证：fork.138 建 5 张新表后，fork.137 照常启动、忽略新表（回滚声明）。
- [x] 备份 + rollback.sh（pre-fork138），换镜像，健康检查；prune 保留 2 份。
- [x] 通知 warm-redundant-ota 简报：面板现为 fork.138（若在 01:40 前完成）。
- [x] 上线后：节点在线数、规则数不变、无离线抖动、日志 15 分钟干净、4 台利群节点运行中、利群汇聚页可打开。
- [x] 导入「预览」只读核对（263/540/279 → 广港，264 → 沪日，280/281 → 广台，282 → 福台，318 跳过，备线 148/619/601/595/587/599）。
- [x] 账本 266 → 已发布；memory fork-release-and-deploy、leikwan-panel-step1；总结。

## 记录
- 测试（00:01–00:20 台湾）：go build、go vet 0；`go test ./...`（SQLite）只有 TestForwardWarmTagContract 失败 1 次——
  这个测试（plan 249，本次没改）最后一步 `waitAdmin("")` 不等待，负载大时有竞态；fork.137 原版和本分支单独各跑 3 次都通过，
  PG 18 上 contract 整包也通过 → 原有的偶发失败，与 266 无关。PG 18：repo / handler / ws / contract 全部 ok；
  利群子测试 10/10（含 TestLeiKwanLinePanelContract sqlite + postgres）。`npm run build` 通过（借 flvxx-leikwan-panel 的 node_modules，package.json 相同）。
- 发布：tag 3.0.27-fork.138 → 10dbcd0a；CI 43905171707 绿；Images 43905189886 绿；Latest = fork.138，10 个文件，compose/install 版本、gost sha256、`gost -V` 都对。
- 回滚声明验证（生产库在线副本，利群 token 已换掉，容器 --network none）：fork.138 建出 5 张新表、规则 51/51 不变；
  随后 fork.137 镜像在同一文件上 6 秒内健康，日志无错误，quick_check ok。副本已删。
- 备份 /opt/flvx-svc/rollback/pre-fork138-20261116T162558Z（quick_check / integrity_check ok，本地镜像标签，rollback.sh）；
  prune 后保留 pre-fork137 + pre-fork138（删了 pre-fork136 和 fork.135 镜像）。
- 换面板 00:26:04–00:26:11 台湾，健康，版本 3.0.27-fork.138-mwjgu3jk；30/30 节点重连，diff 0 更新 / 0 失败；51 条规则全运行；
  4 台利群节点（火山广州 / 百度云广州 / 阿里广州 / 火山上海）运行中；新表都在。
- warm-redundant-ota 简报已追加：今晚 OTA 发的是 fork.138 agent（go-gost 与 fork.136/137 零差异）。
- 生产上唯一的数据写入：leikwan_host_state 4 行（11-16 21:22 只读 SSH 快照，已去密钥；上线前重新只读核对 4 台的
  LOGSWITCH_LINES 与快照一致）。没有它，预览挂不上备线。撤销：删这 4 行即可（不影响任何转发）。
- 导入预览（API + 页面，只读）：263/540/279 → 利群·广港，264 → 利群·沪日，280/281 → 利群·广台，282 → 利群·福台，318 跳过（管道），
  备线 148/595/601/587(280+282 共用)/599/619；分组「利群入口」4 台将加入；管理员授权将新建（主入口 百度云广州，备线开）；名额 8/8。
  **没有点确认导入**：leikwan_line/grant/rule/setting 都是 0 行，没有新建节点分组。
- 页面冒烟（无头 Chromium，管理员，所有写接口拦截）：桌面 + H5 打开利群汇聚页、导入为规则 → 预览，JS 错误 0，被拦截的写请求 0。
- 日志：唯一错误是 Boil Hinet 马年 的 Reality 每日测速失败，升级前备份里已有同样的错误（原有问题，相关代码没改）。
- 00:42 复查（换面板后 16 分钟）：30/30 在线，节点状态和 51 条规则与升级前逐条一致，无离线、无 4xx/5xx、0 次重启；后端内存 34 MiB（升级前 60 MiB）。
- 账本 266 → 已发布（fork.138）；memory fork-release-and-deploy、leikwan-panel-step1 已更新；总结 /root/flvx-workers/runs/release-fork138/summary.md。
