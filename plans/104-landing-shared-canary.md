# 104 服务器 7 共享落地生产金丝雀

用户授权：2026-10-18 `landing-shared-canary`；基线 fork.53，维护分支 `maintenance/3.0.27-fork.53-landing-nodes`。仅配置 FLVX 接入并验证 mmwx 服务器 7（FLVX 节点 35），不启用原生落地、不 OTA、不改其他服务器、真实用户组或 mmwx 设置。生产修改前建立一致性备份并执行两份保留规则。凭据只在私有文件/进程内使用。

- [x] 阅读两项前置发布 final/summary、部署记忆、AGENTS 和计划 101；确认前置发布均完成。
- [ ] 保存生产回滚点及服务器 7 进程、归属、连接基线。
- [ ] FLVX 保存 mmwx URL/令牌，测试、同步并验证完整主机清单、共享只读上限、双装提示及 admin-only 默认值。
- [ ] 仅从节点 35 规则端口范围扣除共享端口，记录旧/新值与回滚方式。
- [ ] 临时用户 landing-canary2 双协议创建、Helper active、Custom 电脑/手机截图。
- [ ] 各协议至少 20 MB 直连，核对 ×2 及共享计数；低流量既有隧道临时规则验证 max(T,L)。
- [ ] 长下载中热增删、私网阻断、临时连接上限、单次点击删除（1–2 分钟）、端口释放及最终计数去重。
- [ ] 对比 mmwx 自身 Core、SingleCore/ownership 及既有入站长连接前/中/后状态。
- [ ] 清理临时规则、节点、授权、用户及 scratch，验证无残留且共享保持 active/connected。
- [ ] 按证据更新计划 101 核验记录；提交并推送维护分支文档；写中文总结。

证据目录：`/root/flvx-workers/runs/landing-shared-canary/`；临时目录：`/root/tmp-work/landing-shared-canary/`。未实测的计划项目保持未勾选。
