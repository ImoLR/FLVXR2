# 265 — 端口分配跳过本机利群客户端的监听端口（leikwan-port-skip）

Branch `fix/3.0.27-leikwan-port-skip` (from tag `3.0.27-fork.136`), release `maintenance/3.0.27-fork.137-leikwan-port-skip`
→ tag `3.0.27-fork.137`. Panel only, no agent OTA, no DB schema change, no UI change.

## Why
On 火山广州 / 百度云广州 / 阿里广州 / 火山上海 the LeiKwan client listens on the public ports of the LeiKwan forwards
(today 10015–10021 + 10044). The panel learns them (`node.leikwan_forwards`, agent report, plan 096) but every port
picker ignored them → a new rule/tunnel on such a node could get 10015 (gost 「端口被占用」, nft mode could steal the
LeiKwan traffic). Prod 11-16: 0 FLVX rows on those ports (checked read-only).

## Design
- `leikwan_forwards` `listenPort` of **all** entries (also `running=false`) = reserved ports of that node. The list is
  only rewritten by agent reports (from config files on disk); disconnect does not clear it → offline nodes keep it.
- One query per pick (`node.leikwan_forwards` of that node), parsed in memory; no new timers.
- Auto pickers skip the reserved set: `pickNodePortTx` (Pick/PickRandom), `GetUsedPortsOnNodeAsMap` (→ handler
  `getUsedPorts`, `pickTunnelPort`, `pickRandomPortForNode`), `ListUsedPortsOnNode` (federation pick + validate),
  `HasOtherForwardOnNodePortTx` (→ `resolvePortForNewEntryNode`), line monitor `pickLineMonitorPortTx`
  (auto check ports, rotate), `linemon.SuggestCustomPort` (custom host = a node address), landing `landingFreePort`
  (native) + `sharedMachineBusyPorts`, chain `usedPortsTx`, Reality guard new-port alloc, node-deploy random port.
- Manual / changed ports rejected: 「节点 X 端口 N 已被本机利群转发占用，请换一个」 in
  `validateForwardPortAvailability(Tx)` (forward create/update/batch/entry changes/tunnel-delete migration) and tunnel
  out/chain-hop manual ports, node-deploy manual listen port.
- Grandfathered: a (node, port) the same forward / tunnel / inbound already holds still saves (no move). Guard and
  chain "keep" checks do not consult the reserved set (existing ports never move automatically).
- Not touched: line monitor `existing`/`manual` ports (they watch a port that already listens, e.g. a LeiKwan port);
  wg_path (fixed 51820+i, no free-port pick); LeiKwan pipe (FLVX side listens on the landing node, the entry dials
  127.0.0.1:listen_port).
- Reserved list changes: the WS report path logs once per node/port (6 h rate limit) when a LeiKwan port is already
  held by an FLVX allocation; nothing moves.

## Results (build)
- Gates 11-16 20:58–21:08 TW: `go build`, `go vet` clean, `go test ./...` (SQLite) all ok; PostgreSQL 18
  repo / handler / ws / contract all ok; the 4 new LeiKwan tests pass on both dialects (8/8 subtests).
- Mutation check: with the reserved set disabled the new tests fail (auto port 10020, pick 10015, typed port accepted).
- Frontend: 0 diff vs fork.136; local `npm run build` ok (rc 0).
- Dry check on a scratch copy of the prod DB (21:1x TW, new code): on 火山广州 / 百度云广州 / 阿里广州 / 火山上海, 20
  rules picked in a row get 10000–10014 then 10022–10026 (never 10015–10021 / 10044); the used-port map holds all 8
  LeiKwan ports; the line-monitor check-port pick never lands on them. Copy deleted afterwards.

## Release (11-16 TW)
- CI 43872987828 / 43873261736 / 43873264874 green; tag `3.0.27-fork.137` → 420921fc on
  `maintenance/3.0.27-fork.137-leikwan-port-skip` (= fork.136 + this branch, fast-forward); Images 43873584648 green;
  Latest = fork.137, same 10 assets as fork.136, compose pinned, install scripts PINNED_VERSION ok, gost-amd64 sha ok.
- Backup `/opt/flvx-svc/rollback/pre-fork137-20261116T132717Z` (DB quick/integrity ok, image tags, rollback.sh);
  prune → kept pre-fork136 + pre-fork137.
- Panel swap 21:27:30–21:27:38 TW, healthy, 救命版 read-only mount kept. No agent OTA.
- After: 30/30 agents reconnected (30 diff syncs, 0 updated / 0 failed / 0 orphans), no offline flaps in 10 min,
  52 rules running, all counts unchanged, LeiKwan status + 8 ports still shown on the 4 nodes (admin node/list),
  backend 0 restarts, RSS 29 MiB (was 60 MiB before restart).
- Note: panel `node/upgrade` only hands out the panel's own version → tonight's warm-redundant OTA installs fork.137
  agents (go-gost 0-line diff vs fork.136); addendum appended to briefs/warm-redundant-ota.md.

## Tasks
- [x] Repo helper `LeiKwanReservedPorts(Tx)` + merge into repo pickers / used-port sets
- [x] Handler: manual-port rejection (forward, tunnel out/chain hops, node deploy) with grandfathering
- [x] Line monitor auto + custom suggestion skip; landing / chain / guard / shared skip
- [x] WS: rate-limited conflict log when the reserved list changes
- [x] Tests: repo + handler unit, contract (SQLite + PostgreSQL)
- [x] go vet, go test ./... (backend), npm run build (untouched)
- [x] Commit + push branch, CI green
- [x] Release branch `maintenance/3.0.27-fork.137-leikwan-port-skip`, tag `3.0.27-fork.137`, CI + Images + assets
- [x] Prod backup + rollback script + prune (keep 2); panel-only upgrade before 01:30 TW
- [x] Verify: 30 online, forwards running, rules unchanged, LeiKwan status on 4 nodes, picker dry-check on a prod DB copy
- [x] Ledger, memory notes, summary, cleanup
