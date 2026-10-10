# 089 — Node install via GitHub mirrors + honest end of install.sh (fork.40)

## Problem
- Nodes that cannot reach GitHub (mainland-China-only networks) cannot install:
  the panel install command, `install.sh` (binary download, self-update) and the
  agent OTA (`agentUpgradeCommandData`) all use only `https://github.com/...`.
  `install.sh` uses `wget -q` with no timeout → a blackholed GitHub hangs silently.
- End of `install_service` looked "stuck" and then printed a misleading message:
  - it polled config.json for `node_id` for 30 s with grep `"node_id":[0-9]*`, but the
    agent writes `"node_id": 5` (json.MarshalIndent, with a space) → the loop never
    matched and **always ran the full 30 s**;
  - then it POSTed `/api/v1/node/batch-reset-traffic` **without auth** → the panel answers
    `{"code":401,"msg":"未登录或token已过期"}` with HTTP 200 / curl exit 0 → the script
    printed that JSON and then `✅ 流量已归零`. The reset never worked.
  - `check_and_install_tcpkill` ran `apt update` + `apt install dsniff` silently with no
    timeout (minutes on a slow mirror).

## Design
- `install.sh`:
  - `GH_MIRRORS` (single variable near the top, prefix style
    `<mirror>/https://github.com/...`), tested from this host on 2026-10-10 against the
    fork.39 release assets: `https://ghfast.top/ https://gh-proxy.com/ https://gcode.hostcentral.cc/`
    (all three served gost-amd64 + .sha256 byte-identical; ghproxy.link returns an HTML
    page with HTTP 200 → not used, but handy for the garbage test).
  - `GH_PROXY=https://x/` env forces that prefix first.
  - One helper `gh_download <github-url> <out> [checksum-url]`: quick probe of GitHub
    (`curl -sIL --connect-timeout 5 -m 8`), direct first when reachable, otherwise prints
    `检测到无法直连 GitHub，使用加速镜像下载` and skips direct. Each attempt has a connect
    timeout + low-speed abort; prints the source. curl preferred, wget fallback.
  - sha256 check against `gost-<arch>.sha256` (format `<hex>  gost-<arch>`), checksum
    fetched through the same chain; mismatch / HTML / empty → next source; no checksum
    at all → warn and continue; never accept a mismatching file.
  - Used for install, update and the script self-update (self-update has no checksum asset).
  - End of install: no traffic reset, no 30 s wait; ≤15 s "等待节点连接面板" check
    (config.json `node_id` or journal `WebSocket 连接建立成功` since the service start) →
    `✅ 节点已连接面板` or `⚠️ 15 秒内未连上面板…`.
  - tcpkill/dsniff: prints `安装 tcpkill (dsniff)…`, `timeout 120` apt update /
    `timeout 180` install when `timeout` exists, warn on failure, continue.
    (Agent without tcpkill: `ForceClosePortConnections` logs `启动 tcpkill 失败` and returns;
    old connections on a removed port are just not force-cut.)
- Panel `buildNodeInstallCommand`: `{ curl direct || curl ghfast || curl gh-proxy || curl gcode; }
  && chmod +x ./install.sh && VERSION=<v> ./install.sh -a <addr> -s <secret>` (frontend appends
  ` -n <service>`; must stay at the end).
- Panel `agentUpgradeCommandData`: GitHub direct first, then the same mirrors, checksumUrls
  paired index-by-index (old agents already loop over the arrays; no go-gost change).

## Checklist
- [x] Mirror test from this host (done above, record timings)
- [x] install.sh: mirror chain + download helper + sha256 (install/update/self-update)
- [x] install.sh: end-of-install connect check, remove batch-reset-traffic
- [x] install.sh: tcpkill step with progress + timeouts
- [x] go-backend: install command fallback chain + OTA mirror URLs + tests
- [x] Frontend check (no own command builder)
- [x] Verification: bash -n / shellcheck, helper tests (a) direct (b) GitHub blackholed (c) garbage mirror (d) timings
- [x] Verification: end-of-install block (full install in a throwaway systemd container against a local panel if feasible)
- [ ] go test ./... (baseline 15)
- [ ] Commit, push, tag 3.0.27-fork.40, CI + release assets verified
- [ ] Prod backup + prune + upgrade /opt/flvx-svc, health + node state
- [ ] Prod install command run on this host up to the install.sh download (direct + GitHub blackholed)
- [ ] Plan marked complete
