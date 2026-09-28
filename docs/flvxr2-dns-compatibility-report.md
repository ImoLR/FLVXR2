# FLVXR2 DNS compatibility and log-volume investigation

Date: 2026-09-28

## Conclusion

The DNS failure is caused by FLVXR2's Linux-only nftables manager, not by AWS
DNS, MTU, routing, Go's resolver, or systemd-resolved itself.

FLVXR2 initializes that manager after each successful panel WebSocket
connection. Its `inet flvx` postrouting chain contained an unconditional
`masquerade` rule. Because the rule had no DNAT, source-network, output-interface,
or packet-mark condition, it also source-NATed host-local UDP queries sent to
`127.0.0.53`. In an isolated namespace reproducing the AWS addresses, a packet
sent from a normal local UDP socket to `127.0.0.53` arrived with source
`172.26.4.200` instead of a `127/8` address.

systemd-resolved 257.13 obtains the sender from `recvmsg().msg_name` and the
destination from `IP_PKTINFO`/`IPV6_PKTINFO`. On the main 127.0.0.53 stub it
rejects the packet when either address is not localhost (except the 127.0.0.54
proxy-stub path). It does not use the socket's configured local address or the
interface index for this decision. The rejected query receives no DNS reply, so
Go eventually reports a read timeout against `127.0.0.53:53`.

The fixed path is:

```text
FLVXR2-created prerouting DNAT
  -> conntrack sets the DNAT status bit
  -> postrouting matches `ct status dnat`
  -> masquerade only that forwarded connection

ordinary host lookup -> 127.0.0.53
  -> no DNAT status
  -> no masquerade
  -> source remains 127.0.0.1
  -> systemd-resolved accepts the query
```

FLVX does not contain FLVXR2's nftables manager or its startup initialization,
so it never installs the unconditional postrouting rule. That is the decisive
implementation difference.

## Source snapshot

Both repositories were cloned recursively from their current default `main`
branch before any edits. Neither repository has submodules.

| Repository | Commit | Commit date | Branch |
| --- | --- | --- | --- |
| FLVX | `129fa0aa4c718eea4381d3c4b8e53a9e113e71d1` | 2026-09-24 | `main` |
| FLVXR2 | `b9f61dff96883f8530364cf58e5fcfb74e08e014` | 2026-07-14 | `main` |

The repositories share merge base
`7134253b2c2410efb6dc56d03a7ddc6a3b31a09b` (2026-03-18). From that base,
the current heads have diverged by 299 FLVX-only commits and 849 FLVXR2-only
commits. A current-head comparison of `go-gost/`, `go-backend/`, and
`install.sh` covers 299 changed files, with 22,835 insertions and 28,530
deletions in the FLVXR2-to-FLVX direction. This is a divergent fork, not a
simple version bump.

### Go modules and checksums

| Tree | `go` / toolchain | Relevant GOST source | `go.mod` SHA-256 | `go.sum` SHA-256 |
| --- | --- | --- | --- | --- |
| FLVX `go-backend` | `go 1.25.0` | n/a | `12594a84a0e0dc81e5c88475251bb7f843a4ed3f1902eb383c122e0451be50bb` | `e207a4fe369f78b3c405810faf88dcba55ff44e5ae8a3c64ef411402db243c89` |
| FLVX `go-gost` | `go 1.25.0` | core `v0.3.1`, x declared `v0.5.3` | `aacb7e5c7a8d91b5704fc536c9d3de70a5b46c95f3e62cb1706a08638b3fcb5b` | `58f9b8e6d28717b4991fedd3e9311ac827fbe5b3d01ab514669381a32339261d` |
| FLVX `go-gost/x` | `go 1.25.0` | local module `github.com/go-gost/x` | `5ee1e7789a8973ff70067d4ac4b3434d731bcef0012a61e602b5190c58b7dd7f` | `01a50e4d55dc930f0441c398d84e1f0f02388812c32ac9b6989ee75ad46f51d8` |
| FLVXR2 `go-backend` | `go 1.24.0`, toolchain `go1.24.4` | n/a | `ef75d5725ffaff2dafb822fd6408cde7deae81da71ff72d0ca86f3f77ed25a96` | `a267a3f46f1ed50d28021737143bb79ebae6ba741cfc948fbe0d8d48c5cd45ad` |
| FLVXR2 `go-gost` | `go 1.23.0`, toolchain `go1.23.4` | core `v0.3.1`, x declared `v0.5.3` | `aa7bd8506f9122be67ec2e607384f42d799aab14a91536c935b1787437cb9779` | `88a636bd521ea0a9a590a0c4b21551ff33a6f8dce54db0496fcec81f00c6d210` |
| FLVXR2 `go-gost/x` | `go 1.22.0`, toolchain `go1.23.4` | local module `github.com/go-gost/x` | `c60a0f726e06493ca5230de3b0985bfbb15604fd17a6a934b6c247ba72d443fb` | `c5967e6c7dbdae9a3fcde26ab230e65c86eb48d254efc2db7970dfd718b13368` |

Both `go-gost/go.mod` files contain the same decisive replacement:

```go
replace github.com/go-gost/x => ./x
```

Thus the declared `github.com/go-gost/x v0.5.3` is not the code executed by
either agent. Each repository builds its own in-tree fork under `go-gost/x`.
Neither module has another `replace` directive.

## Call-chain evidence

FLVXR2's connection path is:

1. `WebSocketReporter.connect()` establishes the panel connection.
2. It calls `initNftablesManager()` unconditionally on Linux.
3. `initNftablesManager()` calls `nftables.NewManager()` whenever nftables is
   available; nftables forwarding does not need to be selected first.
4. `NewManager()` creates the `inet flvx` prerouting/postrouting NAT chains.
5. The old `initChains()` appended `&expr.Masq{}` as the complete postrouting
   rule expression list. This is equivalent to an unconditional `masquerade`.

The rule first entered the FLVXR2 branch in commit
`ba7441210320974d4e0761df7668b99633172f5f` (`add MASQUERADE to postrouting
chain`, 2026-05-18). FLVX never acquired the FLVXR2-only nftables manager after
the repositories diverged.

The exhaustive resolver/config search found:

- Both agents use the ordinary Go `net.LookupHost` path for TCP probe hostnames.
- Neither panel's generated Agent service/chain configuration attaches a GOST
  `resolver` or `nameservers` object.
- Neither fork hard-codes `127.0.0.53`, `127.0.0.54`, or parses
  `/etc/resolv.conf` in the Agent control path.
- Neither probe reuses a forwarding UDP socket for DNS.
- FLVXR2's extra public-IP fallback opens a UDP socket to discover the kernel's
  selected local address but sends no DNS packet and is not the trigger.
- The FLVXR2 UDP listener change after the fork marks queued pseudo-connections
  non-idle; it does not alter DNS bind addresses, `IP_PKTINFO`, or resolver
  sockets.
- The fork has the normal go-gost transparent/redirect UDP implementation, but
  that code is not on this failure path. The global postrouting NAT rule is.

## systemd-resolved condition

The exact Debian 13-relevant code was checked at upstream tag `v257.13`:

- `resolved-manager.c:manager_recv()` fills `p->sender` from the source sockaddr
  returned in `recvmsg().msg_name`.
- The same function fills `p->destination` from `IP_PKTINFO.ipi_addr` or
  `IPV6_PKTINFO.ipi6_addr`.
- `resolved-dns-stub.c:dns_stub_process_query()` prints the warning and returns
  when the main stub receives a packet whose sender or destination is not a
  localhost address. The 127.0.0.54 proxy address is the explicit exception.

Therefore the checked fields are the packet source and destination addresses.
The condition is not based on `RemoteAddr` in FLVXR2, the receiving socket's
configured local address, MTU, upstream DNS, or interface index.

## A/B explanation

On the Debian 11 host with static `nameserver 1.1.1.1` and `8.8.8.8`, Go sends
DNS directly to those addresses. No query reaches the systemd-resolved stub, so
that service cannot emit this particular localhost-range warning. The broad
MASQUERADE rule is still unsafe there; the static resolver merely hides this
specific symptom.

On Debian 13, Go follows `/etc/resolv.conf` to `127.0.0.53`. FLVXR2's old rule
source-NATs that host-local flow to the AWS interface address. resolved sees a
destination of `127.0.0.53` but a sender of `172.26.4.200`, rejects the query,
and Go reports an I/O timeout. Direct ICMP/TCP connectivity to public addresses
does not contradict this: the failure occurs before resolved sends an upstream
query to `172.26.0.2`.

## Implemented fix

The postrouting rule now consists of:

1. load conntrack status;
2. mask `IPS_DST_NAT` (`1 << 5`);
3. require the result to be non-zero;
4. masquerade.

At startup, any old unscoped MASQUERADE rule in FLVXR2's owned table is deleted
and replaced by the scoped rule. Existing installations therefore self-migrate
on Agent restart. DNAT forwarding retains the return-path SNAT it needs, while
ordinary local IPv4 and IPv6 traffic no longer matches.

No resolver, `/etc/resolv.conf`, systemd-resolved, route, MTU, VPC DNS, or host
firewall workaround is used.

## Log-volume hardening

The previous disk incident evidence showed that 91,671 of the last 100,000
syslog records came from `flvxx`. The dominant template was per-attempt success
output from periodic TCP probes, accompanied by raw `TcpPing` command logging,
DNS-start messages, resolved-IP messages, and per-probe summaries.

The Agent now:

- does not log routine `TcpPing`/`ServiceMonitorCheck` command bodies;
- does not print successful DNS resolution, selected IPs, each successful TCP
  attempt, or routine successful summaries;
- logs the first DNS/connectivity failure immediately;
- deduplicates the same failure class per target for one minute, including DNS
  timeouts whose ephemeral UDP source port changes;
- emits a periodic count of suppressed repetitions;
- emits one recovery event after a real failure, while routine successes stay
  silent, and resets suppression so a later regression is visible immediately;
- applies the same protection to recurring WebSocket-connect and network-stats
  failures.

As a second layer, the Agent's existing service-unit repair step now adds
`LogRateLimitIntervalSec=30s` and `LogRateLimitBurst=200` to the actual named
unit (including `flvxx.service`) and reloads systemd. It continues to preserve
the first error and important state changes. `install.sh` was inspected but not
edited because repository policy states that release CI overwrites it; runtime
hardening updates the deployed unit instead.

## Verification

Completed checks:

- `gofmt` on all changed Go files.
- `git diff --check` passed.
- Focused `go test -p 1 ./nftables ./socket` passed.
- Full `go-gost/x`: `GOMAXPROCS=1 go test -p 1 ./...` passed.
- Outer Agent module: `GOMAXPROCS=1 go test -p 1 ./...` passed.
- Isolated namespace migration test: a pre-existing unconditional rule was
  converted to exactly `ct status dnat masquerade`.
- Isolated 127.0.0.53 UDP test: source remained loopback after the Go manager
  initialized.
- Isolated two-network UDP test: client -> DNAT -> target succeeded; the target
  saw the router's target-side address and the reply returned to the client.

### Revalidation after migration to the new control host

The migrated worktree was revalidated on Debian 13 without touching the host
network configuration or any production node:

- Focused `go test -p 1 ./nftables ./socket` passed.
- Full `go-gost/x`: `GOMAXPROCS=2 go test -p 1 ./...` passed.
- Outer Agent module: `GOMAXPROCS=2 go test -p 1 ./...` passed.
- The integration test migrated a pre-existing unconditional rule to exactly
  `ct status dnat masquerade` in a disposable user/network namespace.
- A 127.0.0.53 loopback-stub equivalent retained a loopback source address.
- A private static-resolver equivalent resolved `probe.test` through a local
  DNS server and completed the domain TCP probe while the scoped rule existed.
- Isolated dual-network IPv4 and IPv6 tests passed for both TCP and UDP DNAT;
  the target observed the router's target-side address and replies returned to
  the client, confirming the required return-path masquerade remains intact.
- Domain, explicit IPv4, and explicit IPv6 TCP-probe tests passed.
- The dedupe tests cover first failure, suppression, changed error state,
  one-shot recovery, later regression, ephemeral DNS source ports, and systemd
  unit hardening idempotence.

The pristine upstream `3.0.28` snapshot has unrelated pre-existing release
gate failures. `go-backend` full tests fail in federation metadata,
`connectIp` reconstruction/diagnosis, release-channel normalization, backup
import/restore, legacy SQLite migration, and monitoring contracts. Setting the
project timezone and test license bypass removes the timezone-only failure but
does not remove those failures. A current clean frontend dependency install
also produces a 5.25 MB unminified bundle that exceeds Workbox's default 2 MiB
precache limit. These failures reproduce without the DNS/log commits and are
therefore recorded rather than hidden or changed as part of this focused fix.

No host DNS, resolver, route, firewall, MTU, or service configuration was
changed while testing. All packet tests ran inside disposable user/network
namespaces.

## Deployment check on an affected host

After building and deploying the patched Agent, restart only its service. Then
verify:

```bash
nft list chain inet flvx postrouting
# expected: ct status dnat masquerade

systemctl cat flvxx.service
# expected: LogRateLimitIntervalSec=30s and LogRateLimitBurst=200

journalctl -u systemd-resolved --since '10 minutes ago' \
  | grep -c 'unexpected (i.e. non-localhost)'

getent ahosts facseednet.imgamer.top
```

The warning count should stop increasing, and the hostname lookup should no
longer time out through `127.0.0.53`.

## Upstream source references

- systemd 257.13 stub check:
  <https://github.com/systemd/systemd/blob/v257.13/src/resolve/resolved-dns-stub.c#L898-L916>
- systemd 257.13 UDP packet metadata extraction:
  <https://github.com/systemd/systemd/blob/v257.13/src/resolve/resolved-manager.c#L864-L956>
- affected FLVXR2 source snapshot:
  <https://github.com/iKeilo/FLVXR2/tree/b9f61dff96883f8530364cf58e5fcfb74e08e014>
- normal FLVX comparison snapshot:
  <https://github.com/Sagit-chu/flvx/tree/129fa0aa4c718eea4381d3c4b8e53a9e113e71d1>
