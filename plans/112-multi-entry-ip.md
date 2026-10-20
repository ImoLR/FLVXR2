# 112 · Multi-entry IP: several IPs per node + pick a tunnel's entry IPs

Requested 2026-10-20. Panel-only (no go-gost / agent change, no OTA). Branch
`feature/3.0.27-multi-entry-ip` from 99cac160 (= prod fork.58). **No tag / release / prod
change** — the user approves screenshots first.

User: 「配置节点的时候 IP 可以填多个，创建隧道的时候可以选择入口 IP，用户在规则那里就看隧道选择了哪一个 IP。」
Real case: one VPS with two public IPv6 addresses.

## Findings (before coding)

- `node.extra_ips` **already exists** (upstream column, `text`, nullable, AutoMigrated, API field
  `extraIPs`, input 「额外IP地址」 hidden in the node form's 高级配置). It is already read by the
  admin 「监听IP」 select (comma split), the landing code (JSON array or comma list) and by
  `pickNodeAddressLan`. Prod (backup pre-fork58): **0 of 29 nodes** have it set.
- `pickNodeAddressLan` uses the first extra IP as the hop/exit dial address when a node has no
  intranet IP. It is used for connect type 「内网」 and for the default preference (empty
  `ip_preference` + empty connect type = 42 of 43 prod tunnels). Without a change, adding an
  extra IP to an exit/hop node would silently move tunnel dialing to that IP.
- `resolveForwardIngress` runs `net.JoinHostPort` on the whole tunnel `in_ip` list, so a
  multi-address list containing IPv6 comes out as `[a,b]:port` and the rule page shows broken
  brackets. 12 prod tunnels have `v4,v6` lists. Multi-IPv4 lists give `a,b:port`, which the copy
  modal turns into `[b:port]:port`.
- The UI's auto entry address (create mode) is one address per entry node
  (`v4 || v6 || intranet || serverIp`). The backend auto list (`buildTunnelInIP`) only runs when
  the submitted value is empty.

## Decisions

1. Reuse `node.extra_ips` (no new column). Canonical storage = JSON array text
   (`["2001:db8::2"]`), empty = NULL. Readers accept the JSON form and legacy comma/space/newline lists.
   Validation: literal IPv4/IPv6 only (brackets stripped, canonical `net.IP.String()`), trim, dedupe,
   reject duplicates of the primary `serverIp`/`serverIpV4`/`serverIpV6`, reject
   unspecified/loopback/multicast, max 16.
2. Extra IPs are never dialed implicitly: drop the extras fallback from `pickNodeAddressLan`
   (0 prod nodes affected). A hop uses an extra IP only when it is set as 「自定义连接 IP」.
3. Node form: one new 「附加 IP」 textarea under the IPv6 field. The old 高级配置 「额外IP地址」
   input edits the same column, so it is removed rather than duplicated (flagged for the user).
4. Tunnel form: a chip picker under 「入口地址」, grouped per entry node (IPv4 / IPv6 / 附加),
   two-way bound to the existing textarea. Nothing ticked = empty textarea = today's auto list.
   Hint: 「只决定用户看到和复制的入口地址，不改变监听：规则仍在节点所有地址上监听（除非设置了监听IP）」.
5. Rule display: `resolveForwardIngress` splits the list and joins host:port per address
   (`[v6]:port`). Users with partial entry permissions see the tunnel's picked addresses that belong
   to their allowed entry nodes, falling back to the node's `server_ip` as today.
6. Node-edit sync: primary-slot replacement unchanged. Extra IPs removed from the node are dropped
   from tunnel entry lists and forward bind IPs (paired 1:1 with newly added extras when the counts
   match, i.e. an edited IP is replaced). An empty result falls back to the auto rebuild (tunnels) /
   default listen-all (bind IPs). Forwards whose bind IP changed are redeployed. A change limited to
   extra IPs does not redeploy hop/exit tunnels or WG paths (extras are not dial addresses).
7. Hop/exit 「自定义连接 IP」: quick-fill chips listing the target node's IPs, shown only when
   that node has extra IPs (existing inputs unchanged).

## Checklist

- [x] Baseline `go test ./...` recorded (15 FAIL lines: handler 2, contract 13 incl. subtests)
- [x] Backend: extra IP parse/normalize/validate helper + node create/update wiring + tests
- [x] Backend: `pickNodeAddressLan` no longer dials extras + test
- [x] Backend: node-edit sync for extras (drop/replace, bind redeploy, no dial redeploy for
      extras-only) + `TunnelEntryInIPTx` removes extras of removed entry nodes + tests
- [x] Backend: forward list ingress formatting per address + restricted-user picks + tests
- [x] Backend: tunnel create/update with picked inIp + forward bind on an extra IP tests
- [x] Old data unchanged: DB copy without extras gives byte-identical tunnel in_ip and
      forward service configs before/after
- [x] Frontend: node form 「附加 IP」
- [x] Frontend: tunnel entry IP picker + edit-mode entry sync handles extras + outdated hint
- [x] Frontend: 监听IP options parse extras; hop/exit connect IP quick-fill
- [x] `npm run build` passes
- [x] Full `go test ./...` back at baseline
- [x] Screenshot gallery (desktop + H5 + dark dialogs) published to mmwx-preview
- [x] Summary written; branch pushed

## Results (2026-10-20)

- Commits on `feature/3.0.27-multi-entry-ip` (base 99cac160): 5dd8669d node extra IP validation + no
  implicit dial, 7145a88b node-edit sync for extras + bind redeploy, e0535528 forward list per-address
  formatting + partial-permission picks, a40d6df8 node form 「附加 IP」, a629b382 tunnel picker + hop
  quick-fill + listen IP options, 5c846839 tunnel create/update test, 9ab6e08e forward list query only
  widened for partial entry permission.
- No new column: the existing nullable `node.extra_ips` text column is reused (canonical JSON array).
  No migration, no agent change.
- Tests: 16 new Go tests pass. `go test ./...` = 15 FAIL lines, identical names to the baseline taken
  before any change (pre-existing: federation dual panel ×3, backup export/import ×5, legacy migration
  ×2, service monitor ×2, address-in-use, federation nodelay, renewal cycle). `npm run build` passes;
  `tsc --noEmit` clean; eslint error counts on edited pages unchanged.
- Old data: prod backup pre-fork58 copy, base vs branch: 43 tunnel `in_ip`, 408 agent commands of a
  full redeploy of all tunnels/rules and 31 rule-list addresses are byte-identical.
- Resource use: no new polling, no per-request extra queries on the common path. Rule list on the
  prod copy (31 rules, refreshed every 5 s while the page is open): 8.0–9.6 ms base vs 8.8–9.5 ms
  branch (noise), ~+80 KB allocations per call, from rules of users with partial entry permission.
  Node saves parse at most 16 IPs; sync work only runs when addresses change.
- Screenshots (synthetic data): `/root/flvx-workers/runs/multi-entry-ip/screens/`, published to
  mmwx-preview `.../multi-entry-ip/index.html`.
- Not released: waits for the user's screenshot approval (then panel-only release, no OTA).
