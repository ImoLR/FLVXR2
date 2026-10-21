# 120 — 发布 fork.64：整机连接数 + 整机连接保护（plan 118）

用户 2026-10-21 看过两组截图后批准发布（「可以 你就發布吧」）。时间统一台湾时间（UTC+8）。
从 fork.63 分支头 `ec7837f5`（= tag `3.0.27-fork.63` 4fa412e9 + plan 119 完成文档）建
`maintenance/3.0.27-fork.64-conn-protect`，按序 cherry-pick `feature/3.0.27-conn-protect` 的 14 个提交
（b2cfa5bf..6a376462），保留 fork.63 监控入口页与共享落地时效提示的行为。不加新功能。
运行目录：`/root/flvx-workers/runs/release-fork64-conn-protect/`。

## 检查清单

- [x] 阅读发布流程记忆、plan 118 总结与契约，确认工作树、分支与授权。
- [x] 合入 14 个提交并解决冲突（两边行为都保留）；推送发布分支。
- [ ] 干净 fork.63 临时工作树取后端完整测试基线；发布代码后端全量测试按名称对比无新增失败；go vet 同基线。
- [ ] go-gost/x 全部测试通过，新包 `machineprotect` 加 -race 通过。
- [ ] 前端 tsc、eslint 无新增错误、`npm run build` 每个文件 < 5 MiB。
- [ ] `scripts/landing-acceptance/` 本机验收通过。
- [ ] 重拍 3 张关键页面（节点编辑「连接保护」、监控列表芯片含 fork.63 入口页、落地共享卡片）。
- [ ] Tag `3.0.27-fork.64`；CI / Images 通过；Latest 资产集合与 fork.63 相同。
- [ ] 生产升级前：回滚目录 `pre-fork64-<TS>`（验证过的 DB 备份、compose/.env、镜像 tag、rollback.sh）、
      prune 保留 2 份；记录在线节点数、表计数/哈希、面板 RSS。
- [ ] 升级生产面板；核对 node 新列默认值（开 / 1000 / 500 / 10）、在线节点数不变、日志无错误激增、
      规则/隧道/落地/计费不变。
- [ ] OTA 前逐台记录 1 天 `tcp_conns` 峰值与（有 SSH 时）整机实时总数；≥ 700 的节点先在面板把上限调到 1500。
- [ ] 金丝雀：一台纯 FLVX 节点，SSH 备份 + 恢复脚本，OTA，检查归属 / nft 表 / sysctl / 芯片 / 诊断 /
      CPU·RSS / 单循环 / business-ports.json，观察 15 分钟。
- [ ] 与妙妙屋共机的节点（21/31/35/45/36 及按 IP 找到的其它）：OTA 后显示「由妙妙屋负责」或「妙妙屋助手版本过旧」，
      无 FLVX nft 表，sysctl 未动。
- [ ] 其余节点逐台 OTA，前后诊断；14 / 20 各试一次；离线节点跳过并记录。
- [ ] 清理临时环境；120 完成；提交推送；中文总结写入运行目录 `summary.md`。

## 证据

- 2026-10-21 07:4x：14 个提交按序 `cherry-pick -x`，无 merge commit。唯一冲突
  `vite-frontend/src/pages/landing/monitor-view.tsx`（共享服务器卡片头）：保留 conn-protect 的
  `MachineConnChip` 与 fork.63 的 `AgeHint ageHintMs`。核对：`git diff fork.62..fork.63分支头` 与
  `git diff feature头..发布头` 只差冲突处上下文行和行号偏移，即合并结果 = 两边改动之和。go-backend / go-gost /
  go-gost/x `go build ./...` 通过。
