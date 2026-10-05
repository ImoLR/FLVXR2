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
- [x] 确认 CI Build Check 全绿：r3 提交 `78b718da` 的 CI `37374592949` 四项成功；见下方证据。完成记录提交的最终 HEAD CI 状态另存 r3 总结。
- [x] 写入中文总结 `/root/flvx-workers/runs/tunnel-region/summary.md`（明确记录 CI 外部阻塞，未宣称本轮全部完成）。

## Round 2 (amendments)

- [x] 阅读指定记忆、首轮总结和项目规则，确认仅实现/提交/推送，不发布或修改生产。
- [x] A/B：统一 IPv4 优先的公网识别结果、检测接口和回填日志，补齐单元/契约验证。
- [x] C：节点表单三地址识别、人工值保护、识别提示及未设置地区 Chip。
- [x] D：隧道分组胶囊菜单、全宽布局、分组标题及规则选择器提示。
- [x] E：基线/新版 1440/1920、明暗主题/390px、筛选/菜单/规则选择器/节点截图，逐张查看。
  - r3 补齐 13 张 PNG（00 原图保留）；全部实际打开检查，记录 `r3/screens-reviewed.json`。真实隧道无未知/多地区，节点空地区仅浏览器 mock；无视觉代码修复。
- [x] F：后端 build/vet/full test 对比基线、前端 build、全新副本回填和用户 3 隐私核验。
  - r3 build/vet/npm build 通过；全量测试与 `/root/flvx-fork10/base.fails` 相同 19 失败、新增 0。新副本 25 节点地区/source/IP 与 r2 一致，节点 20 为 IPv6 回退；用户 3 隐私、detect 403、legacy ip、原有 in_ip 不变均 PASS（`r3/api-check.txt`）。
- [x] 提交并推送、确认 CI Build Check 四个 job 全绿：`78b718da` / run `37374592949` 成功；完成记录提交后继续核对最终 HEAD，见 r3 总结。
- [x] 写中文合并总结 `/root/flvx-workers/runs/tunnel-region-r3/summary.md`，列出 r2/r3 证据与未实施建议；r2 OOM 未单独产出总结，CI 状态在 r3 总结持续更新。

## Round 3（续验，不发布）

- [x] 重读 r1/r2 证据与约束，确认端口无遗留进程；重任务串行，临时文件和构建产物放在 r3 目录。
- [x] 完成后端 build/vet/full test（`-p 2`）及前端 build，核对 19 项既有失败。
  - r3 `backend-build.log`/`backend-vet.log` exit 0；`backend-test.jsonl` 完整结束，`test-comparison.txt` 为相同 19 项失败、新增 0；`frontend-build.log` 完整构建 exit 0（产物 `r3/new-dist/`）。
- [x] 新副本在 16365 复核回填/隐私；补齐全部截图并逐张查看。
  - 1440px region 为 2×518px，1920px 为 4×371px；390px 页面/规则 picker 均无横向溢出。未改变不分组/自定义分组两列 370px 的既有布局。
- [x] 更新本计划并提交本地验证证据。
- [x] 推送验证提交 `be1ca9e3` 并用 `gh run watch` 等待，失败 job 已重试一次；收尾文档提交后继续核对最终 HEAD（总等待最多约 60 分钟）。
  - 最终状态以 r3 `summary.md`/`ci-final.json` 的 headSha、运行 ID 和状态为准；清单以已验证运行作记录，最终 HEAD 的运行结果在总结中单独核对。
- [x] 停止本轮进程并写合并 r2/r3 的中文总结 `/root/flvx-workers/runs/tunnel-region-r3/summary.md`。
  - paneld、静态服务、Chromium 均已结束，16365/13000/13001 无监听；未运行 Vite，未保存实体或操作生产。

## 数据集（首轮）

DB-IP IP to Country Lite **2026-10**（2026-10-01 月度版，2026-10-05 下载），CC BY 4.0；[来源](https://db-ip.com/db/download/ip-to-country-lite)。710,834 条 IPv4/IPv6 范围转为无依赖二进制区间表，gzip 嵌入 **4,298,059 bytes**。归属、原始/嵌入 SHA-256 与刷新命令见 `go-backend/internal/geoip/DATASET.md`；不改写数据库地理归属。DNS 2 秒超时，私网/环回/CGNAT/特殊用途地址返回未知。

未剥离基线隧道名称中既有的 AWS HK / Misaka TW / Lightlayer TW 文本；新增入口标签只显示地区/城市。副本检测与名称预期不符：14 Lightlayer TW→HK；20 Vmsilo TW→CN（私网 IPv4 后回退 IPv6）；24 沪日ixp G口 bug鸡→HK。未人工改写数据。普通未压缩 paneld 比基线增大 6,735,643 bytes，低于 10 MB。

本地截图：`/root/flvx-workers/runs/tunnel-region/tunnel-entry-region.png`、`tunnel-entry-region-list.png`、`forward-picker-open.png`、`node-region-form.png`。完整节点表、测试集合对照与浏览器记录均在同目录。

## Round 3 CI 全绿（2026-10-05 21:25 UTC）

提交 `78b718da` 的 [CI 37374592949](https://github.com/ImoLR/FLVXR2/actions/runs/37374592949) 最终 `completed/success`，Frontend、Backend、PostgreSQL Contract、Agent 四项均成功；累计等待约 43 分钟。证据：r3 `ci-78b718da-success.json/txt`、`ci-watch-final.log`。本提交只勾选完成项并记录此结果；推送后继续检查完成记录提交本身的 CI，最新 headSha/run ID/status 保存在 r3 `summary.md` 和 `ci-final.json`。

## Round 3 CI 先前阻塞记录（2026-10-05 21:13 UTC）

验证提交 `be1ca9e3` 的 [CI 37371273073](https://github.com/ImoLR/FLVXR2/actions/runs/37371273073) 已等待约 31 分钟、完成一次 `--failed` 重试（attempt 2）。Backend 和 PostgreSQL Contract 成功；Frontend/Agent 没有执行构建步骤，GitHub 注释为 `The job was not acquired by Runner of type hosted even after multiple attempts`，最终 run 为 failure（剩余两个 job 为 cancelled）。未把 runner 失败当作代码回归，未修改 CI 配置。原始证据：r3 `ci-attempt1.txt/json`、`ci-attempt2.txt/json`、`ci-watch.log`。

该段记录过去验证提交的结果；收尾文档提交将触发最终 HEAD 的新运行，继续用剩余等待时限核对，最终运行 ID/状态保存在上述 r3 总结及 `ci-final.json`。未发布。

## CI 外部阻塞（首轮历史，2026-10-05 20:08 UTC）

GitHub 官方 [Actions 事件](https://www.githubstatus.com/api/v2/summary.json) 自 19:11 UTC 起报告托管 runner 分配延迟。实现提交的 [CI 37366904425](https://github.com/ImoLR/FLVXR2/actions/runs/37366904425) 四个 job 一直 queued，无执行日志；本地没有 PostgreSQL 17 镜像，未把未执行的 PostgreSQL 合约当作通过。本轮不满足“CI 全绿”的最终门禁，不进入发布。

本记录提交后以最新分支 HEAD 的 CI 为准：`gh run list --branch maintenance/3.0.27-fork.19-tunnel-region --workflow ci-build.yml`，再 `gh run watch <最新运行ID> --exit-status`。恢复后须核对四个 job 全绿，再勾选 CI 清单；如出现新增失败，仅修复本任务回归。

## 下一轮（不在本轮执行）

- [ ] release-only：发布 `3.0.27-fork.19`，验证发布资产与 CI。
- [ ] 生产备份并升级 `/opt/flvx-svc`，只读核验回填与普通用户隐私。
- [ ] 完成 rollout 记录；回滚使用 fork.18 镜像与原 compose/.env，新增列可保留。
