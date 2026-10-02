# 062 — 规则列表隐藏「连接数」「有效期」列

## 背景
- 规则列表的「实时带宽」列已经带了当前连接数徽标(plan 061),单独的「连接数」列(当前/上限)重复。
- 规则有效期只能由管理员在编辑弹窗「高级功能」里设置,用户无法自行设置,列表里显示「有效期」没有意义。

## 范围
- `vite-frontend/src/pages/forward.tsx`:
  - 两种表格视图(按隧道分组的表格 + 紧凑表格)删除「连接数」「有效期」表头和单元格。
  - 卡片视图删除右上角有效期标签。
  - 删除不再使用的 `formatExpiryTime` / `isExpirySoon` / `ConnectionCountCell`。
- 不改:编辑弹窗里管理员的有效期 / 连接数限制输入、后端到期逻辑、API 字段。

## 任务
- [x] 删除表格两列 + 卡片有效期标签 + 无用辅助函数
- [x] `tsc --noEmit`、`npm run build` 通过;`eslint forward.tsx` 与基线一致(333 个原有问题,无新增)
- [x] 发布 `3.0.27-fork.14`(CI + release 资产校验)
- [x] 生产面板备份 + 升级到 fork.14 并验证前端已是新构建

## 发布记录
- `c80651bd` 的 [CI Build Check](https://github.com/ImoLR/FLVXR2/actions/runs/37065619669) 通过;注解标签 `3.0.27-fork.14` 指向该提交。
- [Build and Push Images](https://github.com/ImoLR/FLVXR2/actions/runs/37065834273) 通过;[release](https://github.com/ImoLR/FLVXR2/releases/tag/3.0.27-fork.14) 为 Latest、非 prerelease,10 个资产与 fork.13 一致。
- 两份 compose 的面板镜像均固定为 `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.14`;两脚本的 `REPO`/`PINNED_VERSION` 正确,amd64/arm64 GOST SHA256 均通过。
- 2026-10-02 UTC 生产备份:`/opt/flvx-svc/rollback/pre-fork14-20261002T212018Z/`,包含 compose、`.env`、`gost.db.validated`、62 张表行数及 `ROLLBACK-METADATA.md`;SQLite `quick_check=ok`,原镜像保留为 `local/flvxx-{backend,frontend}:pre-fork14-20261002T212018Z`。
- 21:26 UTC 安装发布的 v6 compose、更新 `FLUX_VERSION` 并仅升级面板 backend/frontend;后端 healthy,前端 HTTP 正常,两容器均无重启。22 个在线节点指标连续推进,原离线节点 1/24/28 不变,所有 agent 版本不变;`POST /api/v1/forward/list` 返回 HTTP 200/code 0/27 条规则。
- 启动阶段出现 54 条已知的「节点不在线」重部署提示;随后约 115 秒日志无错误。用户/节点/隧道/规则数量保持 11/25/49/27,未人工修改生产数据。
- 实际服务的入口 JS 从 `index-NM1hu024.js` 更新为 `index-lur6QsYy.js`,sw index revision 从 `df14b33f6425c68064bed4ed436c7a54` 更新为 `274d6752708decb374fc52617a1913fe`;新 bundle 中三个辅助函数、两组连接数/有效期表头和卡片有效期标签均已移除,管理员有效期编辑字段仍保留。
- 完整发布、备份及前端核验证据:`/root/flvx-workers/runs/fork14-release/`。回滚时恢复备份 compose/`.env`,拉取 fork.13 镜像并重新启动 backend/frontend;无结构变更,正常回滚不恢复数据库。
