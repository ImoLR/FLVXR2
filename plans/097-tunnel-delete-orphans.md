# 097 — 删除隧道时清理转发服务、修正 IPv6 RA 误报

基线：`origin/maintenance/3.0.27-fork.47-leikwan-phase2` (`7e2d786a`)；发布：`3.0.27-fork.48`。
仅修复已有删除逻辑和 RA 判断，无 schema/UI/安装脚本变更。OTA 仅 52、53。

- [x] 阅读流程与项目规则，确认生产 fork.47，建立隔离 worktree。
- [ ] Part A：审计所有删除 forward 的入口，复用单条删除的运行时清理，添加命令接收端回归测试，保留 replace 行为。
- [ ] Part B：记录曾观察到的 RA 依赖，修正用户态 RA 和无 IPv6 误报，补充单元测试。
- [ ] 验证：backend / agent 测试、基线对照、race、vet；可行时本地 E2E。
- [ ] 推送分支，CI 通过后打 fork.48 标签，核验 Images 和发布资产。
- [ ] 生产备份、清理到两个备份、升级面板及健康检查。
- [ ] 生产临时隧道及两个转发删除验证；检查现有服务读取命令并安全清扫孤儿。
- [ ] 备份 52/53 agent，OTA、版本/单元/回连/RA 验证；清理临时对象。
- [ ] 写中文总结、回滚说明与建议，提交计划最终状态并推送。

## 审计与验证记录

运行证据：`/root/flvx-workers/runs/tunnel-delete-orphans/`。
