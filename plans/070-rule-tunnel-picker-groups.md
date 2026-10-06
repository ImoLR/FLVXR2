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
- [ ] 生产 SQLite 只读在线备份到任务目录；复用本地 paneld（仅 16365）与 dist 静态服务；浏览器拦截写请求。
- [ ] 单 Chromium / 单 context 串行检查交互与明暗主题，生成并逐张查看 8 张截图，修正缺陷后重拍。
- [ ] 发布截图与中文 index.html 到指定 preview 的 picker 子目录，记录验证结果并提交。
- [ ] 推送最终分支 HEAD，确认 CI Build Check 绿色（或如实记录 GitHub 阻塞状态）。
- [ ] 停止本任务启动的进程，写中文 summary.md（变更行号、验证、截图、发布状态、回退、建议）。

## 证据位置

- 任务目录：`/root/flvx-workers/runs/picker-groups/`
- 截图：任务目录 `screens/`；日志：`logs/`
- 预览：<http://104.145.236.29:8790/11598628d2503f952ceba408201d5eac/picker/>
- 不提交未跟踪的 `plans/048-aws-hk-fork4-agent-canary.md`；不编辑 memory。

## 本地构建

- `npx --no-install tsc --noEmit`：通过，日志 `logs/tsc.log`。
- `npm run build`：通过，日志 `logs/build.log`；未新增依赖或前端测试。
- 轻量只读复审：默认 native / searchable / multiple 的 JSX 与 class 字符串保留，新增内容仅 grouped 单选使用。
