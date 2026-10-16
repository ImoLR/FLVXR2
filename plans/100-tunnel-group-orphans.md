# 100 隧道、节点和分组授权孤儿记录修复

发布：`3.0.27-fork.51`。基线：`0b5e0074`（fork.50 分支头）。仅修改面板后端与测试，不改 agent、不 OTA、不改变有效业务记录。

- [x] 核对生产版本 fork.50、流程与 worktree，保留主 checkout 原状。
- [ ] 审核所有隧道、节点、用户授权删除路径及数据库引用，记录基线。
- [ ] 修复级联与一次性幂等清理，补充 SQLite/PostgreSQL 回归测试。
- [ ] 完成后端全量测试（失败集合不超过基线 15）和相关包 go vet；生产副本 dry-run。
- [ ] 推送分支，CI 通过，发布 fork.51；核验 Images 和全部 release assets。
- [ ] 生产标准备份与保留两份，升级并执行启动清理。
- [ ] 验证零孤儿、用户权限/分组成员相同、29 节点上报，删除自建数据库副本。
- [ ] 完成中文总结并提交发布记录。

## 核验记录

生产 `/opt/flvx-svc/.env` 起始版本为 `3.0.27-fork.50`。独立 worktree 为 `/root/projects/flvxx/flvxx-tunnel-group-orphans`，分支 `maintenance/3.0.27-fork.51-tunnel-group-orphans`。
