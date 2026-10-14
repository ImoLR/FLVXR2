# 096 — 利群 phase 2 / 3.0.27-fork.47

范围：agent 日志秘密脱敏；利群客户端只读检测、管理员徽章和入口提示；发布及生产升级；仅节点 52 OTA；广州 VPS front relay 覆盖 FLVX 回环连接；新增一条广港 pipe/tunnel 并验证。保持原六条 forward、手机端口、其他服务及代理版本不变。不修改安装脚本、内存文件或用户组。

## 检查清单

- [x] 读取项目约束及指定 memory，确认 fork.46 已发布并在生产；从 origin 的 fork.46 分支头 0ad40aa0 建立 fork.47 分支。
- [x] 记录生产节点/六条 forward ETag 基线，完成 VPS 完整带日期备份。
- [x] 审计 go-gost（含 x）/go-backend 日志，脱敏 PSK、JWT/利群 token、完整配置及带 URL 的错误，增加回归测试。
- [x] 实现只读利群检测、周期上报、只变更写入、管理员字段与桌面/移动徽章及入口选择提示；完成对应测试。
- [x] 本地构建与测试，对照现有失败基线，截图验证。
- [x] 分支 push → CI 绿 → fork.47 tag → images 绿 → 全量发布资产校验。
- [x] 生产备份、保留最新两份、升级面板及健康/节点上报核对。
- [x] 仅 OTA 节点 52，校验原 flvxx.service/-C、版本及徽章；记录其他旧版本节点。
- [ ] bridge repo 中实现、测试、提交 front relay cgroup output NAT；备份后部署并量化短暂重启影响。
- [ ] 选择稳定香港公网 IPv4 落地；持久化幂等键，经生产 API 仅 POST 创建一次新 forward 和未分组 tunnel。
- [ ] 新端口 external/loopback、进度、diagnose/PipePing、10–50 MB 与数百连接实流量、计费及 relay 证据验证；删除临时规则。
- [ ] 核对原 forward/ETag/手机端口与其他服务、生产节点状态；删除本任务临时生产 DB 副本。
- [ ] 完成中文 summary（含截图、备份、回滚、破坏性动作、安全问题及未实施建议）并提交最终计划记录。

## 执行记录

- 生产：回滚点 `/opt/flvx-svc/rollback/pre-fork47-20261014T102037Z`，SQLite quick_check=ok；prune 保留 pre-fork47/pre-fork46，删除 pre-fork45 和 fork.44 旧镜像。10:21:07Z 开始升级，两个容器 fork.47、backend healthy、HTTP/版本/nginx 检查通过；记录数不变，仅 node 三列新增，27 个原在线节点均已重新上报。
- 仅节点 52 OTA：`gost 3.0.27-fork.47 (go1.23.12 linux/amd64)`，原 `flvxx.service` 与 `-C /etc/flvxx/config.json` 完全未变，addr/PSK 未变；新进程日志 PSK 匹配 0，连接日志含脱敏值。生产仅 52 显示利群 running、六条端口，其他节点版本留存于 `nodes-after.json`。

- 发布：实现提交 `95ea6796`；CI `38546176485` 全绿；tag `3.0.27-fork.47` 的 images/release `38546547973` 全绿。Latest 正式 release，10 个资产与 fork.46 一致；compose、脚本版本、两个 agent SHA256、离线包 ZIP 完整性均通过。

- A2：Linux 每分钟只读检测一次并复用现有 SystemInfo；仅解析 forward ID/listen_port、读取 cgroup 进程状态和 TCP LISTEN。三列可跨 SQLite/PostgreSQL，重复上报/重连不写，旧 agent 不覆盖；普通用户列表及广播剔除检测数据。
- 前端 build/定向 lint 通过；backend 全量失败 15 项与 fork.46 完全相同（无新增）；agent、handler/ws/contract 定向测试通过。桌面卡片/列表、390px 手机徽章与详情截图：run 目录 `local-*-badge*.png`。
- bridge 兼容性：VPS nft 0.9.8 不支持 socket cgroupv2，采用现有 iptables-nft cgroup --path 专用 OUTPUT NAT 链；分支 `feat/flvx-loopback-phase2`，提交 `056a769`，本机 unit/race/隔离 netns IPv4+IPv6 原目标及无回环测试通过，尚未部署。

- agent 日志/检测单测已通过；全量 grep 审计在 run 目录。后端 LeiKwan 客户端已有固定错误文本及 token 过滤，GORM 日志为 Silent，HTTP 访问日志只输出 path；无额外 JWT/LeiKwan token 输出点。命令日志仅保留类型，HTTP 响应体及 sing-box 配置诊断内容不再进入日志。显式 CLI 配置导出保留原功能。

- VPS 备份：`/root/backup-20261014T094320Z-flvx-phase2`；基线保存在 run 目录的 `nodes-before.json`、`existing-forwards-before.json`（6/8，ETag r10/r2/r2/r3/r1/r8）。

- 2026-10-14：tag `3.0.27-fork.46` release 已发布；`/opt/flvx-svc/.env` 与两个运行容器均为 fork.46。
- 输出目录：`/root/flvx-workers/runs/leikwan-phase2/`；最终报告：`summary.md`。
