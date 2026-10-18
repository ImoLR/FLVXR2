# 105 — 节点连接面板 IP 协议族与自动自愈

## 范围与基线

- 生产版本：`3.0.27-fork.53`；起点：`origin/maintenance/3.0.27-fork.53-landing-nodes` (`e3ab3002`)，相对 tag 仅文档提交。
- 工作树：`/root/projects/flvxx/flvxx-panel-ip-family`；分支：`maintenance/3.0.27-fork.54-panel-ip-family`。
- 仅修改节点连接面板的设置、WS/面板 HTTP 拨号、安装选项及必要的 agent 死连接检测；不修改转发出口协议族、落地功能、镜像列表或其他节点/服务。
- 用户已授权发布、生产备份升级、节点 42 备份/OTA、限定 Cloudflare IPv6:443 的临时故障注入和最终 `prefer_ipv4`。

## 任务清单

- [x] 读取操作约束、核对生产版本及分支，创建隔离工作树。
- [x] 记录测试基线；核对面板 HTTP/WS 路径、设置下发和死连接行为（实际断流验证见后续 E2E）。
- [ ] 实现并测试 agent 协议族选择、配置持久化、自动自愈与连接日志。
- [ ] 实现并测试节点列/API/下发、表单和安装命令选项及 install.sh 参数。
- [ ] 完成本地 IPv6 连接后黑洞 E2E、三个 Go 模块测试、相关 vet、前端构建。
- [ ] 提交推送、CI 验证、tag fork.54、Images/发布资源验证。
- [ ] 生产备份与回滚脚本、保留最新两份、升级及健康/表行数/在线节点核对。
- [ ] 节点 42 SSH 备份、OTA、14 隧道重部署验证。
- [ ] 节点 42 auto IPv6 黑洞自愈实测，移除故障规则，设置 prefer_ipv4 并验证 IPv4 WS。
- [ ] 记录证据、所有破坏性动作与回滚方式，写中文最终总结并提交计划完成记录。

## 验证与发布记录

运行证据目录：`/root/flvx-workers/runs/panel-ip-family/`；Go 临时目录：`/root/p105t`。

基线：go-backend 15 个既有失败（含子测试）；go-gost 与 go-gost/x 全通过。面板节点 WS 已有 15s ping / 45s pong deadline；历史 HTTP 日志只记录路径和整个连接时长，无法单凭该日志区分浏览器/agent，更不能将连接存活时长视为死连接时长。
