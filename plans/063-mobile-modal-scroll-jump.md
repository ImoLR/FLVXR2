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
- [ ] 发布 `3.0.27-fork.15`(CI + release 资产校验)
- [ ] 生产面板备份 + 升级到 fork.15 并验证前端已是新构建
