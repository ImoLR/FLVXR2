# 103 fork.53 发布与节点 21 原生落地金丝雀

基线：feature/3.0.27-landing-nodes `6b0527de`；用户 2026-10-18 已批准截图及发布。执行分支 maintenance/3.0.27-fork.53-landing-nodes，沿用当前工作树。证据目录 `/root/flvx-workers/runs/landing-release/`。

仅发布计划 101 第一期和第二期 B；生产面板升级、节点 21 OTA/原生落地及金丝雀、节点 21/25 旧入站清理。节点 25 仅 API 清理；节点 35/36、Custom 发布/接入及其他栈不操作。不给真实用户组授权。不修改 memory 或安装脚本。

- [x] 阅读流程、计划和前序总结；核对干净 feature head，创建发布分支。
- [x] 修复门禁发现的额度协调重复写入：仅周期/继承上限/暂停状态变化时更新，新增回归测试。
- [x] 发布门禁：全量 Go 失败集合不超过既有 15，前端构建，生产一致性副本增量迁移及原表校验，流量上传 reconcile 成本实测。
- [x] 发布 fork.53，CI/Images 通过，核验与 fork.52 相同的 10 项资产。
- [x] 完整 SQLite、compose/.env、旧镜像标签及 rollback.sh 备份；执行两份保留裁剪。
- [x] 生产升级、面板健康与对象数量核验，升级前后各约 10 分钟 CPU/SQLite 写入观察（节点 42 上报例外另列）。
- [ ] 节点上报完全恢复原基线：42 重连后无指标，fork.52 后端对照亦复现，未操作范围外节点。
- [x] 节点 21 诊断及 agent/mmwx 基线备份；仅节点 21 OTA，能力及诊断复验（延续面板升级后的 42 异常，无新增失败）。
- [x] 核对全机监听/FLVX 端口，分离规则与落地段；启用 admin-only 原生落地，核验固定 Core/低权限/资源上限及 mmwx 不变。
- [x] 备份旧入站 8/2 和节点 21 sing-box 状态；API 删除并 apply 空配置，节点 21 停用旧服务，外部端口归属核验（60061 为 mmwx，保留）。
- [ ] 临时用户双协议 ≥20 MB 直连、规则 max(T,L)、热更新、连接限制、私网阻断、单击删除与最终计数金丝雀。
- [ ] 清理临时对象/进程/副本；计划 101 FLVX 条目证据、维护/功能分支同步及中文总结。

## 核验记录

启动：HEAD `6b0527de93a2a4a7f88faed8b110063f2e4809cf`，工作树干净；生产镜像 fork.52，数据库卷精确为 `flvx_sqlite_data`。所有重型作业串行，GOMAXPROCS=2，前端 Node 堆 768 MiB，scratch 位于 `/root/tmp-work/landing-release/`。

发布前原始全量失败恰为同一 15 项，前端构建通过。完整生产 SQLite 副本迁移及隔离网络 16365 启动通过；66 个原有业务表行数及原有列 SHA256 全部一致，新增 6 表，node 新增 landing_capable/default_user_quota_bytes，forward 新增 landing_node_id/landing_auto_paused，无删列。sqlite_sequence 仅新增新表序列项，原序列一致。

性能门禁：11 用户/31 规则，按实际路径生成 31 条服务器额度，连续 200 次 reconcile 原中位 16.168 ms / P95 19.244 ms（每次全部额度都 INSERT+UPDATE）；最小修复后中位 0.957 ms / P95 1.828 ms，稳定额度零写入。回归测试在旧源码 overlay 上如期失败；SQLite/PG 落地定向全部通过（repo 29.703s、handler 6.121s、contract 21.671s），含继承上限变更、显式覆盖、周期归零及暂停恢复。证据 reconcile-*.log、landing-focused.log。最终全量复验（含 PG）仍恰为既有 15 项失败，新增/消失均 0，见 backend-final-comparison.json；代码 CI https://github.com/ImoLR/FLVXR2/actions/runs/39131769765 四项全部通过。

升级前 600.07 秒监测：backend cgroup CPU 平均 3.328%（单核=100%），backend /proc write_bytes 239,171 B/s，WAL 120/120 个 5 秒采样间隔发生写入。该 I/O 指标包含进程全部磁盘写入，作为 SQLite 写负载代理，并非 SQL 语句数。证据 cpu-before.json。

只读现场差异：初始节点 28/29 在线，节点 22 原已离线。节点 21 官方 mmw-agent 直接监听 60061/60062；无 /etc/flvxt2、无独立 xray 进程，Helper v0.7.4 active、无接管完成记录。不得为满足旧端口关闭或 xray=1 的预期而操作 mmwx；以实际前后不变作证并在总结明确未满足项。

发布：annotated tag `3.0.27-fork.53` → `814917d6c254969d95bba454822d250e6436bb57`；CI https://github.com/ImoLR/FLVXR2/actions/runs/39132085593、Images https://github.com/ImoLR/FLVXR2/actions/runs/39132087254 全绿。正式 Latest Release 10 项资产与 fork.52 同名；两架构 gost SHA256、compose 镜像 tag、两个脚本固定版本/仓库、离线 ZIP 完整性均通过（release-validation.json）。未修改安装脚本；离线包 offline.sh 沿用 fork.52 原内容。

节点 21 前置证据：6 条隧道 23/26/32/33/43/66 共 27 项诊断全通过（diagnoses-before-ota.json）。SSH agent 回滚点 `/root/flvxx-backup-20261018T134521Z-pre-fork53`，含二进制/配置/unit/restore-agent.sh；旧 sing-box 目录及单元原不存在，记录于 node21-backup.json。

生产回滚点 `/opt/flvx-svc/rollback/pre-fork53-20261018T135250Z`：完整在线 SQLite quick_check=ok、compose/.env、fork.52 两镜像本地标签、rollback.sh（含节点 21 SSH 恢复入口及可选完整 DB 恢复）。备份计数 node/user/tunnel/forward/user_tunnel = 29/11/43/31/143。已执行指定 prune：保留 pre-fork53 与 pre-fork52，删除 pre-fork51 目录/标签和 fork.50 本地镜像；见 backup.json、prune.log。

生产 fork.53 于 13:54Z 启动，健康检查及前端 nginx/版本通过，原对象数量及用户组/隧道链数据不变。升级后 600.07 秒 CPU 平均 3.726%，进程 write_bytes 221,464 B/s，WAL 120/120 间隔写入，见 cpu-after.json。节点 42 在重连后不再上报（最后指标 13:54:06Z），控制命令超时，TCP/SSH 端口可达；其余 27 个原在线节点正常。仅重启一次 backend 无改善，继而使用备份 fork.52 backend、保留当前 DB 作约 4 分钟对照，完全相同的握手在线/命令超时/无指标现象；随后恢复 fork.53。不能断言根因或宣称全节点健康通过；未 OTA、重启或修改 42。相关证据 prod-backend52-final.json、backend-52-comparison.json、diagnoses-after-panel.json。节点 21 相关 27 项诊断中仅 42 一项失败；金丝雀改用零现有规则且 3/3 诊断通过的隧道 26，不修改隧道。

节点 21：面板单节点 OTA 返回成功，fork.53 重连、landingSupported=true；其余节点版本不变。OTA 后 6 隧道 26/27 检查通过，唯一失败仍是 42，较 OTA 前无新增失败（diagnoses-after-ota.json）。规则端口仅由 `10000-65535` 改为 `10000-29999,31000-65535`，node 行仅 port/updated_time 改变；现用 FLVX 14066/27961/28701/45459/49251/65107 全保留，落地 30000–30999 全段无其他监听。原生落地服务器 1 已启用：双向 ×1、每人 3 节点、两协议、计划默认值、无任何组授权。

Core 实机 SHA256 `ae4b83f0cf77c4aa73857cb1de773e7ec89b1bc5ded272a677b46d417cf40212` 与固定 amd64 资产一致；systemd active、User=flvx-landing、MemoryMax=268435456、CPUQuotaPerSecUSec=500ms。mmw-agent/Helper PID、启动时间、重启次数及 ownership/takeover 状态完全一致；独立 xray 可执行文件数 0→0，mmwx 入站已建立连接 0→0、控制连接 1→1。证据 node21-after-enable.json；没有真实 mmwx 入站会话可供掉线验证，不把零连接观察写成在用会话压测。

旧入站：2（25）/8（21）完整行已导出至生产回滚目录 old-deployed-inbounds.json；节点 21 远端备份记录旧目录/unit 原本不存在。两个 delete API 均成功且自动下发 inbounds=[]，最新 revision 均 deployed；21 的 API 自动安装了原先不存在的空 sing-box，随后已 stop+disable，25 仅 API 操作并保留空服务。外部 TCP：25:48221 关闭；21:60061 仍由 mmw-agent 使用，明确未关闭。证据 old-inbounds-cleanup.json、old-inbounds-empty-config.json、old-inbounds-external-ports.json、node21-after-old-cleanup.json。
