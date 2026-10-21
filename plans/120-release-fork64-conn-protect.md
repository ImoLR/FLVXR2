# 120 — 发布 fork.64：整机连接数 + 整机连接保护（plan 118）

用户 2026-10-21 看过两组截图后批准发布（「可以 你就發布吧」）。时间统一台湾时间（UTC+8）。
从 fork.63 分支头 `ec7837f5`（= tag `3.0.27-fork.63` 4fa412e9 + plan 119 完成文档）建
`maintenance/3.0.27-fork.64-conn-protect`，按序 cherry-pick `feature/3.0.27-conn-protect` 的 14 个提交
（b2cfa5bf..6a376462），保留 fork.63 监控入口页与共享落地时效提示的行为。不加新功能。
运行目录：`/root/flvx-workers/runs/release-fork64-conn-protect/`。

## 检查清单

- [x] 阅读发布流程记忆、plan 118 总结与契约，确认工作树、分支与授权。
- [x] 合入 14 个提交并解决冲突（两边行为都保留）；推送发布分支。
- [x] 干净 fork.63 临时工作树取后端完整测试基线；发布代码后端全量测试按名称对比无新增失败；go vet 同基线。
- [x] go-gost/x 全部测试通过，新包 `machineprotect` 加 -race 通过。
- [x] 前端 tsc、eslint 无新增错误、`npm run build` 每个文件 < 5 MiB。
- [x] `scripts/landing-acceptance/` 本机验收通过。
- [x] 重拍 3 张关键页面（节点编辑「连接保护」、监控列表芯片含 fork.63 入口页、落地共享卡片）。
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

- 2026-10-21 14:3x：14 个提交按序 `cherry-pick -x`，无 merge commit。唯一冲突
  `vite-frontend/src/pages/landing/monitor-view.tsx`（共享服务器卡片头）：保留 conn-protect 的
  `MachineConnChip` 与 fork.63 的 `AgeHint ageHintMs`。核对：`git diff fork.62..fork.63分支头` 与
  `git diff feature头..发布头` 只差冲突处上下文行和行号偏移，即合并结果 = 两边改动之和。go-backend / go-gost /
  go-gost/x `go build ./...` 通过。
- 后端（14:37–14:50）：fork.63 完整导出（`git archive 3.0.27-fork.63`，临时目录，不注册 worktree）与发布分支同参数
  `GOMAXPROCS=2 go test -json -count=1 -p 1 ./...`：失败名称 15 = 15，新增 0、消失 0（即 fork.62 起的已知 15 个）；
  `go vet ./...` 输出逐字相同（只有原有 leikwan/testapi 警告）。plan 118 新测试（迁移默认值、只推给有能力的 agent、
  管理员可见性、200 节点内存、共享 machine 透传等）全部通过。注：第一次基线只导出了 go-backend，读仓库根目录文件的
  4 个测试因此假失败，已改为完整导出重跑。
- agent：发布分支 go-gost 与 feature 头 6a376462 逐字节相同（`git diff` 0 行）。go-gost/x `go test -p 1 ./...` 通过；
  `-race` machineprotect 与 socket 保护相关测试通过；netns 真实 nft 端到端 5 项通过（UntrackedFlows 按设计需要干净
  命名空间，单独运行通过：已有 TCP/UDP 不受影响、新连接被拒）。
- 前端：`npm run build`（含 tsc）通过，主包 2,886,593 B（< 5 MiB）；改动的 8 个文件 eslint 与 fork.63 逐文件相同
  （errors 14 / warnings 325 → 14 / 325，新文件 machine-conn.tsx 0/0）。
- 本机落地验收（paneld + agent 3.0.27-fork.64 + 固定 Core v1.4.59 sha ae4b83f0）：S1–S8、E1–E3、T1、R1 共 13 项全部
  PASS（122 条检查）。第一次运行因下载的 Core 文件没有执行权限（0644）在 setup 失败，属本机环境问题，加执行权限后重跑
  通过。实验室 agent 会往宿主机写 `/run/flvx/business-ports.json`（/run 不随 netns 隔离），已删除。
- 截图（运行目录 `screens/`）：本地演示面板 + 模拟 agent（真实 WS 协议）+ 模拟 Custom，只在 127.0.0.1：
  `1-node-edit-conn-protect`（连接保护默认 开/1000/500/10）、`2-monitor-node-list-chips`（旧记住页签=落地仍打开
  「节点」= fork.63 行为；芯片红/黄/灰/-）、`3-landing-shared-card`（「整机连接 846/1000」，马年 `-`）；0 页面错误。
- CI Build Check（分支 b09b7442）：run 39556567545 success。

