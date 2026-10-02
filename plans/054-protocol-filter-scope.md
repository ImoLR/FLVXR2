# 隧道协议过滤按服务隔离修复计划

## 范围

本计划只修复协议过滤配置文件安全落盘、入口 GOST TCP forward 的隧道级隔离、失败 warning、支持范围提示及对应测试；不处理首读/分片绕过、UDP/nftables 过滤、tunnel-entrypoint relay 跳过或可观测性，也不做发布、部署或生产重启。

## 混合版本兼容

- Panel 继续发送现有 `SetProtocol` 命令，使 fork.7/fork.8 等旧 agent 保持原有节点全局行为。
- Panel 同时把隧道的四个过滤值显式写入每个入口 forward service（包括四项全为 0），新 agent 优先使用 service 自身值，不受 legacy 全局值覆盖或泄漏。
- 新 agent 对没有携带 service 级过滤标记的旧配置保留 legacy 全局回退；节点重连/重新下发 forward 时，现有 forward 重建路径会带上隧道值并自动收敛。
- service 四项全为 0 时不包装连接，避免 detector 开销。
- 已核实重连路径：`onNodeOnline` 调用 `redeployNodeRuntime`，后者经 `redeployTunnelAndForwards` / `syncForwardServices` 重新读取 tunnel 并生成带 service 级过滤值的 forward 配置。

## 清单

- [x] 从 `2991b893` 创建 `maintenance/3.0.27-fork.9-protocol-filter`，记录范围与混合版本兼容策略。
- [x] 安全保真地持久化 agent 协议配置，并添加未知字段、原子替换和权限单元测试。
- [x] Panel 在入口 forward service 中携带隧道过滤值，更新时复用 forward sync 并向调用方返回离线/失败 warning；添加生成配置测试并核实重连收敛路径。
- [x] Agent 按 service 使用过滤值，零值不包装；添加同节点双 service 隔离和 legacy 命令不覆盖测试。
- [x] 在隧道表单添加仅支持 GOST 模式 TCP forward 的文字说明。
- [x] 运行 go-backend、go-gost、go-gost/x 全量 Go 测试和前端构建，并将后端失败与 `2991b893` 基线比较。
- [x] 推送分支并完成实施记录（不发布、不部署）。

## 验证结果

- `go-gost`: `go test ./...` 通过。
- `go-gost/x`: `go test ./...` 通过。
- `vite-frontend`: `npm run build` 通过。
- `go-backend`: `go test ./...` 当前分支与 `2991b893` 基线均为相同的 19 个已知失败项，无新增失败。

## 发布与上线（3.0.27-fork.9）

- [x] 发布前复核：`go-gost/x` service/socket/parsing 测试通过，`go-gost` 构建通过，`vite-frontend` `npm run build` 通过；`go-backend` `go test ./...` 失败数 19，与已知基线一致。
  （`go-backend/internal/http/handler` 中唯一失败为已知的 `TestReconstructTunnelState_PreservesConnectIP`。）
- [x] 在 `146d0ec3` 打注释 tag `3.0.27-fork.9` 并推送；分支 "CI Build Check" 与 tag "Build and Push Images"（run 37000841936）均成功。
  （Release 为 Latest、非 prerelease，10 个资产与 fork.8 相同；compose 镜像为 `ghcr.io/imolr/flvxr2-svc-{backend,frontend}:3.0.27-fork.9`；
  install.sh/panel_install.sh 均为 `REPO="ImoLR/FLVXR2"`、`PINNED_VERSION="3.0.27-fork.9"`；gost-amd64 sha256 `929b3d55…744b` 与 .sha256 一致，`-V` 输出 `gost 3.0.27-fork.9`。）
- [x] 升级生产 panel `/opt/flvx-svc`（2026-10-02T11:34:11Z）。
  （回滚点 `/opt/flvx-svc/rollback/pre-fork9-20261002T112501Z`：compose + .env、在线备份 `gost.db.validated`（quick_check ok，sha256 `2036b50c…ad67`），
  镜像 `local/flvxx-{backend,frontend}:pre-fork9-20261002T112501Z`、ROLLBACK-METADATA.md。升级后 backend healthy、frontend 200；
  升级前在线的 22 个节点全部重连并上报新 metric（节点 1、24、28 升级前即离线）；重启瞬间的 "节点不在线" redeploy 报错为正常重连竞争。
  `/flow/upload` 180 秒窗口：计费/原始 = 1.000（forward/user/user_tunnel 均 +163 MB）。）
- [x] 通过 UI（headless Chromium → 63666）原样保存现有隧道 73：响应 code 0、无 warning，表单中出现 UDP/nftables 不过滤提示；
  tunnel/forward/forward_port 行内容不变（chain_tunnel 行 id 重建为既有行为，内容一致）。
- [x] Agent 金丝雀：节点 47 "AWS HK"（仅出口）经 panel 版本锁定 `/api/v1/node/upgrade` 于 11:37:49Z 升级，3 秒内以 `3.0.27-fork.9 (debian/amd64)` 重连；
  转发 102 诊断：入口→AWS HK 2.9 ms、AWS HK→目标 20.0 ms 均成功。
  节点 47 只做出口，不会收到 `SetProtocol`，fork.9 也不会改写它的 `config.json`；panel 无法读取 agent 的 `config.json`，且没有 SSH，所以无法直接查看它的字段和权限。节点能用该文件中的 secret 正常重连，可作为间接证据。
- [x] 入口节点（2/23/32 等）未升级，留给用户；顺序与验证方法见下。

### 入口节点升级（用户）

- 顺序：先选一个共享入口节点在低峰期升级（建议节点 23 "Mkcloud 沪日ixp 440"），确认正常后再升级 2 和 32，最后升级其余出口/链路节点。
- 方式：节点 → 升级（panel 锁定到 3.0.27-fork.9）。
- 每个节点的检查：在线且版本为 `3.0.27-fork.9`；其转发在转发页诊断通过；nftables 转发流量正常增长。
- 按隧道过滤验证（当前 49 个隧道都屏蔽 HTTP/TLS/SOCKS）：在同一入口节点选两个带 GOST TCP 转发的隧道 A、B，临时取消 A 的"屏蔽 HTTP"并保存（B 不动）；
  分别对 A、B 的转发入口端口发 `curl -v http://<入口IP>:<端口>/`：A 应能通到目标（或收到目标的响应），B 应被立即断开；
  升级前（旧 agent）两者会一起跟随最后一次保存的值。验证完把 A 改回原值并保存。
- 单节点回滚：`systemctl stop <svc> && cp /etc/<svc>/<svc>.old /etc/<svc>/<svc> && systemctl start <svc>`。
