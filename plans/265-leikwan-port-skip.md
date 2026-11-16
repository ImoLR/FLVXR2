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

## Tasks
- [x] Repo helper `LeiKwanReservedPorts(Tx)` + merge into repo pickers / used-port sets
- [x] Handler: manual-port rejection (forward, tunnel out/chain hops, node deploy) with grandfathering
- [x] Line monitor auto + custom suggestion skip; landing / chain / guard / shared skip
- [x] WS: rate-limited conflict log when the reserved list changes
- [x] Tests: repo + handler unit, contract (SQLite + PostgreSQL)
- [x] go vet, go test ./... (backend), npm run build (untouched)
- [ ] Commit + push branch, CI green
- [ ] Release branch `maintenance/3.0.27-fork.137-leikwan-port-skip`, tag `3.0.27-fork.137`, CI + Images + assets
- [ ] Prod backup + rollback script + prune (keep 2); panel-only upgrade before 01:30 TW
- [ ] Verify: 30 online, forwards running, rules unchanged, LeiKwan status on 4 nodes, picker dry-check on a prod DB copy
- [ ] Ledger, memory notes, summary, cleanup
