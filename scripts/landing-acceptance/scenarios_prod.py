"""Production acceptance check with a temporary user (plan 110 rule: after a
landing release, run the matrix against prod on a native landing and a shared
server, then clean up).

Safety rules built in:
* JWTs are minted in memory from FLVX_JWT_SECRET (env); no password, no login.
* Only the temporary user `acc_prod_<ts>` and its own nodes are created and
  changed; existing users/servers/nodes/profiles are read-only. No route
  profile is assigned, no outbound/profile is created, WARP is never touched.
* Cleanup runs on every exit path (also SIGINT/SIGTERM): clients stopped,
  nodes deleted (shared deletes awaited), the user deleted, then a leftover
  check proves no row of the temporary user remains.
"""
import hashlib
import json
import os
import pathlib
import signal
import socket
import sqlite3
import subprocess
import sys
import time

import coreclient
from accept import Check, Report, expect, run_scenarios
from flvxapi import ADMIN, APIError, Identity, Panel, server_payload, wait_for

DAY_MS = 86400000
HERE = pathlib.Path(__file__).resolve().parent
STABLE_TABLES = ["node", "tunnel", "forward", "user_tunnel", "landing_server", "landing_server_user_group", "landing_outbound", "landing_route_profile", "landing_warp_account", "landing_egress_setting"]


class Ctx:
    pass


def now_ms():
    return int(time.time() * 1000)


def db(ctx):
    c = sqlite3.connect(f"file:{ctx.db_path}?mode=ro", uri=True, timeout=15)
    c.row_factory = sqlite3.Row
    return c


def rows(ctx, sql, args=()):
    c = db(ctx)
    try:
        return [dict(r) for r in c.execute(sql, args)]
    finally:
        c.close()


def digest(obj):
    return hashlib.sha256(json.dumps(obj, sort_keys=True, default=str).encode()).hexdigest()[:16]


def snapshot(ctx):
    """Stable view of everything the check must not change."""
    out = {"counts": {}, "hash": {}}
    c = db(ctx)
    try:
        for t in STABLE_TABLES + ["user", "landing_node", "landing_server_user", "user_server_quota"]:
            try:
                out["counts"][t] = c.execute(f'select count(*) from "{t}"').fetchone()[0]
            except sqlite3.OperationalError:
                out["counts"][t] = None
        servers = [dict(r) for r in c.execute("select * from landing_server order by id")]
        volatile = {"shared_caps", "shared_last_sync_at", "shared_collected_at", "shared_applied_revision", "shared_pushed_revision", "shared_last_push_at", "shared_desired_hash", "shared_sync_error", "updated_time", "egress_checked_at", "egress_state", "egress_error", "egress_ready", "egress_warnings", "revision", "egress_hash"}
        out["hash"]["landing_server"] = digest([{k: v for k, v in s.items() if k not in volatile} for s in servers])
        out["route_profiles"] = [(s["id"], s.get("route_profile_id")) for s in servers]
        nodes = [dict(r) for r in c.execute("select id, user_id, landing_server_id, protocol, port, inbound_tag, server_key, user_key, uuid, public_key, short_id, route_profile_id from landing_node where user_id <> ? order by id", (ctx.user.uid if getattr(ctx, "user", None) else -1,))]
        out["hash"]["landing_node_credentials"] = digest(nodes)
        out["node_override_profiles"] = [n["id"] for n in nodes if n.get("route_profile_id") is not None]
        users = [dict(r) for r in c.execute("select id, user, flow, num, exp_time, status from user order by id")]
        out["hash"]["users"] = digest([u for u in users if not u["user"].startswith("acc_prod_")])
        out["online_nodes"] = c.execute("select count(*) from node where status = 1").fetchone()[0]
    finally:
        c.close()
    return out


def ledger(ctx):
    uid = ctx.user.uid
    u = rows(ctx, "select in_flow, out_flow from user where id=?", (uid,))[0]
    nodes = {r["id"]: r for r in rows(ctx, "select id, landing_server_id, port, status, up_bytes, down_bytes, billed_in, billed_out, connections from landing_node where user_id=?", (uid,))}
    q = rows(ctx, "select daily_used_bytes, monthly_used_bytes from user_quota where user_id=?", (uid,))
    sq = {(r["server_kind"], r["server_ref_id"]): r for r in rows(ctx, "select server_kind, server_ref_id, used_in, used_out from user_server_quota where user_id=?", (uid,))}
    return {"user": u, "nodes": nodes, "quota": q[0] if q else {"daily_used_bytes": 0, "monthly_used_bytes": 0}, "server_quota": sq}


def raw_total(a, ids=None):
    return sum(n["up_bytes"] + n["down_bytes"] for i, n in a["nodes"].items() if ids is None or i in ids)


def settle(ctx, min_delta, base, ids, timeout=240, shared=False):
    prev, steady = None, 0
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if shared:
            sync(ctx)
        a = ledger(ctx)
        key = (a["user"]["in_flow"], a["user"]["out_flow"], raw_total(a, ids))
        grown = raw_total(a, ids) - raw_total(base, ids) >= min_delta
        steady = steady + 1 if key == prev and grown else 0
        if steady >= 3:
            return a
        prev = key
        time.sleep(5 if shared else 3)
    raise TimeoutError("ledger did not settle")


def sync(ctx):
    try:
        return ctx.api.call("landing/integration/sync", {}, timeout=120)
    except Exception as e:  # noqa: BLE001 - sync is best effort here
        return str(e)


def node_view(ctx, nid):
    return ctx.api.node(nid)


def wait_node(ctx, nid, pred, what, timeout=180, shared=False):
    def probe():
        if shared:
            sync(ctx)
        v = node_view(ctx, nid)
        return v if v and pred(v) else None
    return wait_for(what, probe, timeout=timeout, interval=5 if shared else 2)


def wait_conns_zero(ctx, nid, timeout=240):
    return wait_for(f"node {nid} connections drained", lambda: (lambda v: v if v and v["connections"] == 0 else None)(node_view(ctx, nid)), timeout=timeout, interval=5)


def links(ctx, nid):
    return {x["kind"]: x["url"] for x in ctx.api.links(nid, ctx.user)}


def client(ctx, link, port):
    c = coreclient.CoreClient(ctx.core, ctx.scratch / "clients", link, port).start()
    ctx.clients.append(c)
    return c


def stop(ctx, c):
    c.stop()
    if c in ctx.clients:
        ctx.clients.remove(c)


def dl(ctx, port, n=1 << 20, timeout=60):
    return coreclient.download(port, ctx.fixture_host, ctx.fixture_port, n, timeout=timeout)


def can_dl(ctx, port):
    return coreclient.try_download(port, ctx.fixture_host, ctx.fixture_port, n=4096, timeout=8)


def can_echo(ctx, port):
    return coreclient.try_echo(port, ctx.fixture_host, ctx.fixture_port + 1, timeout=8)


def user_row(ctx):
    return next(u for u in ctx.api.call("user/list", {}) if u["id"] == ctx.user.uid)


def user_update(ctx, **changes):
    u = user_row(ctx)
    keys = ["id", "user", "name", "flow", "num", "maxConnections", "maxClientIps", "expTime", "flowResetTime", "status", "renewalAmount", "balance", "autoRenew"]
    body = {k: u[k] for k in keys if k in u}
    body.update(changes)
    return ctx.api.call("user/update", body)


# ------------------------------------------------------------------ scenarios
def p0_baseline(ctx):
    r = ctx.report
    ctx.before = snapshot(ctx)
    r.ok("baseline snapshot of everything the check must not change", ctx.before)
    for sid in [ctx.native] + ([ctx.shared] if ctx.shared else []):
        v = ctx.api.server(sid)
        expect(r, f"server {sid} is active/online with the built-in default profile", v and v["status"] == "active" and v["online"] and (v.get("routeProfileId") or 0) == 0, {"name": v["name"], "online": v["online"], "routeProfileId": v.get("routeProfileId"), "flowType": v["flowType"], "ratio": v["trafficRatio"], "egress": v.get("egress", {}).get("state")})
    expect(r, "no outbound/profile/WARP rows exist before the check", all(ctx.before["counts"].get(t) in (0, None) for t in ("landing_outbound", "landing_route_profile", "landing_warp_account")), {t: ctx.before["counts"].get(t) for t in ("landing_outbound", "landing_route_profile", "landing_warp_account")})


def p1_user(ctx):
    r, api = ctx.report, ctx.api
    name = "acc_prod_" + time.strftime("%m%d%H%M%S")
    api.call("user/create", {"user": name, "pwd": "Acc-" + os.urandom(12).hex(), "name": "temporary landing acceptance user (auto-deleted)", "flow": 10, "num": 1, "status": 1, "expTime": now_ms() + DAY_MS, "flowResetTime": 1})
    row = next(u for u in api.call("user/list", {}) if u["user"] == name)
    ctx.user = Identity(row["id"], name, 1)
    ctx.state["user_id"] = row["id"]
    save_state(ctx)
    expect(r, "temporary user without grant sees no landing server", api.servers(ctx.user) == [], {"user_id": row["id"], "user": name})
    grant = [ctx.native] + ([ctx.shared] if ctx.shared else [])
    api.call("landing/user/save", {"userId": ctx.user.uid, "maxNodes": 2, "quotas": [], "serverIds": grant})
    seen = sorted(s["id"] for s in api.servers(ctx.user))
    expect(r, "direct per-user grant shows exactly the granted servers", seen == sorted(grant), seen)
    leak = sorted({k for s in api.servers(ctx.user) for k in ("egress", "routeProfileId", "egressState", "egressError") if k in s})
    expect(r, "user view: only the read-only 出站 label, no egress internals", not leak and all(s.get("egressLabel") for s in api.servers(ctx.user)), {"leak": leak, "labels": [s.get("egressLabel") for s in api.servers(ctx.user)]})


def p2_create(ctx):
    r, api = ctx.report, ctx.api
    a = api.call("landing/node/create", {"landingServerId": ctx.native, "name": "acc-native-ss", "protocol": "ss2022"}, ctx.user)
    ctx.nodes["native_ss"] = a["id"]
    second_server = ctx.shared or ctx.native
    b = api.call("landing/node/create", {"landingServerId": second_server, "name": "acc-shared-vless" if ctx.shared else "acc-native-vless", "protocol": "vless_reality_vision"}, ctx.user)
    ctx.nodes["shared_vless" if ctx.shared else "native_vless_user"] = b["id"]
    save_state(ctx)
    leak = [k for k in ("routeProfileId", "effectiveRouteProfileId", "effectiveRouteProfileName") if k in a]
    expect(r, "user create response hides route-profile internals", not leak, leak)
    msg = api.expect_error("landing/node/create", {"landingServerId": ctx.native, "name": "acc-3rd", "protocol": "ss2022"}, ctx.user)
    expect(r, "3rd node refused at max nodes 2", True, msg)
    msg = api.expect_error("landing/node/create", {"landingServerId": ctx.native, "name": "acc-3rd-admin", "protocol": "ss2022", "userId": ctx.user.uid})
    expect(r, "admin create-for-user respects the user's max nodes", True, msg)
    api.call("landing/user/save", {"userId": ctx.user.uid, "maxNodes": 4, "quotas": [], "serverIds": [ctx.native] + ([ctx.shared] if ctx.shared else [])})
    c = api.call("landing/node/create", {"landingServerId": ctx.native, "name": "acc-native-vless-admin", "protocol": "vless_reality_vision", "userId": ctx.user.uid})
    expect(r, "admin creates a node FOR the user (所属用户)", c["userId"] == ctx.user.uid, {"userId": c["userId"]})
    ctx.nodes["native_vless"] = c["id"]
    if ctx.shared:
        d = api.call("landing/node/create", {"landingServerId": ctx.shared, "name": "acc-shared-ss", "protocol": "ss2022"}, ctx.user)
        ctx.nodes["shared_ss"] = d["id"]
    save_state(ctx)
    for key, nid in ctx.nodes.items():
        shared = key.startswith("shared")
        v = wait_node(ctx, nid, lambda x: x["status"] == "active" and (not shared or x["remoteState"] == "active"), f"{key} active", timeout=240, shared=shared)
        expect(r, f"{key} active (port {v['port']})", True, {"status": v["status"], "remoteState": v.get("remoteState")})


def p3_connect(ctx):
    r = ctx.report
    port = 26680
    for key, nid in ctx.nodes.items():
        port += 1
        cl = client(ctx, links(ctx, nid)["direct"], port)
        res = wait_for(f"{key} carries traffic", lambda: dl(ctx, port, 4 << 20), timeout=90, interval=5)
        expect(r, f"{key}: 4 MiB byte-exact through the real landing", res["bytes"] == 4 << 20, res)
        expect(r, f"{key}: echo through the landing", can_echo(ctx, port), None)
        if key.startswith("native"):
            with coreclient.echo_open(port, ctx.fixture_host, ctx.fixture_port + 1):
                def online_ips():
                    data = ctx.api.call("landing/monitor/ips", {"nodeId": nid})
                    return data if (data.get("onlineIps") or 0) > 0 and data.get("ips") else None
                ip_view = wait_for(f"{key} real online IP reported", online_ips, timeout=60)
                expect(r, f"{key}: real online IPs have connections and a sample time",
                       all(x["connections"] > 0 for x in ip_view["ips"]) and ip_view["sampleAt"] > 0, ip_view)
                monitor = ctx.api.call("landing/monitor")
                monitored = next(n for s in monitor["servers"] for n in s["nodes"] if n["id"] == nid)
                expect(r, f"{key}: monitor shows a real online IP count",
                       (monitored.get("onlineIps") or 0) > 0, {"nodeId": nid, "onlineIps": monitored.get("onlineIps")})
        blocked = []
        for host, tport in (("127.0.0.1", 22), ("169.254.169.254", 80)):
            try:
                s = coreclient.socks_connect(port, host, tport, 5)
                s.sendall(b"GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
                blocked.append(len(s.recv(4096)) == 0)
                s.close()
            except Exception:  # noqa: BLE001 - refused = blocked
                blocked.append(True)
        expect(r, f"{key}: loopback + metadata targets blocked by the safety rule", all(blocked), blocked)
        stop(ctx, cl)


def p4_billing(ctx):
    r = ctx.report
    for group in (["native_ss", "native_vless"], [k for k in ("shared_ss", "shared_vless") if k in ctx.nodes]):
        if not group:
            continue
        ids = [ctx.nodes[k] for k in group]
        shared = group[0].startswith("shared")
        base = settle(ctx, 0, ledger(ctx), ids, shared=shared)
        port = 26690
        for k in group:
            port += 1
            cl = client(ctx, links(ctx, ctx.nodes[k])["direct"], port)
            dl(ctx, port, 8 << 20)
            stop(ctx, cl)
        a = settle(ctx, 16 << 20, base, ids, shared=shared)
        srv = ctx.api.server(ctx.shared if shared else ctx.native)
        mode, ratio = int(srv["flowType"] or 1), float(srv["trafficRatio"] or 1)
        ok, ev = True, {}
        for i in ids:
            n0, n1 = base["nodes"][i], a["nodes"][i]
            up, down = n1["up_bytes"] - n0["up_bytes"], n1["down_bytes"] - n0["down_bytes"]
            bi, bo = n1["billed_in"] - n0["billed_in"], n1["billed_out"] - n0["billed_out"]
            want = (int(up * ratio) * mode, int(down * ratio) * mode)
            exact = ratio == 1.0
            ok &= (bi, bo) == want if exact else abs(bi - want[0]) <= 64 * mode and abs(bo - want[1]) <= 64 * mode
            ev[i] = {"up": up, "down": down, "billed_in": bi, "billed_out": bo, "want": want}
        expect(r, f"{'shared' if shared else 'native'}: billed = raw x ratio x mode ({mode}, {ratio}) per direction", ok, ev)
        du = a["user"]["in_flow"] + a["user"]["out_flow"] - base["user"]["in_flow"] - base["user"]["out_flow"]
        billed = sum(ev[i]["billed_in"] + ev[i]["billed_out"] for i in ids)
        expect(r, f"{'shared' if shared else 'native'}: user total flow grows by exactly the landing bill", du == billed, {"user_delta": du, "billed": billed})
        dq = a["quota"]["daily_used_bytes"] - base["quota"]["daily_used_bytes"]
        expect(r, f"{'shared' if shared else 'native'}: daily quota usage = user delta", dq == du, {"daily": dq})
        key = ("landing", ctx.shared) if shared else ("node", srv["nodeId"])
        q0 = base["server_quota"].get(key, {"used_in": 0, "used_out": 0})
        q1 = a["server_quota"].get(key, {"used_in": 0, "used_out": 0})
        dsq = q1["used_in"] + q1["used_out"] - q0["used_in"] - q0["used_out"]
        expect(r, f"{'shared' if shared else 'native'}: per-server quota row = user delta", dsq == du, {"key": key, "delta": dsq})


def p5_limits(ctx):
    r, api = ctx.report, ctx.api
    for key in [k for k in ("native_ss", "shared_ss") if k in ctx.nodes]:
        nid = ctx.nodes[key]
        shared = key.startswith("shared")
        # Restore the node's own value afterwards: on a shared server 0 (= unlimited)
        # exceeds the mmwx share cap and is refused by design.
        original = node_view(ctx, nid)["maxConnections"]
        wait_conns_zero(ctx, nid)
        api.call("landing/node/update", {"id": nid, "maxConnections": 2})
        try:
            if shared:
                rev = api.server(ctx.shared)["revision"]
                wait_for("limit applied by Custom", lambda: (sync(ctx), api.server(ctx.shared)["sharedAppliedRevision"] >= rev)[1], timeout=240, interval=5)
            else:
                time.sleep(12)
            cl = client(ctx, links(ctx, nid)["direct"], 26700)
            held = wait_for("first session", lambda: coreclient.echo_open(26700, ctx.fixture_host, ctx.fixture_port + 1, 10), timeout=60, interval=3)
            try:
                second = can_echo(ctx, 26700)
                expect(r, f"{key}: max_connections 2 (one session) -> a 2nd concurrent session is refused, the 1st keeps working", not second and coreclient.echo_alive(held), None)
            finally:
                held.close()
            stop(ctx, cl)
        finally:
            api.call("landing/node/update", {"id": nid, "maxConnections": original})
            if shared:
                rev = api.server(ctx.shared)["revision"]
                wait_for("original limit applied by Custom", lambda: (sync(ctx), api.server(ctx.shared)["sharedAppliedRevision"] >= rev)[1], timeout=240, interval=5)
        expect(r, f"{key}: original connection limit ({original}) restored", node_view(ctx, nid)["maxConnections"] == original, original)


def p6_user_state(ctx):
    r = ctx.report
    keys = [k for k in ("native_ss", "shared_ss") if k in ctx.nodes]
    before = {k: links(ctx, ctx.nodes[k]) for k in keys}
    ports = {k: 26710 + i for i, k in enumerate(keys)}
    cls = [client(ctx, before[k]["direct"], ports[k]) for k in keys]
    orig = user_row(ctx)
    try:
        for label, change, restore, state in (
            ("disabled", {"status": 0}, {"status": 1}, "disabled"),
            ("flow exhausted", {"flow": 0}, {"flow": orig["flow"]}, "paused_quota"),
            ("expired", {"expTime": now_ms() - 60000}, {"expTime": orig["expTime"]}, "paused_expired"),
        ):
            user_update(ctx, **change)
            for k in keys:
                shared = k.startswith("shared")
                v = wait_node(ctx, ctx.nodes[k], lambda x: x["status"] == state, f"{k} {state}", timeout=60)
                gone = wait_for(f"{k} refuses traffic", lambda: (sync(ctx) if shared else None, not can_dl(ctx, ports[k]))[1], timeout=240 if shared else 60, interval=5)
                expect(r, f"user {label}: {k} stops ({state}) and refuses traffic", gone and v["status"] == state, {"status": v["status"]})
            user_update(ctx, **restore)
            for k in keys:
                shared = k.startswith("shared")
                wait_node(ctx, ctx.nodes[k], lambda x: x["status"] == "active" and (not shared or x["remoteState"] == "active"), f"{k} restored", timeout=240, shared=shared)
                ok = wait_for(f"{k} traffic again", lambda: (sync(ctx) if shared else None, can_dl(ctx, ports[k]))[1], timeout=240 if shared else 60, interval=5)
                expect(r, f"user {label} lifted: {k} active again, same port + credentials, traffic flows", ok and links(ctx, ctx.nodes[k]) == before[k], None)
    finally:
        for c in cls:
            stop(ctx, c)


def p7_delete(ctx):
    r, api = ctx.report, ctx.api
    for key, nid in list(ctx.nodes.items()):
        v = node_view(ctx, nid)
        if v is None:
            continue
        shared = key.startswith("shared")
        server = ctx.api.server(v["landingServerId"])
        port, host = v["port"], server["publicHost"]
        wait_conns_zero(ctx, nid)
        res = api.raw("landing/node/delete", {"id": nid}, ctx.user)
        expect(r, f"{key}: delete accepted", res.get("code") == 0, res.get("msg"))
        gone = wait_for(f"{key} deleted", lambda: (sync(ctx) if shared else None, node_view(ctx, nid) is None)[1], timeout=300 if shared else 60, interval=5)
        expect(r, f"{key}: row removed{' after Custom confirmed' if shared else ''}", gone, None)
        if not shared:
            closed = wait_for(f"{key} listener closed", lambda: not port_open(host, port), timeout=60, interval=3)
            expect(r, f"{key}: native listener {port} closed (port released)", closed, port)
        del ctx.nodes[key]
        save_state(ctx)


def port_open(host, port):
    try:
        socket.create_connection((host, port), 4).close()
        return True
    except OSError:
        return False


def p8_egress_invariants(ctx):
    r, api = ctx.report, ctx.api
    after = snapshot(ctx)
    keys = ("landing_outbound", "landing_route_profile", "landing_warp_account")
    expect(r, "no outbound / profile / WARP row was created", all(after["counts"].get(k) == ctx.before["counts"].get(k) for k in keys), {k: after["counts"].get(k) for k in keys})
    expect(r, "every landing server still on the built-in default profile", all((p or 0) == 0 for _, p in after["route_profiles"]) and not after["node_override_profiles"], after["route_profiles"])
    for sid in [ctx.native] + ([ctx.shared] if ctx.shared else []) + ctx.observe:
        v = api.server(sid)
        if v is None:
            continue
        e = v.get("egress") or {}
        r.ok(f"server {sid} egress view", {"supported": e.get("supported"), "state": e.get("state"), "reason": e.get("unsupportedReason"), "label": v.get("egressLabel"), "notice": v.get("sharedNotice")})


def p9_observe(ctx):
    r, api = ctx.report, ctx.api
    for sid in ctx.observe:
        v = api.server(sid)
        if v is None:
            r.ok(f"server {sid} not present", None)
            continue
        nodes = [n for n in api.nodes() if n["landingServerId"] == sid]
        expect(r, f"observed server {sid} ({v['name']}): no node shown as removed", all(n.get("remoteState") != "removed" or n.get("deletePending") for n in nodes), [{k: n.get(k) for k in ("id", "status", "remoteState", "pendingReason")} for n in nodes])
        r.ok(f"observed server {sid} state", {"online": v["online"], "notice": v.get("sharedNotice"), "syncError": v.get("sharedSyncError"), "core": (v.get("sharedCaps") or {}).get("core")})


# ------------------------------------------------------------------ cleanup
def save_state(ctx):
    ctx.state["nodes"] = dict(ctx.nodes)
    (ctx.scratch / "prod-state.json").write_text(json.dumps(ctx.state))


def cleanup(ctx):
    log = []
    for c in list(ctx.clients):
        c.stop()
    ctx.clients.clear()
    if getattr(ctx, "user", None) is None:
        return log
    api = ctx.api
    # Restore the user so deletes are not blocked, then remove every node.
    try:
        user_update(ctx, status=1, flow=10, expTime=now_ms() + DAY_MS)
    except Exception as e:  # noqa: BLE001
        log.append(f"user restore: {e}")
    for n in [n for n in api.nodes() if n["userId"] == ctx.user.uid]:
        deadline = time.monotonic() + 360
        while time.monotonic() < deadline:
            v = api.node(n["id"])
            if v is None:
                break
            try:
                if v["connections"] == 0 and not v.get("deletePending"):
                    api.raw("landing/node/delete", {"id": n["id"]}, ctx.user)
                sync(ctx)
            except Exception as e:  # noqa: BLE001
                log.append(f"node {n['id']}: {e}")
            time.sleep(5)
        log.append(f"node {n['id']}: {'deleted' if api.node(n['id']) is None else 'STILL PRESENT'}")
    try:
        res = api.raw("user/delete", {"id": ctx.user.uid})
        log.append(f"user delete: {res.get('code')} {res.get('msg')}")
    except Exception as e:  # noqa: BLE001
        log.append(f"user delete: {e}")
    return log


def leftovers(ctx, uid):
    out = {}
    for t, col in (("user", "id"), ("landing_node", "user_id"), ("landing_server_user", "user_id"), ("user_server_quota", "user_id"), ("landing_user_setting", "user_id"), ("user_quota", "user_id"), ("forward", "user_id"), ("user_tunnel", "user_id"), ("user_group_user", "user_id")):
        try:
            out[t] = rows(ctx, f'select count(*) as n from "{t}" where {col}=?', (uid,))[0]["n"]
        except sqlite3.OperationalError:
            out[t] = None
    return out


def run(args, selected):
    ctx = Ctx()
    ctx.scratch = pathlib.Path(args.scratch)
    ctx.scratch.mkdir(parents=True, exist_ok=True)
    url, secret = os.environ.get("FLVX_URL", "http://127.0.0.1:63665"), os.environ.get("FLVX_JWT_SECRET", "")
    if not secret or not args.db:
        raise SystemExit("FLVX_JWT_SECRET (env) and --db are required")
    ctx.api = Panel(url, secret)
    ctx.db_path = args.db
    ctx.core = args.core
    ctx.native, ctx.shared, ctx.observe = args.native_server, args.shared_server, args.observe_server
    ctx.fixture_host, ctx.fixture_port = args.fixture_host, args.fixture_port
    ctx.report = Report(args.report or ctx.scratch / "prod-report.json")
    ctx.clients, ctx.nodes, ctx.state = [], {}, {"started": time.time()}
    fixture = subprocess.Popen([sys.executable, str(HERE / "fixtures.py"), "serve", "0.0.0.0", str(args.fixture_port), str(args.fixture_port + 1)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)

    def on_signal(signum, frame):
        raise KeyboardInterrupt(f"signal {signum}")
    signal.signal(signal.SIGTERM, on_signal)
    scenarios = [
        ("P0", "baseline snapshot + servers ready on the default profile", p0_baseline),
        ("P1", "temporary user: no grant -> nothing; direct grant -> servers", p1_user),
        ("P2", "max nodes, admin create-for-user, nodes active (shared applied by mmwx)", p2_create),
        ("P3", "links carry real traffic through native + shared landings", p3_connect),
        ("P4", "billing exact on native and shared", p4_billing),
        ("P5", "connection limit enforced (native + shared)", p5_limits),
        ("P6", "user disabled / flow exhausted / expired stop and restore nodes", p6_user_state),
        ("P7", "delete releases ports (native immediately, shared after mmwx)", p7_delete),
        ("P8", "egress untouched: no profiles/outbounds/WARP, all default", p8_egress_invariants),
        ("P9", "observed servers read-only", p9_observe),
    ]
    try:
        time.sleep(0.5)
        run_scenarios(ctx, selected, scenarios)
    except KeyboardInterrupt as e:
        print("interrupted:", e)
    finally:
        log = cleanup(ctx)
        fixture.terminate()
        uid = getattr(getattr(ctx, "user", None), "uid", None)
        if uid:
            left = leftovers(ctx, uid)
            ctx.report.begin("PC", "cleanup: no row of the temporary user remains, nothing else changed")
            try:
                expect(ctx.report, "cleanup log", True, log)
                expect(ctx.report, "temporary user fully removed", all(v in (0, None) for v in left.values()), left)
                after = snapshot(ctx)
                same = {k: after["hash"][k] == ctx.before["hash"][k] for k in ctx.before["hash"]} if hasattr(ctx, "before") else {}
                expect(ctx.report, "existing servers, other users' nodes/credentials and users unchanged", all(same.values()), {"same": same, "counts_before": ctx.before["counts"], "counts_after": after["counts"]})
                ctx.report.end()
            except Check as e:
                ctx.report.end(f"check failed: {e}")
    print(ctx.report.matrix())
    return 0 if ctx.report.passed else 1
