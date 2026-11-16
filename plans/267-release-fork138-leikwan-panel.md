# 267 — 发布 plan 266（利群专线入面板 第一步）为 3.0.27-fork.138，只升面板

用户 11-17 ~00:00 台湾：「没什么要改的，直接发布吧，放上去我跑一下看看」→ 按已构建版本发布，不改界面。
不 OTA agent（go-gost 与 fork.137 一行不差）。不在生产上应用导入、不建利群隧道/授权/规则、不写利群 API。

## 检查清单

- [x] 磁盘：go clean -cache、npm cache；flvxx-leikwan-port-skip worktree 已不在。
- [x] 分支 maintenance/3.0.27-fork.138-leikwan-panel（fork.137 + merge origin/feature/3.0.27-leikwan-panel @ 823c8723），无冲突，不碰 go-gost。
- [x] go build / go vet / go test ./...（SQLite）；PG 18 repo/handler/ws/contract + 利群子测试；npm run build。
- [ ] 推送、tag 3.0.27-fork.138、CI + Images 绿、Latest 10 个文件。
- [ ] 生产库副本上验证：fork.138 建 5 张新表后，fork.137 照常启动、忽略新表（回滚声明）。
- [ ] 备份 + rollback.sh（pre-fork138），换镜像，健康检查；prune 保留 2 份。
- [ ] 通知 warm-redundant-ota 简报：面板现为 fork.138（若在 01:40 前完成）。
- [ ] 上线后：节点在线数、规则数不变、无离线抖动、日志 15 分钟干净、4 台利群节点运行中、利群汇聚页可打开。
- [ ] 导入「预览」只读核对（263/540/279 → 广港，264 → 沪日，280/281 → 广台，282 → 福台，318 跳过，备线 148/619/601/595/587/599）。
- [ ] 账本 266 → 已发布；memory fork-release-and-deploy、leikwan-panel-step1；总结。

## 记录
- 测试（00:01–00:20 台湾）：go build、go vet 0；`go test ./...`（SQLite）只有 TestForwardWarmTagContract 失败 1 次——
  这个测试（plan 249，本次没改）最后一步 `waitAdmin("")` 不等待，负载大时有竞态；fork.137 原版和本分支单独各跑 3 次都通过，
  PG 18 上 contract 整包也通过 → 原有的偶发失败，与 266 无关。PG 18：repo / handler / ws / contract 全部 ok；
  利群子测试 10/10（含 TestLeiKwanLinePanelContract sqlite + postgres）。`npm run build` 通过（借 flvxx-leikwan-panel 的 node_modules，package.json 相同）。
