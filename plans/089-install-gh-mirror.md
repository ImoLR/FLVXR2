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
- [x] go test ./... (baseline 15)
- [x] Commit, push, tag 3.0.27-fork.40, CI + release assets verified
- [x] Prod backup + prune + upgrade /opt/flvx-svc, health + node state
- [x] Prod install command run on this host up to the install.sh download (direct + GitHub blackholed)
- [x] Plan marked complete

## Results (round 2, 2026-10-10)
- `go test ./...` (go-backend, fork.40 head 9e0a38fd): 15 failing tests/subtests, list identical to the
  fork.39 final run (federation dual panel ×3, backup export/import ×4, legacy migrations ×2, renewal TZ,
  service monitor, federation nodelay, tunnel addr-in-use, …) → no new failures. Install/OTA tests
  (`TestPanelAgentInstallCommand*`, `TestPanelAgentUpgradeUsesExactPanelVersion`,
  `TestInstallerMirrorChainMatchesPanel`) pass.
- Round 1 timings (helper/full-install tests in throwaway containers): GitHub blackholed (iptables DROP) →
  one-line command falls back to ghfast.top in 5.5 s; install.sh probe 5 s then ghfast.top; old agent OTA
  with GitHub DROP: HEAD stalls 30 s (Go default dial timeout) then ghfast.top, 32 s total.
- CI: CI Build Check 38041187091 ✅, Build and Push Images 38041298912 ✅. Release `3.0.27-fork.40`
  published 09:42:05Z, Latest, not prerelease, same 10 assets as fork.39; install.sh / panel_install.sh
  `PINNED_VERSION="3.0.27-fork.40"`, `REPO="ImoLR/FLVXR2"`, `GH_MIRRORS` present, no
  `batch-reset-traffic`; install.sh asset = repo file apart from PINNED_VERSION; compose images
  `ghcr.io/imolr/flvxr2-svc-*:3.0.27-fork.40`; gost-amd64/arm64 sha256 match their `.sha256` assets
  (amd64 4dfc30ed…, arm64 ecd6cf7a…).
- Prod: rollback dir `/opt/flvx-svc/rollback/pre-fork40-20261010T110227Z` (compose + .env,
  `gost.db.validated` quick_check ok, 27 nodes / 26 forwards / 2.29 M node_metric), local tags
  `local/flvxx-{backend,frontend}:pre-fork40-20261010T110227Z` (= fork.39 images). prune-backups kept
  pre-fork40 + pre-fork39, removed pre-fork38 + fork.37 images. Upgraded 11:02:55Z: backend healthy,
  frontend 200, `/version.json` build `3.0.27-fork.40-…`, `/license/info` still 401. 26/27 nodes online
  (24 long-offline, as before), all 26 have node_metric < 60 s old.
- Prod install command (`POST /api/v1/node/install`, node 48, admin JWT) = the 4-source `{ … || … ; }`
  chain. Run on this host in a disposable `debian:trixie-slim` container (fetch block only + install.sh's
  `gh_download` for gost-amd64 + sha256; main()/agent install never run, secret not used):
  - direct: install.sh 0.13 s; probe OK → GitHub direct, sha256 OK, 0.32 s;
  - GitHub blackholed (`--add-host` github.com / release-assets / objects → 10.255.255.1): install.sh via
    ghfast.top after 4.7 s; probe fails → "检测到无法直连 GitHub，使用加速镜像下载" → checksum + binary
    from ghfast.top, sha256 OK, 1.8 s.
- OTA data: same `releaseAssetSourceURLs` as the install command (direct, ghfast.top, gh-proxy.com,
  gcode.hostcentral.cc; checksumUrls paired); prod backend binary contains the three mirror prefixes.
  No node OTA was triggered.
- Agent caveat (no go-gost change): OTA uses `http.Head`/`http.Get` with the default client — up to
  30 s per dead source on connect, and no overall timeout if a download stalls mid-transfer (panel
  waits ≤5 min). Suggestion for a future agent release: per-request timeouts.
- Cleanup: round-1 test container `flvx-installtest-bh` + image `local/flvx-installtest:1` removed.
