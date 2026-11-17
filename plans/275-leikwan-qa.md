# 275 — 利群功能全面测试（只出报告，不改代码，不发布）

基线：tag `3.0.27-fork.144`（fea5c139 = 生产），分支 `qa/3.0.27-leikwan-qa`，worktree `/root/projects/flvxx/flvxx-leikwan-qa`（只放计划文档）。
用户 11-17：「把这边完善完之后，整个利群的功能要做一个全面的测试，看看还有什么其他问题」。
规则：所有写操作只在实验室（生产库只读快照 + 假利群 API + 隔离网络命名空间）；生产只读（admin token GET/只读 POST 列表、只读 SSH、aws-hk TCP 连接测试，一次一个、间隔几秒）。
交付：/root/flvx-workers/runs/leikwan-qa/report.md + final.md + summary.md，缺陷截图 /root/mmwx-preview/leikwan-qa/（index.html）。

## 检查清单

- [x] 读 memory / 计划 266–272 / 各轮 final.md，建 worktree + 计划文档。
- [x] 实验室：fork.144 代码（leikwanlab 构建）+ 生产前端 dist + 生产库快照 + 新普通用户 + 假利群 API。
- [x] 故事 1：管理员给新用户授权（主入口 A + 备线开），用户能看到 / 不能看到什么（导航、API、别人的数据、中转 IP）。
- [x] 故事 2：用户建利群规则（面板落地 + 外部节点）、手动 IP:端口被拒、换落地、删除、诊断（用户只看自己的入口）。
- [x] 故事 3：生成订阅（一条、自动识别、重用）、详细设置 → 订阅编辑器、混入其他节点、集合 / 分流；落地页复制链接、规则行复制链接；Clash / Surge / v2ray 输出。
- [x] 故事 4：管理员换主入口 A → B：提示、宽限 chip、订阅立刻换 B、旧入口保留到订阅拉取 / 24 h、之后屏蔽；豁免规则全开；agent 侧按主机屏蔽（夹具）。
- [x] 故事 5：配额 / 到期屏蔽、取消授权（规则 / 订阅 / 导航）、流量 / 备线流量数字。
- [x] 管理员页其余：专线·规则树、入口机抽屉（接管、差异、漂移 ⚠、logswitch）、设置、备线隧道、利群流量计费 / 倍率 / 重置、诊断（管理员）。
- [x] 安全：越权读写（别人的规则 / 专线 / 中转 IP）、无授权用户 API 全空。
- [x] 界面：H5 390 / 360、浅色 + 深色、长名字。
- [x] 生产只读：4 台利群入口（百度云广州、阿里广州、火山广州、火山上海）转发、nft 表、日志、CPU / 内存 / 连接数、预热只在主入口；aws-hk 端口连接测试。
- [x] 报告 report.md + final.md + summary.md、截图画廊、删实验室（含生产库副本）。

## 记录（11-17 19:40–20:40 台湾）

- 实验室：/root/flvx-workers/runs/leikwan-qa/lab（run.sh / inner.sh / agent.sh / prep.py / s*.py / shots*.cjs）。生产库快照 + 假利群 API + fork.144 面板（leikwanlab）
  + 生产 fork.144 前端 dist + 4 台假入口机 agent（fork.144 leikwanlab，主机夹具，nft/systemctl 只记日志，只读挂载空间）。用完已删 /root/lkqa-lab。
- 结果：P1 1 条（F01 删用户不清利群规则，端口不屏蔽）、P2 4 条（F02 备线改目标失败后错误卡住 / 重试无效，F03 任意订阅拉取都结束宽限，
  F04 用户页没有换入口提示，F05 删外部节点 / 落地不查利群规则）、P3 11 条、风险 6 条。详见 runs/leikwan-qa/report.md。
- 生产只读：4 台利群入口 7/7 转发、agent 61–63 MB、日志安静、差异 0；nft 屏蔽和 AWS HK 连接测试都符合「只开主入口」+ 5 条豁免。
- 截图：http://104.145.236.29:8791/leikwan-qa/（9 张 + index.html）。
