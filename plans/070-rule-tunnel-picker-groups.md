# 070 — 规则表单隧道选择器分组层级（fork.22）

## 范围

从 fork.21 当前 HEAD `f79ec3e7` 创建
`maintenance/3.0.27-fork.22-rule-tunnel-picker-groups`。
仅修改 Select bridge 的显式启用分组列表模式及规则新增/编辑表单隧道选择器。
无搜索框；分组标题吸顶、强调条、数量；隧道常规字重、倍率独立徽标、备注第二行。
保留多地区信息、管理员未设置地区提示、键盘导航与关闭行为。
其他 Select 维持原分支及样式。仅实现、验证、截图和推送分支；不发布、不改生产。

## 任务

- [x] 读取项目规则和两份 memory；确认 fork.21 HEAD、工作区、内存并创建 fork.22 分支。
- [x] 实现 opt-in grouped Select 与规则表单接入；完成静态审阅并提交。
- [x] 串行运行 `npx tsc --noEmit` 和 `npm run build`，保存日志并提交记录。
- [x] 生产 SQLite 只读在线备份到任务目录；复用本地 paneld（仅 16365）与 dist 静态服务；浏览器拦截写请求。
- [x] 单 Chromium / 单 context 串行检查交互与明暗主题，生成并逐张查看 8 张截图，修正缺陷后重拍。
- [x] 发布截图与中文 index.html 到指定 preview 的 picker 子目录，记录验证结果并提交。
- [x] 推送最终分支 HEAD，确认 CI Build Check 绿色（或如实记录 GitHub 阻塞状态）。
- [x] 停止本任务启动的进程，写中文 summary.md（变更行号、验证、截图、发布状态、回退、建议）。

## 证据位置

- 任务目录：`/root/flvx-workers/runs/picker-groups/`
- 截图：任务目录 `screens/`；日志：`logs/`
- 预览：<http://104.145.236.29:8790/11598628d2503f952ceba408201d5eac/picker/>
- 不提交未跟踪的 `plans/048-aws-hk-fork4-agent-canary.md`；不编辑 memory。

## 本地构建

- `npx --no-install tsc --noEmit`：通过，日志 `logs/tsc.log`。
- `npm run build`：通过，日志 `logs/build.log`；未新增依赖或前端测试。
- 轻量只读复审：默认 native / searchable / multiple 的 JSX 与 class 字符串保留，新增内容仅 grouped 单选使用。

## 截图环境

- 2026-10-06 10:30 UTC，SQLite `mode=ro` + 只读事务固定快照后 online backup；`quick_check=ok`，25 节点 / 49 隧道 / 26 规则 / 12 用户。
- 原始快照保留为 `prod-copy-original.db`（0400），本地 paneld 仅使用 `local.db` 副本。首次分页备份因持续写入重试，已停止本任务复制进程，未完成副本保留在 `tmp/incomplete-backup.db`。
- 复用 r2 paneld，仅监听 `127.0.0.1:16365`；dist 静态服务 `127.0.0.1:13022`，无 Vite dev server。
- 浏览器和静态代理均只允许列出的读取接口，其他 API 拦截 403；浏览器屏蔽外部地址。
- Chromium 使用现有系统 Playwright 库；所有临时文件均在任务目录，单浏览器、单 context 串行截图。

## 首轮视觉修正

- 已查看首轮桌面和 390px 截图：发现桌面列表被 Modal 下边界裁切，长名称在半列表单宽度内换行过多。
- 仅 grouped 列表按 dialog / viewport 内可用高度收缩；仅规则表单通过 `classNames.listbox` 让桌面下拉跨越两列。触发器和其他字段布局不变。
- 展开时将已选项所属分组带入视野；Tab 离开选择器时关闭 grouped 列表。
- 修正后再次串行 tsc / build，均通过；日志已更新。

- 最终只读复审发现 grouped 失焦回 trigger 与点击 toggle 竞争，已让 blur 仅处理离开容器，并允许 Shift+Tab 回 trigger 后用箭头重新进入列表。

## 最终截图与验证

- 代码 `172b9ebf`：`tsc --noEmit` 与完整 `npm run build`（含 PWA）通过。一轮构建在生成 PWA 前收到 SIGTERM（143），保留 `logs/build-interrupted.log`；单独重跑完整构建成功，未并行运行浏览器。
- 8 张指定文件均由最终 dist 生成并逐张查看；桌面 1440×1080，手机 390×844。列表未超出弹窗/视口，手机无横向溢出，选中项正常显示；所有隧道行 computed font-weight=400。
- 浏览器检查：箭头/Enter/Escape、Escape 保留编辑框、触发器再次点击收起、Tab 离开、返回 trigger 后箭头重入、外部点击关闭、ARIA group/option 与 aria-selected；无搜索输入框、展开后焦点为按钮；原生「转发模式」仍为 SELECT。无 pageerror，无写请求尝试。
- 当前快照 49 隧道均有地区，且无备注/多地区数据。第 06 张仅在浏览器响应里把隧道 3 的 exitRegions 置空并加演示备注、隧道 4 设为 HK/TW，用于验证提示/第二行/多地区 tooltip 和后缀；第 07 张普通用户 3 仅模拟隧道 3 空地区，确认没有管理员提示。未保存。
- 用户 3 的 12 条隧道仅有公开 entryGroups 元数据，分组不显示节点名/IP；截图可视内容亦不含节点名/IP。原有隧道名中 10/11/73 三条本身包含节点名，按范围保持原文，已在预览和总结披露。
- 新建指定 preview 的 picker 目录，发布 index.html + 8 PNG；逐文件 HTTP 200 与 SHA256/内容一致。未更改 preview unit 或其他目录。
- 日志：`logs/browser-screens.log`、`logs/browser-report.json`、`logs/preview-verification.json`；脚本保留在任务目录，未新增仓库前端测试。
- 前序代码 CI `989d0b0a`：[37450780523](https://github.com/ImoLR/FLVXR2/actions/runs/37450780523) 全部通过；最新 HEAD 推送后的状态另行确认。

## 清理与交付

- 已停止本任务 paneld 4185506 / 静态服务 4185507，Chromium/context 已关闭，未终止其他进程。
- 停机后 node/tunnel/forward/forward_port/user 全部行与原始快照一致。
- 中文 summary.md 已写到任务目录；最终 HEAD CI 结果将在推送完成后补入该外部总结，避免为了记录 run id 反复改变 HEAD。
- 无 tag/release/生产变更；仅新增 picker 预览目录，未覆盖原有目录内容。

## 分支 CI

- 包含全部最终代码和截图验收记录的 `a7847b2f` 已推送，CI Build Check [37451697319](https://github.com/ImoLR/FLVXR2/actions/runs/37451697319) 四项全部通过。
- 本完成记录仅更新 plan；推送该记录后仍会等待新的最终 HEAD CI，具体 SHA/run id 记录在任务目录 summary.md 与 logs/ci-final-head.json，不再为记录 run id 改变 HEAD。
- 全部计划任务完成；无 release 或生产部署，等待用户查看截图。

## 用户批准后的 fork.22 发布与生产部署

用户已查看截图并批准「可以，就这么发布」。以下为独立的 release-only 执行记录；不修改代码、不升级节点、不修改生产业务数据。

- [x] 确认发布代码 `17ed05fe6b9e58e93f04008199cb7e0e1ee17bd5` 的 CI Build Check [37451925796](https://github.com/ImoLR/FLVXR2/actions/runs/37451925796) 成功。
- [x] 在批准代码上创建并推送 annotated tag `3.0.27-fork.22`；等待镜像构建，核验正式 Latest release、资产集合、compose、安装脚本与 gost SHA256。
- [ ] 备份生产 compose / .env、SQLite 在线一致性快照及行数，保留旧镜像本地标签并写回滚说明。
- [ ] 安装 fork.22 v6 compose、更新 `FLUX_VERSION`，仅拉取并重建面板 backend / frontend。
- [ ] 验证容器健康、日志、在线节点指标推进、规则列表 API，以及线上前端新 hash / grouped listbox / 无旧搜索提示。
- [ ] 提交 `docs(plan): mark fork22 rollout complete` 并推送分支，写中文发布总结。

发布证据与最终总结：`/root/flvx-workers/runs/fork22-release/`。

- 2026-10-06 11:44:41 UTC：正式 [3.0.27-fork.22 Release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.22) 发布并确认为 Latest / 非 prerelease；镜像流水线 [37456500636](https://github.com/ImoLR/FLVXR2/actions/runs/37456500636) 成功。10 项资产名称与 fork.21 完全一致；两个 compose 镜像、两个脚本的 PINNED_VERSION / REPO、amd64 / arm64 gost SHA256 均通过。
