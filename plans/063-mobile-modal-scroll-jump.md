# 063 — 手机端打开弹窗后页面跳回顶部

## 现象
手机上在用户管理页往下滑,点某个用户「编辑」→ 保存,弹窗关闭后页面停在最顶部,需要重新往下找。

## 根因
- Radix Dialog/Select 打开时用 react-remove-scroll 锁滚动,给 body 加 `body[data-scroll-locked] { overflow: hidden !important }`。
- 移动端样式(fork.7 单指滚动修复)让 `html, body, #root` 为 `overflow-x: clip`。html 不是 `overflow: visible` 时,body 的 overflow 不再传递给视口,body(高度 = 视口高)自己变成裁剪容器,文档高度塌到一屏,`window.scrollY` 被浏览器重置为 0;关闭弹窗后不会恢复。
- 实际上是 **打开弹窗那一刻** 就跳到顶部了,所有页面的弹窗/下拉在手机上都受影响,不只是用户页。

## 修复
`vite-frontend/src/styles/globals.css` 移动端媒体查询里:锁滚动时让 body 纵向保持 `overflow-y: visible`(横向仍 clip)。背景滚动仍由 react-remove-scroll 的 touch/wheel 拦截阻止。桌面端不受影响。

## 任务
- [x] 本地复现(Playwright 安卓模拟,412×915):滚到 2500 → 打开编辑弹窗即变 0
- [x] 修 CSS;复现脚本验证:打开 / 保存 / 取消后均停在 2500,弹窗打开时滑动背景不动
- [x] `npm run build` 通过
- [x] 发布 `3.0.27-fork.15`(CI + release 资产校验)
- [x] 生产面板备份 + 升级到 fork.15 并验证前端已是新构建

## 发布记录
- 已将 fork.15 rebase 到 fork.14 rollout-complete 提交 `b48c5107`,发布提交为 `9169a79d`;rebase 前后代码内容一致。
- [CI Build Check](https://github.com/ImoLR/FLVXR2/actions/runs/37067728015) 四项检查通过;注解标签 `3.0.27-fork.15` 指向该发布提交。
- [Build and Push Images](https://github.com/ImoLR/FLVXR2/actions/runs/37067861743) 通过;[release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.15) 为 Latest、非 prerelease,10 个资产与 fork.14 一致。
- 两份 compose 的镜像均固定为 `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.15`;两脚本的 `REPO=ImoLR/FLVXR2`、`PINNED_VERSION` 正确,amd64/arm64 GOST SHA256 和全部资产的 GitHub SHA256 均通过。
- 2026-10-02 UTC 生产备份:`/opt/flvx-svc/rollback/pre-fork15-20261002T214605Z/`,包含 compose、`.env`、一次在线备份 `gost.db.validated`、62 张表行数及 `ROLLBACK-METADATA.md`;SQLite `quick_check=ok`,原镜像保留为 `local/flvxx-{backend,frontend}:pre-fork15-20261002T214605Z`。
- 21:47 UTC 安装发布的 v6 compose、更新 `FLUX_VERSION` 并仅升级 backend/frontend;后端 healthy,前端 HTTP 正常,两容器均无重启。22 个在线节点指标在间隔约 38 秒的两次采样中全部推进,原离线节点 1/24/28 及所有 agent 版本不变;`POST /api/v1/forward/list` 返回 HTTP 200/code 0/27 条规则。
- 用户/节点/隧道/规则数量保持 11/25/49/27。启动期间出现 48 条已知「节点不在线」重部署提示,最后一条之后连续 159 秒日志无错误或告警;未人工修改生产数据库数据。
- 实际服务的 JS/CSS 分别由 `index-lur6QsYy.js` / `index-DyiwXFGW.css` 更新为 `index-Bsk11_SE.js` / `index-CNHEc6RQ.css`;sw index revision 从 `274d6752708decb374fc52617a1913fe` 更新为 `000ee20e049a935d912c3337c52c2991`,index/sw 均返回 `Cache-Control: no-cache`。
- 实际 CSS 的 `max-width:768px` 范围包含 `html body[data-scroll-locked]` 覆盖:默认 `overflow:visible!important`,支持 clip 时编译为等价的 `overflow:clip visible!important`。发布资产及前端证据均经独立只读复核。
- 完整证据及中文总结:`/root/flvx-workers/runs/fork15-release/`。回滚时恢复备份 compose/`.env`,拉取 fork.14 镜像并重新启动 backend/frontend;无结构变更,正常回滚不恢复数据库。待用户在真实手机上确认弹窗/下拉打开、保存及取消后位置保持,弹窗打开时背景仍不可滑动。
