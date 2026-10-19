# 110 landing-share-fix：共享落地测试中发现的 4 个问题

基线：tag `3.0.27-fork.56`（3cf457eb），分支 `maintenance/3.0.27-fork.57-landing-share-fix`。
用户 2026-10-19 ~12:40Z 要求「先把这几个 bug 修复了然后上线」：本轮含发布 fork.57 与生产面板升级（仅面板，无 agent 变更，不 OTA）。

## 根因

1. **给用户分配落地服务器无效**：非管理员可见性只看 `landing_server_user_group ⨝ user_group_user`；用户编辑「落地」里的「添加服务器限额」只写 `user_server_quota`，不是授权。服务器 3 无用户组、用户 26 不在任何组 → 看不到。
2. **管理员建节点不能选所属用户**：后端已接受 `userId`，前端创建对话框没有该字段 → 节点 10 记在管理员名下。
3. **共享节点一直「等待同步」且无原因；同步报错**：妙妙屋服务器 5 的 Helper v0.7.4 没有 FLVX 运行时，core `stopped`、`collected_at 0`、`applied_revision 0`。FLVX 只显示「等待同步」。同步报错：妙妙屋端不记录 API 请求、FLVX 端只记 HTTP 200，原始提示已无从取回；代码中可产生的提示均不具体——409/422 统一成「暂未接受配置（共享状态、地址解析或版本冲突）」/「拒绝节点配置」（主机清单 422 也显示成节点错误），退避期间真实原因被「同步异常，正在退避重试」覆盖并写入服务器/节点；管理员/用户操作（建/改/删节点、保存用户落地设置）触发的推送仍受退避限制。
4. **共享服务器显示动态 IP**：妙妙屋只从 `remote_servers.domain` 发域名（DDNS 名在 `pull_address`，由妙妙屋侧修复）。FLVX 侧的「仍是上次自动地址才跟随」逻辑本身正确，补充大小写/方括号归一化与测试。

## 任务

- [x] 后端：`landing_server_user` 表（复合主键 + user 索引，SQLite/PG AutoMigrate）；一次性回填（`vite_config` 标记 `landing_server_user_backfill_v1`）由已有 landing 类服务器限额生成授权；服务器（节点）删除与用户删除级联。
- [x] 后端：可见性/创建权限统一为「直接授权 OR 启用用户组授权」（列表、创建、规则目标、生命周期停用判断同一条件，单条带 EXISTS 的索引查询）。
- [x] 后端：`/landing/user/get` 返回 `serverIds`；`/landing/user/save` 可选 `serverIds`（替换直接授权；缺省不动）；新增 landing 类限额行自动授权，已有限额行不会反复授权（撤销有效）。
- [x] 后端：创建节点按所属用户的权限/上限检查（管理员替他人创建也检查该用户授权，错误提示说明如何授权）。
- [x] 后端：妙妙屋错误码映射为具体提示；退避期间保留真实原因；手动操作（建/改/删节点等）推送绕过该推送自身的退避，但仍遵守「妙妙屋不可达」断路器（既有契约测试要求）；`core.state` 为空按 stopped 处理；服务器/节点视图返回 `sharedNotice` / `pendingReason`。
- [x] 后端：共享地址自动跟随归一化比较。
- [x] 前端：用户编辑「落地」新增「可用落地服务器」多选；添加共享服务器限额时自动加入；弹窗主「保存」在落地部分有改动时一并保存。
- [x] 前端：服务器设置「可见用户组」旁提示；创建节点对话框管理员「所属用户」；服务器卡片/节点行显示妙妙屋未就绪原因。
- [x] 测试：单元 + contract（SQLite + PostgreSQL），全量 `go test ./...` 对比基线；前端构建。
- [x] 生产副本迁移演练（磁盘副本，非 /tmp）。
- [x] 截图（桌面 + H5）。
- [ ] 发布 fork.57、CI/镜像/资产核验。
- [ ] 等 quota-log-hotfix OTA 单元结束 → 备份 + prune → 升级生产面板 → 健康/节点/只读线上检查。
- [ ] 计划收尾提交。

## 资源效率

- 可见性：一条查询，`landing_server_user` 主键 (landing_server_id, user_id) + `idx_landing_server_user_user`；组授权部分沿用原 join。无 N+1。
- 生命周期作业：原来每个非管理员节点一条组授权 count，现在同样一条（EXISTS OR EXISTS），查询数不变。
- 共享同步循环：不新增请求或每 tick 写入；退避提示文本固定（无倒计时），避免每 tick 改写；`sharedNotice` 仅在列表视图从已存 caps 计算。
- 管理员服务器视图每台服务器多 1 条 count（直接授权人数），仅管理员列表。

## 验证记录

- 定向测试（SQLite + PostgreSQL 18 临时容器）：`internal/landingcustom`、`internal/store/repo`、`internal/http/handler`、`tests/contract` 中 Landing/Shared 相关全部通过；新增用例覆盖仅组授权 / 仅直接授权 / 无授权 / 管理员、管理员替用户创建（授权与节点上限）、限额自动授权与撤销不回弹、一次性回填、删除级联、DDNS 地址跟随域名且保留手工值、手动操作绕过退避、断路器、未知 core 状态。日志 `/root/flvx-workers/runs/landing-share-fix/logs/focused-{sqlite,pg}.log`。
- 全量 `go test ./...`（含 PG DSN）：失败集合 15 项，与已知基线完全一致；同 15 项在未修改的 fork.56 上复跑同样失败（`base.fails` 与 `new.fails` 相同）。0 新增失败。
- 前端：`npm run build`（tsc + vite）通过；改动文件 eslint 0 error（仅既有风格类 warning）。
- 生产副本迁移演练（磁盘副本 `/root/tmp-work/landing-share-fix/prodcopy`）：新增 `landing_server_user` 表 + `idx_landing_server_user_user` 索引，`vite_config` +1 标记行；其余 71 张表行数与内容哈希完全一致；回填 1 条（用户 26 → 服务器 3），`quick_check=ok`，迁移 0.18 s。
- 可见性查询计划：`landing_server` 扫描（3 行）+ 两个相关子查询均走覆盖索引；单次 0.009 ms。
- 隔离网络（`unshare -n`，假 Custom 复刻服务器 5/7 当前状态）运行新后端 + 生产副本：leekao 列表出现「Boil Hinet 马年」；服务器 3 公网地址仍为手工域名；服务器 3 卡片与节点 10 显示「妙妙屋端核心未运行：该服务器 Helper 从未上报 FLVX 状态，可能需要升级 Helper」。
- 截图 28 张（before 12 / after 16，桌面 + H5）：`/root/flvx-workers/runs/landing-share-fix/screens/`。
- 「同步」报错的原始文字无法取回（妙妙屋端不记录 API 请求，FLVX 只记录 HTTP 200）。代码路径中能产生的提示均已改为具体原因；最可能是对服务器 5 的推送/状态被 409（地址核验/官方端口）拒绝后进入退避，退避期间节点修改写入了「同步异常，正在退避重试」。
