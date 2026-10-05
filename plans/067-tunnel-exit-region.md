# 067 节点地区与入口 → 出口地区分组

基线：`ed80872a`；分支：`maintenance/3.0.27-fork.19-tunnel-region`。
本轮仅实现、验证、提交、推送与 CI；不发布、不打 tag、不升级生产。

## 实施与验证

- [x] 阅读项目规则与发布记忆，确认基线并创建分支。
- [x] 嵌入离线地理数据库、刷新脚本、许可说明及检测单元测试（`go test`/`go vet ./internal/geoip` 通过）。
- [x] 节点地区字段、校验、管理员检测接口、创建检测与一次性异步回填（节点更新传播契约随隧道测试验证）。
- [x] 批量派生隧道入口/出口地区，验证普通用户隐私及派生逻辑；节点地区 round-trip、不改写 in_ip/不重部署、非管理员检测 403 契约通过。
- [x] 地区公共工具、节点表单/展示、Select 分组及规则表单/筛选器；表单支持搜索，表格采用 native optgroup 避免裁切，Escape 仅关闭菜单。
- [x] 隧道页入口/地区分组、筛选持久化、地区标记与原有操作兼容；真实副本浏览器验证通过，另用浏览器模拟响应验证多入口勾选去重及多地区标签。
- [x] 后端 `go build ./...`、所改包 `go vet` 通过；`go test ./...` 与新建基线 worktree 均为相同 19 个既有失败，新增 0；前端 `npm run build` 通过。
- [x] 生产库在线备份副本在 16365 本地验证：25 节点回填，重启不重复；tunnel/forward_port in_ip 与原副本一致；用户 3 分组元数据无节点身份，检测接口 envelope code 403（沿用项目 HTTP 200 约定）；截图已保存。
- [ ] 确认 CI Build Check 全绿：代码审查与分支推送已完成，GitHub 托管 runner 排队阻塞，尚未获得 CI 结果。
- [x] 写入中文总结 `/root/flvx-workers/runs/tunnel-region/summary.md`（明确记录 CI 外部阻塞，未宣称本轮全部完成）。

## Round 2 (amendments)

- [x] 阅读指定记忆、首轮总结和项目规则，确认仅实现/提交/推送，不发布或修改生产。
- [ ] A/B：统一 IPv4 优先的公网识别结果、检测接口和回填日志，补齐单元/契约验证。
- [ ] C：节点表单三地址识别、人工值保护、识别提示及未设置地区 Chip。
- [x] D：隧道分组胶囊菜单、全宽布局、分组标题及规则选择器提示。
- [ ] E：基线/新版 1440/1920、明暗主题/390px、筛选/菜单/规则选择器/节点截图，逐张查看。
- [ ] F：后端 build/vet/full test 对比基线、前端 build、全新副本回填和用户 3 隐私核验。
- [ ] 提交并推送最终 HEAD，确认 CI Build Check 四个 job 全绿。
- [ ] 写中文总结 `/root/flvx-workers/runs/tunnel-region-r2/summary.md`，列出证据与未实施建议。

## 数据集（首轮）

DB-IP IP to Country Lite **2026-10**（2026-10-01 月度版，2026-10-05 下载），CC BY 4.0；[来源](https://db-ip.com/db/download/ip-to-country-lite)。710,834 条 IPv4/IPv6 范围转为无依赖二进制区间表，gzip 嵌入 **4,298,059 bytes**。归属、原始/嵌入 SHA-256 与刷新命令见 `go-backend/internal/geoip/DATASET.md`；不改写数据库地理归属。DNS 2 秒超时，私网/环回/CGNAT/特殊用途地址返回未知。

未剥离基线隧道名称中既有的 AWS HK / Misaka TW / Lightlayer TW 文本；新增入口标签只显示地区/城市。副本检测与名称预期不符：14 Lightlayer TW→HK；20 Vmsilo TW→CN（私网 IPv4 后回退 IPv6）；24 沪日ixp G口 bug鸡→HK。未人工改写数据。普通未压缩 paneld 比基线增大 6,735,643 bytes，低于 10 MB。

本地截图：`/root/flvx-workers/runs/tunnel-region/tunnel-entry-region.png`、`tunnel-entry-region-list.png`、`forward-picker-open.png`、`node-region-form.png`。完整节点表、测试集合对照与浏览器记录均在同目录。

## CI 外部阻塞（2026-10-05 20:08 UTC）

GitHub 官方 [Actions 事件](https://www.githubstatus.com/api/v2/summary.json) 自 19:11 UTC 起报告托管 runner 分配延迟。实现提交的 [CI 37366904425](https://github.com/ImoLR/FLVXR2/actions/runs/37366904425) 四个 job 一直 queued，无执行日志；本地没有 PostgreSQL 17 镜像，未把未执行的 PostgreSQL 合约当作通过。本轮不满足“CI 全绿”的最终门禁，不进入发布。

本记录提交后以最新分支 HEAD 的 CI 为准：`gh run list --branch maintenance/3.0.27-fork.19-tunnel-region --workflow ci-build.yml`，再 `gh run watch <最新运行ID> --exit-status`。恢复后须核对四个 job 全绿，再勾选 CI 清单；如出现新增失败，仅修复本任务回归。

## 下一轮（不在本轮执行）

- [ ] release-only：发布 `3.0.27-fork.19`，验证发布资产与 CI。
- [ ] 生产备份并升级 `/opt/flvx-svc`，只读核验回填与普通用户隐私。
- [ ] 完成 rollout 记录；回滚使用 fork.18 镜像与原 compose/.env，新增列可保留。
