# 094 利群汇聚管道隧道（3.0.27-fork.45）

## 约束与设计

遵循方案 B：利群 forward 是普通 FLVX 隧道第一跳的不透明管道；每个管道仅关联一个隧道，多入口共用客户端监听端口。普通用户的规则、分组、授权、计费及限制保持原有路径。管理入口仅管理员可用，API token 不回传、不记录。生产利群 API 仅 GET；不接管、不创建、不修改、不删除现有六条 forward。VPS、安装脚本、其他服务及 memory 文件不在范围内。

采用独立持久化管道关联，部署时将首个落地拨号地址替换为 127.0.0.1 和利群 listen_port；落地继续使用自己的服务端口，不占用或强迫所有落地使用客户端端口。所有重建路径从持久化关联还原，诊断由入口 agent 执行。具体接口和一致性处理随实现记录。

## 检查清单

- [x] 读取项目规则、发布/备份流程、利群文档和账号背景；确认 fork.44 起点与范围。
- [x] 实现账号/管道数据层与有界 API 客户端（缓存、限速、重试、ETag、幂等）；fake API 和单元测试。
- [x] 实现管理员接口、管道接管/换落地/创建、普通隧道关联与更新删除一致性、诊断和重部署；契约测试。
- [x] 实现管理员桌面/H5 导航及页面、复用普通隧道编辑器、确认和有界进度界面。
- [x] 对比 fork.44 基线，完成后端、前端、netns 数据路径/流量/限制测试及桌面/移动截图、资源测量。
- [x] 提交推送分支，CI 绿色后打 tag 3.0.27-fork.45，验证镜像与 release 资产。
- [x] 创建验证备份并 prune 保留两份；升级 /opt/flvx-svc，检查节点、隧道及只读利群概览。
- [x] 写中文总结与回滚说明，完成计划提交。

## 验证与发布记录

证据目录：`/root/flvx-workers/runs/leikwan-tunnel/`。所有自动化测试仅调用本地 fake LeiKwan API；浏览器单实例，MemAvailable ≥750 MiB，systemd scope MemoryMax=650M / MemorySwapMax=0。生产账号保存只修改本地面板数据库，远端仅 GET。

- API/数据层提交 `9f938ce0`：新接口均管理员 HTTP 403 + envelope，token JSON 隐藏；共享有界客户端（10 秒、4 连接/host）、600 ms 请求间隔/2200 ms 写间隔、15 秒 singleflight 缓存、429 Retry-After。fake API 与单元/契约测试通过；生产 API 未调用。前端初版提交 `bb35241c`，布局与端到端验证继续。

- 隧道/代理提交 `81b70648`，API 边界补齐 `93621631`，前端视觉修正 `176acb82`。scoped 后端生命周期测试 18.711 s、代理 PipePing/取消测试 0.138 s 通过；涵盖多入口/重启/重部署、换端口/节点、冲突及响应丢失补偿、四种删除路径默认保留。客户端 race、管理员403及账号/管道契约均通过。前端 tsc/build 通过，eslint 96 errors / 3582 warnings，较基线少1条警告。端到端与截图验证进行中。

- 最终实现提交 `8f89997d` CI [38536586236](https://github.com/ImoLR/FLVXR2/actions/runs/38536586236) 四项全绿。完整 Go suite 与 fork.44 失败集合完全相同（无新增失败）；新增单元/契约/race 通过。`e2e/results.json` 12项全过：双入口中继、连接/IP限制、计费、PipePing、重启/重连/批量重部署和落地端口同步。两入口各1MiB原始流量按既有 flow=2 公式每方向计入4MiB，精确一致。测试 netns 和 /etc/flvxlk45_* 已清理。
- `ui-check.json` pass=true：桌面/390px移动端页面、全部对话框、普通隧道关联编辑/删除默认保留、管理员/H5导航与非管理员不可见、关闭取消进度。截图与基线比对后修正卡片标题对齐及两个新复选框的可访问名称；浏览器650M/Swap0、≥750MiB门槛。测试辅助cmd已从工作区移除，未提交。
- 已发现但按范围未修复的既有安全问题：agent启动WebSocket URL及HTTP回退日志会含节点secret（websocket_reporter.go:2799、service/traffic_reporter.go:183/210）；本次只在隔离测试使用的PSK日志中观察到，真实利群token未输出。最终总结必须告知。

- 标签 `3.0.27-fork.45` 指向 `34c8059c`，精确提交 CI [38536941371](https://github.com/ImoLR/FLVXR2/actions/runs/38536941371) 全绿；镜像/发布 [38537199232](https://github.com/ImoLR/FLVXR2/actions/runs/38537199232) 全绿。Release 为 Latest/正式版本，10 个资产与 fork.44 一致；compose、PINNED_VERSION、仓库地址、两个架构 SHA256 和离线 zip 校验通过（release-validation.json）。

- 2026-10-14 09:01:58 UTC 升级生产；备份 `/opt/flvx-svc/rollback/pre-fork45-20261014T090138Z`，SQLite quick_check=ok，prune 仅保留它和 pre-fork44。两容器 fork.45，后端 healthy，公网 version.json 正确。27/27 升级前在线节点重新上报（节点24原本离线）；隧道3/4/5诊断通过，节点/用户/隧道/规则/端口/链记录数不变。
- 通过生产新页面保存服务39账号，仅本地数据库新增账号；真实利群仅 GET options/forwards。配额6/8、路由21/23/24/25/29、转发263/264/279/280/281/282均未接管；pipe/create表仍0条。生产桌面/移动截图和验证见 prod-leikwan-verification.json、prod-desktop-leikwan.png、prod-mobile-leikwan.png；浏览器无错误/OOM。

- 中文总结完成：`/root/flvx-workers/runs/leikwan-tunnel/summary.md`，包含设计、34文件清单、测试/截图/资源证据、发布与生产、备份清理和回滚、安全问题及第二阶段事项。未创建真实管道、未升级任何生产agent、未触碰VPS/其他服务；保留未跟踪计划048。
