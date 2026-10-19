# Landing acceptance runner

End-to-end acceptance for FLVX landings (plan 110 matrix, extended by plan 107
「出站与路由」). It drives the real HTTP API as an admin and as normal users,
runs the real FLVX agent with the pinned fork Core (`mmwxc-core`, xray) and
connects through the generated SS2022 / VLESS Reality links with the same Core
as a client, verifying every payload byte.

Python 3 standard library only. No production data is read or written in local
mode; production mode only creates a temporary user and its nodes, and removes
them again.

## Local mode (isolated lab, root)

```
cd go-backend && go build -o /root/tmp-work/bin/paneld ./cmd/paneld
cd go-gost && CGO_ENABLED=0 go build -ldflags "-X main.version=3.0.27-fork.N" -o /root/tmp-work/bin/flvx-agent .
# pinned Core: https://github.com/ImoLR/mmwx-custom/releases/download/v1.4.59/mmwxc-core-linux-amd64
python3 scripts/landing-acceptance/accept.py local \
  --panel /root/tmp-work/bin/paneld --agent /root/tmp-work/bin/flvx-agent \
  --core /root/tmp-work/bin/mmwxc-core-linux-amd64 --scratch /root/tmp-work/landing-acceptance
# --only S4,E2  runs a subset (scenarios depend on users/nodes made by S2/S4)
# --keep        leaves the lab running; `accept.py cleanup --scratch DIR` removes it
```

Lab: network namespace `flvxacc` (agent + landing Core, 198.51.100.2 /
2001:db8:acc::2, default routes like a real server), host side 198.51.100.1
(+ .11–.13 as extra client source IPs) with the panel (`:26481`, temporary
SQLite), byte/echo fixtures on IPv4 and IPv6 (`:26482`/`:26483`), an `openssl
s_server` Reality destination (`:26443`) and a fake mmwx Custom on
`127.0.0.1:26490` (`customfake.py`: async apply, "old Helper" that never
applies, failure injection, egress capability toggles, synthetic counters).

| id | what is proven |
|----|----------------|
| S1 | shared caps: IPv4-only → public host IPv4; domain appears → switches to the domain; an admin's manual public host survives later Custom changes |
| S2 | no grant/group → no server; direct per-user grant; group grant; non-admin view hides egress internals; revoke → node auto-disabled and refuses traffic; re-grant → same port/credentials, traffic again |
| S3 | max nodes 2 → 3rd refused (user and admin create-for-user); admin creates FOR a user (所属用户); no grant → refused; non-admin create response hides route-profile internals |
| S4 | SS2022 + VLESS Reality direct links carry 2 MiB byte-exact through the real Core; private targets blocked; rule picked from 我的落地节点 + 转发版 link works; shared node applied by the async Custom; payload without capability has no `egress` key; old Helper → node 等待同步 with the not-ready reason, never "removed" |
| S5 | landing billed = raw ×2 (双向 ×1); rule → own landing billed once (landing only) while the rule keeps its own counters; daily/monthly quota and per-server quota equal the user delta; Custom counters billed for shared nodes; a node traffic cap pauses only that node and lifting it restores it |
| S6 | node max_connections (total socket budget: inbound + outbound, TIME_WAIT counted ~60 s) and max_online_ips enforced by the Core (3rd source IP refused); shared cap 50 refuses a node limit of 100 |
| S7 | user flow exhausted / expired / disabled → landing nodes stop (paused_quota / paused_expired / disabled), rule paused, shared node disabled in desired; lifting → same port + credentials, rule resumed, traffic again |
| S8 | native delete closes the listener and frees the port (reused); shared delete pending until Custom reports removed, port reused; 立即同步 pushes at once after a backoff |
| E1 | default profile → agent desired has no egress plan; share without capability → 不支持, assignment refused, payload unchanged |
| E2 | native: block rule (target IP+port) hot-applied; read-only 出站 label for users; node override 默认（直连）; 强制 IPv6 / 强制 IPv4 proven both ways (own family works, other-family literal cannot leave); back to default; Core process never restarted |
| E3 | shared: capability v1 → plan in desired with exactly this share's inbound tags; geo profile refused without geo data; policy 不允许 → plan withdrawn |
| R1 | Core/agent RSS, fds, threads, CPU, log and state-file size after the whole run |

## Production mode (temporary user, cleaned up on exit)

See `scenarios_prod.py`. It mints JWTs from the panel's `JWT_SECRET` (never a
password), creates `acc_prod_<ts>` with a direct grant on the given servers,
creates its nodes, moves real traffic through them to a fixture on this host,
checks billing in the read-only DB, exercises limits and stop/restore on the
temporary user only, deletes everything again and verifies no row of the
temporary user is left. Existing users, nodes, servers and profiles are only
read (before/after snapshots must be identical). It never assigns a route
profile, never creates outbounds/profiles and never registers WARP.
