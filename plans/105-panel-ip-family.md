# 105 — 节点连接面板 IP 协议族与自动自愈

## 范围与基线

- 生产版本：`3.0.27-fork.53`；起点：`origin/maintenance/3.0.27-fork.53-landing-nodes` (`e3ab3002`)，相对 tag 仅文档提交。
- 工作树：`/root/projects/flvxx/flvxx-panel-ip-family`；分支：`maintenance/3.0.27-fork.54-panel-ip-family`。
- 仅修改节点连接面板的设置、WS/面板 HTTP 拨号、安装选项及必要的 agent 死连接检测；不修改转发出口协议族、落地功能、镜像列表或其他节点/服务。
- 用户已授权发布、生产备份升级、节点 42 备份/OTA、限定 Cloudflare IPv6:443 的临时故障注入和最终 `prefer_ipv4`。

## 任务清单

- [x] 读取操作约束、核对生产版本及分支，创建隔离工作树。
- [x] 记录测试基线；核对面板 HTTP/WS 路径、设置下发和死连接行为（实际断流验证见后续 E2E）。
- [x] 实现并测试 agent 协议族选择、配置持久化、自动自愈与连接日志。
- [x] 实现并测试节点列/API/下发、表单和安装命令选项及 install.sh 参数。
- [x] 完成本地 IPv6 连接后黑洞 E2E、三个 Go 模块测试、相关 vet、前端构建。
- [x] 提交推送、CI 验证、tag fork.54、Images/发布资源验证。
- [x] 生产备份与回滚脚本、保留最新两份、升级及健康/表行数/在线节点核对。
- [ ] 节点 42 SSH 备份、OTA、14 隧道重部署验证。
- [ ] 节点 42 auto IPv6 黑洞自愈实测，移除故障规则，设置 prefer_ipv4 并验证 IPv4 WS。
- [ ] 记录证据、所有破坏性动作与回滚方式，写中文最终总结并提交计划完成记录。

## 验证与发布记录

运行证据目录：`/root/flvx-workers/runs/panel-ip-family/`；Go 临时目录：`/root/p105t`。

基线：go-backend 15 个既有失败（含子测试）；go-gost 与 go-gost/x 全通过。面板节点 WS 已有 15s ping / 45s pong deadline；历史 HTTP 日志只记录路径和整个连接时长，无法单凭该日志区分浏览器/agent，更不能将连接存活时长视为死连接时长。

Agent：`panelconn` 共用 WS/面板 HTTP 策略与分模式连接池；auto 的两次 IPv6 异常会话触发 1h IPv4 优先，正常关闭/本地重连不计数。设置先原子持久化再回执重连。公网 IP 查询移到后台并限制 curl 时间，避免阻塞 WS 读循环。为新增配置加载测试，将 CLI 参数解析从 init 移至 main，运行参数语义不变。定向单测及 race 已通过。

Panel：新增 `node.panel_ip_family`（varchar、默认空）；create/update/list、备份导入导出保留该值。新 agent 握手声明当前模式，旧 agent 不下发未知命令。未配置的节点继承 installer 初始选择，面板明确保存后（包括 auto）以面板为准。节点表单/安装弹窗仅增加 Select；installer `-f` 接受四值，省略不写 key。镜像列表与 sha256 代码未改。后端 API/列/WS 下发测试通过；无 pong 的已建立 agent TCP 连接在 45.001s 被标离线，未修改既有心跳机制。前端 build、installer 参数及 JSON 验证通过。

完整验证：三个模块 `go test ./...` 完成，backend 失败集合与基线 15/15 完全一致，gost / x 全通过；所有触及 Go 包 vet 通过，拨号/自愈/持久化 race 测试通过，前端 build 通过。独立 netns 对已建立的 IPv6 WS 双向丢包，两次分别 59.92s/59.91s 触发超时，auto 在 120.19s 转 IPv4，面板 HTTP 同时恢复；恢复 IPv6 后 prefer_ipv4 连续 3 次仍使用 IPv4。netns 已删除。测试不使用生产 DB。

分支已推送；CI Build Check [39144085675](https://github.com/ImoLR/FLVXR2/actions/runs/39144085675) 四项全部绿色（含 PostgreSQL contract）。tag `3.0.27-fork.54` 指向 `43c8d277`。节点 42 备份：`/root/flvxx-backup-20261018T161547Z-pre-fork54`，含二进制/config/gost/unit 和 `restore-agent.sh`，旧版本 fork.53；另一个面板 flux_agent PID=429，未操作。

Images [39144267928](https://github.com/ImoLR/FLVXR2/actions/runs/39144267928) 全通过；[fork.54 release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.54) 为 Latest、非 prerelease，10 个资源与 fork.53 同集。v4/v6 compose 镜像和两个安装脚本版本锁定正确，amd64/arm64 sha256、离线包中的二进制均校验通过；发布 amd64 `-V` 确认为 fork.54。

生产回滚点：`/opt/flvx-svc/rollback/pre-fork54-20261018T163653Z`，SQLite 固定读快照在线备份（quick_check=ok）、compose/.env、旧镜像 local 标签、`rollback.sh` 均已就绪。首次分批备份因写入反复重试，停止了本任务备份进程并清理其未完成副本，原库未改。按规定 prune 后仅留本次 + `pre-shared-canary-20261018T151747Z`；删除 pre-fork53 及 fork.52 本地镜像（旧 DB 备份不可恢复；镜像可按 tag 重拉）。升级前在线 28/29，22 原先离线；核心行数 node/user/tunnel/forward/user_tunnel = 29/11/43/31/143。

生产已于约 16:37Z 升级 fork.54，后端 healthy；本机前后端和 flvxxht 公网健康检查 200，前端 build 含 fork.54。在线集合 28/29 与升级前完全相同，核心业务表行数及分组/链路/授权关系一致（动态采样表增量见运行目录）。节点 42 在面板重启后 14/14 隧道重部署成功，仍为旧 fork.53，尚未 OTA。

补充健康证据：本机 curl 经 IPv6 Cloudflare 访问 flvxxht `/flow/test` 为 200（默认 Python urllib 公网探针被返回 403，未改 Cloudflare 策略）。所有非采样表行数完全一致；node_metric 2437175→2439716、tunnel_metric 2818→2822 为正常采样增长。新增 panel_ip_family 为 NOT NULL varchar(16)，默认空。
