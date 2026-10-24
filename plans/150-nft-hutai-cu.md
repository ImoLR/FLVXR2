# 150 — nftables forwarding dead on node 48 「沪台」 entries (gost works)

Brief: `/root/flvx-workers/briefs/nft-hutai-cu.md` (user 2026-10-24 evening TW: 「测试一下是 IPv4 的问题还是 IPv6 的问题，
还是 NFT Table 转发还没做完…去查一下然后就修复它」, release when fixed).
Run dir: `/root/flvx-workers/runs/nft-hutai-cu`. Base: fork.89 (`maintenance/3.0.27-fork.89-open-items-fix` 04e310cc).

## Facts found (read-only)
- The user's rule: forward **155** (@jiuwei, user 8, user_tunnel 370), tunnel 75 「拼车hinet3 利群沪台IPLC」 (type 1, entry node 48,
  in_ip `leikwan-hutai-ct.imgamer.top,leikwan-hutai-cu.imgamer.top` = AAAA only), port 32873, mode **nftables**,
  agent target `36.232.14.79:30000` (IPv4; `pqstw.imgamer.top` has only an A record).
  Created 10-23 16:32Z (00:32 TW 10-24), switched to gost 16:55Z, deleted; replaced by forward **156** (gost, port 25706,
  `pqstw.imgamer.top:30001` = ss2022 landing node 105 on PQS TW) at 17:04Z. Evidence: node 48 agent journal
  (AddNftablesRules … Target="36.232.14.79:30000", applied without error) + DB copies before/after (forward seq 154 → 156).
  Not touched.
- Node 48: NAT VPS, v4 only `10.3.100.20` (egress NAT 114.35.6.24 / 103.246.219.39, no inbound mapping for rule ports:
  `leikwan_status=absent`), public entry = 2 SLAAC IPv6 on eth0 (CT 240e main + default source, CU 2408 extra), one RA
  default route `mtu 1492`. Kernel 5.10, nft 0.9.8. Direct egress from the node works over v4 and v6 (both sources).
- Agent DNAT rule (`go-gost/x/nftables/manager.go dnatExpressions`): `th dport N dnat ip|ip6 to T` in table `inet flvx`;
  the NAT family is the target's. In an inet table the kernel only runs a NAT expression whose family equals the packet's
  (`nft_nat_inet_eval`), so an IPv6 packet hitting a rule with an IPv4 target is not translated, reaches INPUT, finds no
  listener → TCP RST / ICMPv6 port unreachable.
- Panel (`resolveTargetIP`): a domain target was resolved to `LookupHost()[0]`, ignoring which family the entry has; the
  form refused nftables + IPv6 target outright (assumed IPv4 entries) — the only combination that works on node 48.

## Reproduction on prod node 48 (throwaway admin rules 157–160 on tunnel 75, client = dev host 2602:ffb0:236:1b::a)
Echo/bulk server on the dev host (v4 104.145.236.29 + v6), probes with connected sockets.

| rule | mode | target | CT TCP | CU TCP | CT UDP | CU UDP |
|---|---|---|---|---|---|---|
| A 157 | nftables | IPv4 104.145.236.29 | refused | refused | refused | refused |
| B 158 | nftables | IPv6 2602:ffb0:236:1b::a | OK | OK | OK 4/5 | OK 4/5 |
| C 159 | gost | IPv4 | OK | OK | OK 5/5 | **FAIL 0/5** |
| D 160 | nftables | `one.one.one.one:53` (A+AAAA) → panel installed 1.1.1.1 | refused | refused | refused | fail |

- Bulk 150 KB up+down with sha check: B and C OK on CT and CU (slow path from the dev host: ~10–50 KB/s, 16 % retrans,
  RTT 400 ms — path quality, not FLVX; nft = one end-to-end TCP, gost splits it).
- C/CU UDP: tcpdump on the client: the echo reply leaves node 48 from **240e (CT)**:47303 although the client sent to
  2408 (CU) → dropped by the connected client socket (and by any NAT on the client side).
- CU path oddity (provider, not FLVX): every new inbound UDP flow to the CU address gets two 228/232-byte DTLS-looking
  datagrams (ClientHello/ServerHello) from the CU address within one RTT and again every ~5 s, also on ports with no rule
  and from node-48 ports nothing listens on. Harmless to real clients (invalid packets are dropped) but visible.
- IPv4 entry path: none exists for rule ports (NAT VPS) → covered by the netns lab instead.
- Node 48 egress: IPv4 leaves in Taiwan (HiNet NAT; 1.1.1.1 32 ms, 8.8.8.8 47 ms); IPv6 (CN 240e/2408 prefixes) reached
  the dev host but **not** Cloudflare/Google DNS over IPv6 (timeouts), curl -6 to ipify/baidu/qq failed. So on node 48 even an
  nftables rule to an IPv6 target does not leave through the IPLC's Taiwan exit; gost (in over IPv6, out over Taiwan IPv4)
  is the right mode there.
- Hypotheses: (a) **confirmed** (v6 entry → v4 target is impossible for kernel NAT); (b) no — the DNAT matches the port on
  every local address, only the address family limits it; (c) not for nft (conntrack reverse-NATs, masquerade picks CT, works) but **yes
  for gost UDP** (reply source = kernel default CT, see C/CU); (d) yes: panel never considered entry families (domain picks
  ips[0]; form blocked v6 targets); (e) no (B passes, counters/acct fine, machine-protect set idle); (f) no (rules pushed).

## Root cause
1. nftables mode = kernel DNAT, which cannot translate IPv6↔IPv4. Node 48 (and node 51 vmsilo 沪日首发) are reachable only
   over IPv6; the user's target was IPv4-only, so the rule was installed but could never match — silently dead. gost
   (userspace) accepts on IPv6 and dials IPv4, so it worked.
2. Separate agent bug found while testing: gost's UDP listener on `[::]` answers from the kernel's default source address,
   so UDP through node 48's second (CU) address was dead even in gost mode (TCP fine).

## Fix
- Panel (`go-backend/internal/http/handler/nft_family.go`):
  - entry families per entry node = the addresses users are given (tunnel entry list / rule listen IP / node address,
    same selection as the 转发版 links, new `repo.EntryHostsForNodes`), names resolved (3 s timeout);
  - every nftables push (create/update/resume, reconnect diff, 10-min DNS refresh) picks the target IP of a family the
    entry has (domain with A+AAAA on node 48 → the AAAA); literals unchanged; unknown families = old behaviour;
  - create/update in nftables mode: no matching family on some entry → saved as **gost** + note 「入口「…」只有 IPv6
    地址，目标只有 IPv4 地址；nftables（内核转发）不能在 IPv4 和 IPv6 之间转换，已自动改用 gost 转发」; dual-stack entry
    with a single-family target → stays nftables + note which entry addresses cannot be used;
  - batch tunnel change refuses to move an nftables rule onto entries that cannot carry it (reason shown, rule stays);
  - a push of a legacy dead rule logs one line.
  - Why auto-gost instead of refusing: it keeps the user's traffic working with no extra step; the note says why.
- Frontend: removed the client-side refusal of nftables + IPv6 target; the mode field's hint (only while nftables is
  selected) says nftables cannot cross IPv4/IPv6 and switches to gost automatically. Notes arrive as the existing ⚠️ toasts.
- Agent (`go-gost/x/internal/net/udp/sticky_linux.go`): wildcard UDP listeners enable IPV6_RECVPKTINFO/IP_PKTINFO and
  reply with IPV6_PKTINFO/IP_PKTINFO from the address the client used; bound sockets unchanged; falls back to the old
  send if that address vanished (SLAAC rotation); no-op outside Linux.

## Affected (prod copy 10-24 13:37Z)
- IPv6-only entries: node 48 (tunnels 75/78/79) and node 51 vmsilo 沪日首发 (tunnels 25–33, 39, 42, 43). An nftables rule
  to an IPv4 target there was dead; now saved as gost with the note. IPv6 targets work in nftables.
- Existing nftables rules (6: 6, 98, 106, 124, 125, 153) are all on IPv4 entries (nodes 2, 23): none affected, no notes.
- Multi-address UDP (agent fix): node 48 (CT + CU). No other node has extra IPs in the panel.

## Tasks
- [x] Read memory + AGENTS.md, fresh prod DB copy (read-only), find the user's rule(s)
- [x] Live reproduction on prod node 48 with throwaway admin rules (CT/CU × target v4/v6/domain × TCP/UDP, gost control)
- [x] Root cause stated with evidence (which of hypotheses a–f)
- [x] Netns lab reproducing the failing combination (before = fail)
- [x] Fix (panel and/or agent) + unit/contract tests (fail without fix)
- [x] Affected rules/nodes list from the prod copy
- [x] Full backend tests SQLite + PostgreSQL vs fork.89 baseline; agent tests; frontend tsc/build/lint
- [ ] Commit, push branch `maintenance/3.0.27-fork.90-nft-hutai`, tag `3.0.27-fork.90`, CI + release assets verified
- [ ] Prod panel backup (rollback dir) + prune + upgrade + health/node checks
- [ ] OTA only if the agent changed (node 48 canary first)
- [ ] Re-run throwaway tests on prod node 48, delete throwaway rules, node 48 resource numbers before/after
- [ ] Plan doc final notes + summary

## Tests so far
- Agent: `internal/net/udp` unit (127.0.0.2 / dual-stack / IPv6 loopback / fallback / cmsg parsing) — the plain socket
  reproduces the bug; `listener/udp` test fails with the fix reverted (127.0.0.2 i/o timeout) and passes with it.
- Netns lab `go-gost/x/internal/net/udp/netns_sticky.sh` (secondary v4 + deprecated secondary v6 on the node):

  | target | plain socket | StickySource |
  |---|---|---|
  | 10.232.1.1 | OK | OK |
  | 10.232.1.5 | FAIL | OK |
  | fd32:1::1 | OK | OK |
  | fd32:1::5 | FAIL | OK |
- Panel: `nft_family_test.go` (SQLite + PostgreSQL): pick per family, verdicts for node-48 / node-23 shapes, listen IP
  override, unresolvable entry/target unchanged, `EntryHostsForNodes` = link selection.
- Contract `tests/contract/nft_family_contract_test.go` (full router + mock node, asserts pushed commands): fails on fork.89
  (`create v4 target: warnings "" mode <nil>`), passes with the fix.
- Frontend: tsc 0 errors; eslint forward.tsx 17 errors / 643 warnings vs 17 / 646 on fork.89 (no new); `npm run build` ok.
- Full backend `go test ./...`: SQLite 17 ok packages, PostgreSQL 17 ok packages; failing set identical to fork.89's
  (TestBackupExportImportRestoreContracts ×4 subtests, TestServiceMonitorResults/…, federation dual-panel ×3, nodelay,
  legacy migrations ×2, renewal anchor, tunnel address-in-use — all pre-existing).
- Agent `go test ./internal/net/... ./listener/... ./handler/... ./landing/... ./socket/... ./service/... ./nftables/...`:
  all ok (landing needs a short TMPDIR: unix socket path limit, same as earlier releases); nftables netns integration
  `TestDNATNeverCrossesAddressFamilies` + accounting test pass.
- Screens (desktop light/dark + H5, 9/9 checks, demo panel offline in `unshare -n`):
  http://104.145.236.29:8791/ea923e5e348dfb9548d4d0909b6de1ee/nft-hutai/
