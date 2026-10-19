"""Local-lab scenarios: plan 110 acceptance matrix S1-S8 plus plan 107
outbound/routing E1-E3 and a resource probe R1 (see README.md)."""
import json
import pathlib
import socket
import time
import urllib.request

import coreclient
import lab as labmod
from accept import Check, Report, expect, run_scenarios
from flvxapi import ADMIN, APIError, Identity, Panel, server_payload, wait_for

H, HTTP, ECHO = labmod.HOST_IP, labmod.HTTP_PORT, labmod.ECHO_PORT
DAY_MS = 86400000


class Ctx:
    pass


# ------------------------------------------------------------------ helpers
def fake(ctx, path, data=None):
    url = f"http://127.0.0.1:{labmod.CUSTOM_PORT}/_fake/{path}"
    body = json.dumps(data).encode() if data is not None else None
    req = urllib.request.Request(url, data=body, headers={"Content-Type": "application/json"}, method="POST" if body is not None else "GET")
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.load(r)


def desired(ctx, share_id):
    st = fake(ctx, "state")
    d = st["desired"].get(str(share_id))
    if not d:
        return None, None
    return json.loads(d["raw"]), d["raw"]


def sync(ctx):
    return ctx.api.call("landing/integration/sync", {})


def now_ms():
    return int(time.time() * 1000)


def make_user(ctx, name, flow=100, exp_days=30):
    api = ctx.api
    api.call("user/create", {"user": name, "pwd": "Acceptance-" + name + "-9", "name": "acceptance " + name, "flow": flow, "num": 10, "status": 1, "expTime": now_ms() + exp_days * DAY_MS, "flowResetTime": 1})
    row = next(u for u in api.call("user/list", {}) if u["user"] == name)
    who = Identity(row["id"], name, 1)
    ctx.users[name] = who
    return who


def user_row(ctx, uid):
    return next(u for u in ctx.api.call("user/list", {}) if u["id"] == uid)


def user_update(ctx, uid, **changes):
    u = user_row(ctx, uid)
    keys = ["id", "user", "name", "flow", "num", "maxConnections", "maxClientIps", "expTime", "flowResetTime", "status", "renewalAmount", "balance", "autoRenew"]
    body = {k: u[k] for k in keys if k in u}
    body.update(changes)
    return ctx.api.call("user/update", body)


def landing_user(ctx, uid, max_nodes, server_ids):
    return ctx.api.call("landing/user/save", {"userId": uid, "maxNodes": max_nodes, "quotas": [], "serverIds": server_ids})


def create_node(ctx, who, server_id, protocol, name, owner=None):
    body = {"landingServerId": server_id, "name": name, "protocol": protocol}
    if owner is not None:
        body["userId"] = owner
    return ctx.api.call("landing/node/create", body, who)


def nview(ctx, nid):
    return ctx.api.node(nid)


def wait_node(ctx, nid, pred, what, timeout=45, interval=0.5, do_sync=False):
    def probe():
        if do_sync:
            sync(ctx)
        v = nview(ctx, nid)
        return v if v and pred(v) else None
    return wait_for(what, probe, timeout=timeout, interval=interval)


def client(ctx, link, port, send_through=None):
    c = coreclient.CoreClient(ctx.core, ctx.lab.scratch / "clients", link, port, send_through=send_through).start()
    ctx.clients.append(c)
    return c


def stop(ctx, c):
    c.stop()
    if c in ctx.clients:
        ctx.clients.remove(c)


def db(ctx):
    return ctx.lab.db()


def ledger(ctx, uid):
    c = db(ctx)
    try:
        u = dict(c.execute("select in_flow, out_flow from user where id=?", (uid,)).fetchone())
        nodes = {r["id"]: dict(r) for r in c.execute("select id, port, status, up_bytes, down_bytes, billed_in, billed_out from landing_node where user_id=?", (uid,))}
        q = c.execute("select daily_used_bytes, monthly_used_bytes from user_quota where user_id=?", (uid,)).fetchone()
        sq = {(r["server_kind"], r["server_ref_id"]): dict(r) for r in c.execute("select server_kind, server_ref_id, used_in, used_out, paused from user_server_quota where user_id=?", (uid,))}
        fw = {r["id"]: dict(r) for r in c.execute("select id, in_flow, out_flow, status, landing_auto_paused from forward where user_id=?", (uid,))}
    finally:
        c.close()
    return {"user": u, "nodes": nodes, "quota": dict(q) if q else {"daily_used_bytes": 0, "monthly_used_bytes": 0}, "server_quota": sq, "forwards": fw}


def settle(ctx, uid, min_raw_delta, base, timeout=90):
    """Wait until the ledger stops moving and landing raw grew by >= min_raw_delta."""
    prev, steady = None, 0
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        a = ledger(ctx, uid)
        raw = sum(n["up_bytes"] + n["down_bytes"] for n in a["nodes"].values())
        base_raw = sum(n["up_bytes"] + n["down_bytes"] for n in base["nodes"].values())
        key = (a["user"]["in_flow"], a["user"]["out_flow"], raw, json.dumps(a["forwards"], sort_keys=True))
        steady = steady + 1 if key == prev and raw - base_raw >= min_raw_delta else 0
        if steady >= 3:
            return a
        prev = key
        time.sleep(2)
    raise TimeoutError("ledger did not settle")


def wait_conns_zero(ctx, nid, timeout=150):
    """The fork Core counts every TCP socket of a node (inbound + outbound,
    TIME_WAIT included, ~60 s), so limit and delete checks start from zero."""
    return wait_for(f"node {nid} connections drained", lambda: (lambda v: v if v and v["connections"] == 0 else None)(nview(ctx, nid)), timeout=timeout, interval=2)


def links(ctx, nid, who):
    return {x["kind"]: x["url"] for x in ctx.api.links(nid, who)}


def tcp_open(host, port, timeout=2):
    try:
        socket.create_connection((host, port), timeout).close()
        return True
    except OSError:
        return False


def ns_listening(port):
    out = labmod.sh("ip", "netns", "exec", labmod.NS, "ss", "-Hltn", f"sport = :{port}", check=False).stdout
    return bool(out.strip())


# ---------------------------------------------------------------- lab setup
def setup(ctx):
    L = ctx.lab
    L.preflight()
    L.network()
    L.fixtures()
    L.panel()
    api = ctx.api = Panel(L.panel_url, L.jwt_secret)
    (L.scratch / "lab.json").write_text(json.dumps({"panel": L.panel_url, "jwt_secret": L.jwt_secret}))
    (L.scratch / "lab.json").chmod(0o600)
    api.call("node/create", {"name": "acceptance landing node", "serverIp": labmod.NODE_IP, "serverIpV4": labmod.NODE_IP, "serverIpV6": labmod.NODE_IP6, "port": "31000-31999"})
    c = L.db()
    row = c.execute("select id, secret from node where server_ip = ?", (labmod.NODE_IP,)).fetchone()
    c.close()
    ctx.node_id = row["id"]
    L.start_agent(row["id"], row["secret"])

    def online():
        n = next((n for n in api.call("node/list", {}) if n["id"] == ctx.node_id), None)
        if not n or n["status"] != 1:
            return False
        c = L.db()
        try:
            return c.execute("select landing_capable from node where id=?", (ctx.node_id,)).fetchone()[0]
        finally:
            c.close()
    wait_for("agent online + landing capable", online, timeout=60)
    s = api.call("landing/server/enable", {"nodeId": ctx.node_id})
    api.call("landing/server/save", server_payload(s, portMin=30000, portMax=30099, realityDest=f"{H}:{labmod.REALITY_PORT}", realityServerNames=[labmod.REALITY_SNI], maxNodesPerUser=10, status="active"))
    ctx.native = s["id"]
    fake(ctx, "share", {"id": 7001, "name": "fake async share", "hosts": {"ipv4": "203.0.113.71", "ipv6": "", "domain": ""}, "port_min": 40000, "port_max": 40099, "max_connections": 50, "_cfg": {"mode": "async", "delay": 2.0}})
    fake(ctx, "share", {"id": 7002, "name": "fake old helper", "hosts": {"ipv4": "203.0.113.72", "ipv6": "", "domain": "old.example.test"}, "port_min": 41000, "port_max": 41099, "core": {"state": "stopped", "version": ""}, "_cfg": {"mode": "old_helper"}})
    api.call("landing/integration/save", {"baseUrl": f"http://127.0.0.1:{labmod.CUSTOM_PORT}", "token": L.custom_token})
    servers = wait_for("shared servers listed", lambda: (lambda ss: ss if len(ss) == 3 else None)(api.servers()), timeout=30)
    ctx.shared = next(x["id"] for x in servers if x.get("remoteServerId") == 7001)
    ctx.oldhelper = next(x["id"] for x in servers if x.get("remoteServerId") == 7002)
    for sid in (ctx.shared, ctx.oldhelper):
        api.call("landing/server/save", server_payload(api.server(sid), status="active", maxNodesPerUser=10))
    wait_for("native Core running", L.core_pids, timeout=90)
    ctx.core_pids0 = L.core_pids()
    ctx.users = {}
    ctx.nodes = {}


# --------------------------------------------------------------- scenarios
def s1_hosts(ctx):
    r, api = ctx.report, ctx.api
    v = api.server(ctx.shared)
    expect(r, "IPv4-only caps -> public host is the IPv4", v["publicHost"] == "203.0.113.71", v["publicHost"])
    fake(ctx, "share", {"id": 7001, "hosts": {"ipv4": "203.0.113.71", "ipv6": "", "domain": "s7001.example.test"}})
    sync(ctx)
    v = wait_for("domain adopted", lambda: (lambda x: x if x["publicHost"] == "s7001.example.test" else None)(api.server(ctx.shared)), timeout=20)
    expect(r, "domain appears -> auto public host switches to the domain", True, v["publicHost"])
    api.call("landing/server/save", server_payload(v, publicHost="manual.example.test"))
    fake(ctx, "share", {"id": 7001, "hosts": {"ipv4": "203.0.113.71", "ipv6": "", "domain": "s7001b.example.test"}})
    sync(ctx)
    time.sleep(1)
    v = api.server(ctx.shared)
    expect(r, "admin's manual public host survives a later Custom host change", v["publicHost"] == "manual.example.test", v["publicHost"])
    # Old-Helper share: domain from caps.
    o = api.server(ctx.oldhelper)
    expect(r, "domain + IPv4 caps -> domain used", o["publicHost"] == "old.example.test", o["publicHost"])


def s2_grants(ctx):
    r, api = ctx.report, ctx.api
    u1, u2, u3 = make_user(ctx, "acc_u1"), make_user(ctx, "acc_u2"), make_user(ctx, "acc_u3")
    for u in (u1, u2, u3):
        expect(r, f"{u.user} without grant/group sees no landing server", api.servers(u) == [], [s["id"] for s in api.servers(u)])
    landing_user(ctx, u1.uid, 10, [ctx.native, ctx.shared, ctx.oldhelper])
    seen = sorted(s["id"] for s in api.servers(u1))
    expect(r, "direct per-user grant -> u1 sees exactly the granted servers", seen == sorted([ctx.native, ctx.shared, ctx.oldhelper]), seen)
    leak = sorted({k for s in api.servers(u1) for k in ("egress", "routeProfileId", "egressRestartOk", "egressState", "egressError", "egressReady", "egressCheckedAt") if k in s})
    expect(r, "non-admin server view hides egress internals (only the 出站 label)", not leak and all("egressLabel" in s for s in api.servers(u1)), leak)
    api.call("group/user/create", {"name": "acc-group", "status": 1})
    c = db(ctx)
    gid = c.execute("select id from user_group where name='acc-group'").fetchone()[0]
    c.close()
    api.call("group/user/assign", {"groupId": gid, "userIds": [u3.uid]})
    api.call("landing/server/save", server_payload(api.server(ctx.native), userGroupIds=[gid]))
    expect(r, "group-only grant still works (u3 via group sees native)", [s["id"] for s in api.servers(u3)] == [ctx.native], [s["id"] for s in api.servers(u3)])
    expect(r, "u2 (no grant, no group) still sees nothing", api.servers(u2) == [], None)
    n = create_node(ctx, u1, ctx.native, "ss2022", "u1-native-ss")
    ctx.nodes["u1_ss"] = n["id"]
    before = links(ctx, n["id"], u1)
    landing_user(ctx, u1.uid, 10, [ctx.shared, ctx.oldhelper])
    expect(r, "revoke grant -> server disappears for u1", ctx.native not in [s["id"] for s in api.servers(u1)], None)
    v = wait_node(ctx, n["id"], lambda x: x["status"] == "disabled", "revoked node disabled")
    expect(r, "revoke -> u1's existing node auto-disabled (kept, not deleted)", v["status"] == "disabled" and v["autoDisabled"], {k: v[k] for k in ("status", "autoDisabled")})
    cl = client(ctx, before["direct"], 26580)
    blocked = wait_for("revoked node refuses traffic", lambda: not coreclient.try_echo(26580, H, ECHO), timeout=30)
    expect(r, "revoked node refuses connections", blocked, None)
    landing_user(ctx, u1.uid, 10, [ctx.native, ctx.shared, ctx.oldhelper])
    v = wait_node(ctx, n["id"], lambda x: x["status"] == "active", "re-granted node active")
    expect(r, "re-grant -> node active again with the same port + credentials", links(ctx, n["id"], u1) == before and v["port"] == n["port"], v["port"])
    ok = wait_for("re-granted node accepts traffic", lambda: coreclient.try_echo(26580, H, ECHO), timeout=30)
    expect(r, "re-granted node accepts connections", ok, None)
    stop(ctx, cl)


def s3_limits_count(ctx):
    r, api = ctx.report, ctx.api
    ul = make_user(ctx, "acc_lim")
    landing_user(ctx, ul.uid, 2, [ctx.native])
    a = create_node(ctx, ul, ctx.native, "ss2022", "lim-1")
    b = create_node(ctx, ADMIN, ctx.native, "vless_reality_vision", "lim-2-by-admin", owner=ul.uid)
    expect(r, "admin creates a node FOR the user (所属用户) -> owned by the user", b["userId"] == ul.uid, {"userId": b["userId"]})
    msg = api.expect_error("landing/node/create", {"landingServerId": ctx.native, "name": "lim-3", "protocol": "ss2022"}, ul)
    expect(r, "user's 3rd node refused at max nodes 2", True, msg)
    msg = api.expect_error("landing/node/create", {"landingServerId": ctx.native, "name": "lim-3-admin", "protocol": "ss2022", "userId": ul.uid})
    expect(r, "admin create-for-user also respects the user's max nodes", True, msg)
    u2 = ctx.users["acc_u2"]
    msg = api.expect_error("landing/node/create", {"landingServerId": ctx.native, "name": "u2-admin", "protocol": "ss2022", "userId": u2.uid})
    expect(r, "admin create-for-user refused when the user has no grant", True, msg)
    owners = sorted(n["userId"] for n in api.nodes(ul))
    expect(r, "user's node list shows exactly its 2 nodes", owners == [ul.uid, ul.uid], owners)
    created = api.raw("landing/node/create", {"landingServerId": ctx.native, "name": "u1-leakcheck", "protocol": "ss2022"}, ctx.users["acc_u1"])
    leak = [k for k in ("routeProfileId", "effectiveRouteProfileId", "effectiveRouteProfileName") if k in (created.get("data") or {})]
    expect(r, "non-admin create response hides route profile internals", created.get("code") == 0 and not leak, leak)
    ctx.api.call("landing/node/delete", {"id": created["data"]["id"]}, ctx.users["acc_u1"])
    for n in (a, b):
        api.call("landing/node/delete", {"id": n["id"]})


def s4_connect(ctx):
    r, api = ctx.report, ctx.api
    u1 = ctx.users["acc_u1"]
    v = create_node(ctx, u1, ctx.native, "vless_reality_vision", "u1-native-vless")
    ctx.nodes["u1_vless"] = v["id"]
    for key, port in (("u1_ss", 26581), ("u1_vless", 26582)):
        nid = ctx.nodes[key]
        cl = client(ctx, links(ctx, nid, u1)["direct"], port)
        res = coreclient.download(port, H, HTTP, 2 << 20)
        expect(r, f"{key}: direct link carries 2 MiB through the real Core (byte-exact)", res["bytes"] == 2 << 20, res)
        expect(r, f"{key}: echo through the link", coreclient.try_echo(port, H, ECHO), None)
        expect(r, f"{key}: private/loopback target blocked by the safety rule", not coreclient.try_download(port, "127.0.0.1", HTTP), None)
        stop(ctx, cl)
    # Rule -> own landing (转发版 link).
    api.call("tunnel/create", {"name": "acc-port-forward", "type": 1, "flow": 2, "trafficRatio": 1, "status": 1, "inIp": labmod.NODE_IP, "inNodeId": [{"nodeId": ctx.node_id, "protocol": "tls", "strategy": "round"}]})
    c = db(ctx)
    ctx.tunnel = c.execute("select id from tunnel where name='acc-port-forward'").fetchone()[0]
    c.close()
    api.call("tunnel/user/assign", {"userId": u1.uid, "tunnelId": ctx.tunnel, "flow": 100, "num": 10, "expTime": now_ms() + 30 * DAY_MS, "flowResetTime": 1, "status": 1})
    api.call("forward/create", {"name": "acc-rule", "tunnelId": ctx.tunnel, "landingNodeId": ctx.nodes["u1_ss"], "strategy": "fifo"}, u1)
    fw = api.call("forward/list", {}, u1)["items"]
    ctx.forward = fw[0]["id"]
    expect(r, "rule picked from 我的落地节点 stores landingNodeId and targets the node", fw[0]["landingNodeId"] == ctx.nodes["u1_ss"] and fw[0]["remoteAddr"].endswith(":" + str(nview(ctx, ctx.nodes["u1_ss"])["port"])), {k: fw[0][k] for k in ("landingNodeId", "remoteAddr", "inIp")})
    lk = links(ctx, ctx.nodes["u1_ss"], u1)
    expect(r, "node links include the 转发版 (rule entry) link", "forward" in lk, list(lk))
    cl = client(ctx, lk["forward"], 26583)
    res = wait_for("rule link carries traffic", lambda: coreclient.download(26583, H, HTTP, 1 << 20), timeout=30)
    expect(r, "转发版 link carries 1 MiB via the rule port into the landing", res["bytes"] == 1 << 20, res)
    stop(ctx, cl)
    # Shared (async Custom fake).
    s = create_node(ctx, u1, ctx.shared, "ss2022", "u1-shared-ss")
    ctx.nodes["u1_shared"] = s["id"]
    v = wait_node(ctx, s["id"], lambda x: x["remoteState"] == "active", "shared node applied by async Custom", timeout=40, interval=1, do_sync=True)
    body, raw = desired(ctx, 7001)
    in_desired = [n for n in body["nodes"] if n["id"] == str(s["id"])]
    expect(r, "shared node pushed to Custom and reported active after async apply", in_desired and in_desired[0]["enabled"] and v["remoteState"] == "active", {"remoteState": v["remoteState"], "port": in_desired[0]["port"] if in_desired else None})
    expect(r, "Custom without egress capability gets today's payload (no `egress` key)", '"egress"' not in raw, raw[:160])
    # Old Helper.
    o = create_node(ctx, u1, ctx.oldhelper, "ss2022", "u1-oldhelper-ss")
    ctx.nodes["u1_old"] = o["id"]
    for _ in range(3):
        sync(ctx)
        time.sleep(1)
    ov, sv = nview(ctx, o["id"]), api.server(ctx.oldhelper)
    expect(r, "old-Helper share: node never shown as removed", ov["remoteState"] != "removed", {"remoteState": ov["remoteState"], "syncError": ov["syncError"]})
    notice = sv.get("sharedNotice") or ""
    expect(r, "old-Helper share: server card explains the mmwx side is not ready", "妙妙屋" in notice and ("未运行" in notice or "升级" in notice or "就绪" in notice), notice)
    uv = next(x for x in api.nodes(u1) if x["id"] == o["id"])
    expect(r, "old-Helper share: the user's node row shows 等待同步 with the not-ready reason", uv.get("status") == "pending" and "妙妙屋" in (uv.get("pendingReason") or ""), {k: uv.get(k) for k in ("status", "pendingReason", "remoteState")})


def s5_billing(ctx):
    r, api = ctx.report, ctx.api
    u1 = ctx.users["acc_u1"]
    ss = ctx.nodes["u1_ss"]
    base = settle(ctx, u1.uid, 0, ledger(ctx, u1.uid), timeout=60)
    lk = links(ctx, ss, u1)
    cl1, cl2 = client(ctx, lk["direct"], 26584), client(ctx, lk["forward"], 26585)
    coreclient.download(26584, H, HTTP, 4 << 20)
    coreclient.download(26585, H, HTTP, 4 << 20)
    stop(ctx, cl1)
    stop(ctx, cl2)
    a = settle(ctx, u1.uid, 8 << 20, base)
    n0, n1 = base["nodes"][ss], a["nodes"][ss]
    up, down = n1["up_bytes"] - n0["up_bytes"], n1["down_bytes"] - n0["down_bytes"]
    bi, bo = n1["billed_in"] - n0["billed_in"], n1["billed_out"] - n0["billed_out"]
    expect(r, "landing billed = raw x2 (双向 x1) per direction", bi == 2 * up and bo == 2 * down, {"up": up, "down": down, "billed_in": bi, "billed_out": bo})
    du = (a["user"]["in_flow"] - base["user"]["in_flow"], a["user"]["out_flow"] - base["user"]["out_flow"])
    all_billed = sum(x["billed_in"] - base["nodes"].get(i, {"billed_in": 0})["billed_in"] + x["billed_out"] - base["nodes"].get(i, {"billed_out": 0})["billed_out"] for i, x in a["nodes"].items())
    expect(r, "user total flow grows by the landing bill only (rule -> own landing not double billed)", du[0] + du[1] == all_billed and du[0] == sum(x["billed_in"] - base["nodes"].get(i, {"billed_in": 0})["billed_in"] for i, x in a["nodes"].items()), {"user_delta": du, "landing_billed": all_billed})
    fwd = a["forwards"][ctx.forward]["in_flow"] + a["forwards"][ctx.forward]["out_flow"] - base["forwards"][ctx.forward]["in_flow"] - base["forwards"][ctx.forward]["out_flow"]
    expect(r, "rule traffic still recorded on the rule itself", fwd > 4 << 20, fwd)
    dq = a["quota"]["daily_used_bytes"] - base["quota"]["daily_used_bytes"], a["quota"]["monthly_used_bytes"] - base["quota"]["monthly_used_bytes"]
    expect(r, "daily + monthly quota usage = user flow delta", dq[0] == du[0] + du[1] and dq[1] == du[0] + du[1], {"daily": dq[0], "monthly": dq[1]})
    key = ("node", ctx.node_id)
    sq0 = base["server_quota"].get(key, {"used_in": 0, "used_out": 0})
    sq = a["server_quota"][key]["used_in"] + a["server_quota"][key]["used_out"] - sq0["used_in"] - sq0["used_out"]
    expect(r, "per-server quota row (this node) = user flow delta", sq == du[0] + du[1], {"server_quota_delta": sq})
    # Shared counters (Custom-reported) bill the same way.
    sh = ctx.nodes["u1_shared"]
    b2 = ledger(ctx, u1.uid)
    fake(ctx, "counters", {"id": 7001, "node_id": sh, "up": 1000, "down": 9000, "connections": 0})
    sync(ctx)

    def billed_shared():
        x = ledger(ctx, u1.uid)
        return x if x["nodes"][sh]["billed_in"] - b2["nodes"][sh]["billed_in"] == 2000 else None
    a2 = wait_for("shared counters billed", billed_shared, timeout=30)
    d2 = a2["user"]["in_flow"] + a2["user"]["out_flow"] - b2["user"]["in_flow"] - b2["user"]["out_flow"]
    sk = ("landing", ctx.shared)
    sq2 = a2["server_quota"][sk]["used_in"] + a2["server_quota"][sk]["used_out"] - b2["server_quota"].get(sk, {"used_in": 0, "used_out": 0})["used_in"] - b2["server_quota"].get(sk, {"used_in": 0, "used_out": 0})["used_out"]
    expect(r, "shared node: Custom counters 1000/9000 bill 2000/18000 into user flow + shared server quota", d2 == 20000 and sq2 == 20000 and a2["nodes"][sh]["billed_out"] - b2["nodes"][sh]["billed_out"] == 18000, {"user_delta": d2, "server_quota_delta": sq2})
    # Per-node traffic cap pauses only that node.
    vl = ctx.nodes["u1_vless"]
    api.call("landing/node/update", {"id": vl, "trafficLimitBytes": 1})
    v = wait_node(ctx, vl, lambda x: x["status"] == "paused_quota", "vless paused by node cap")
    other = nview(ctx, ss)
    expect(r, "node traffic cap pauses only that node", v["status"] == "paused_quota" and other["status"] == "active", {"vless": v["status"], "ss": other["status"]})
    cl = client(ctx, links(ctx, vl, u1)["direct"], 26586)
    expect(r, "capped node refuses traffic", wait_for("cap enforced", lambda: not coreclient.try_echo(26586, H, ECHO), timeout=30), None)
    api.call("landing/node/update", {"id": vl, "trafficLimitBytes": 0})
    wait_node(ctx, vl, lambda x: x["status"] == "active", "vless restored")
    expect(r, "removing the cap restores the node", wait_for("cap lifted", lambda: coreclient.try_echo(26586, H, ECHO), timeout=30), None)
    stop(ctx, cl)


def s6_limits(ctx):
    r, api = ctx.report, ctx.api
    u1 = ctx.users["acc_u1"]
    ss = ctx.nodes["u1_ss"]
    link = links(ctx, ss, u1)["direct"]
    try:
        wait_conns_zero(ctx, ss)
        # max_connections is the node's total socket budget (each proxied
        # session = inbound + outbound socket), as plan 101 designed it.
        api.call("landing/node/update", {"id": ss, "maxConnections": 4})
        time.sleep(3)
        cl = client(ctx, link, 26587)
        held = []
        try:
            held.append(wait_for("1st held", lambda: coreclient.echo_open(26587, H, ECHO), timeout=20))
            held.append(coreclient.echo_open(26587, H, ECHO))
            third = coreclient.try_echo(26587, H, ECHO)
            expect(r, "max_connections 4 (= 2 sessions): two held sessions work, the 3rd is refused", not third and all(coreclient.echo_alive(x) for x in held), None)
        finally:
            for x in held:
                x.close()
        expect(r, "after closing (and TIME_WAIT expiry) a new session is accepted again", wait_for("slot freed", lambda: coreclient.try_echo(26587, H, ECHO), timeout=120, interval=3), None)
        stop(ctx, cl)
        api.call("landing/node/update", {"id": ss, "maxConnections": 0})
        wait_conns_zero(ctx, ss)
        api.call("landing/node/update", {"id": ss, "maxOnlineIps": 2})
        time.sleep(3)
        cls = [client(ctx, link, 26588 + i, send_through=ip) for i, ip in enumerate(labmod.SOURCE_IPS)]
        held = []
        try:
            held.append(wait_for("ip1", lambda: coreclient.echo_open(26588, H, ECHO), timeout=20))
            held.append(coreclient.echo_open(26589, H, ECHO))
            def online_ips():
                data = api.call("landing/monitor/ips", {"nodeId": ss})
                return data if data.get("onlineIps") == 2 else None
            ip_view = wait_for("two real client IPs reported", online_ips, timeout=45)
            expect(r, "online IP list contains both connected sources with connections",
                   {x["ip"] for x in ip_view["ips"]} == set(labmod.SOURCE_IPS[:2])
                   and all(x["connections"] > 0 for x in ip_view["ips"]), ip_view)
            monitor = api.call("landing/monitor")
            monitored = next(n for s in monitor["servers"] for n in s["nodes"] if n["id"] == ss)
            expect(r, "monitor onlineIps agrees with the real client IP list",
                   monitored.get("onlineIps") == 2, {"nodeId": ss, "onlineIps": monitored.get("onlineIps")})
            third = coreclient.try_echo(26590, H, ECHO)
            expect(r, "max_online_ips 2: sources .11 and .12 work, a 3rd source IP is refused", not third, None)
            expect(r, "an already-online source can open more sessions", coreclient.try_echo(26588, H, ECHO), None)
        finally:
            for x in held:
                x.close()
            for c in cls:
                stop(ctx, c)
    finally:
        api.call("landing/node/update", {"id": ss, "maxConnections": 0, "maxOnlineIps": 0})
    # Shared caps clamp node limits (share cap 50 connections).
    sh = ctx.nodes["u1_shared"]
    original = nview(ctx, sh)["maxConnections"]
    res = api.raw("landing/node/update", {"id": sh, "maxConnections": 100})
    sync(ctx)
    time.sleep(1)
    body, _ = desired(ctx, 7001)
    pushed = next(n for n in body["nodes"] if n["id"] == str(sh))
    expect(r, "shared share cap (50) clamps or refuses a node limit of 100", (0 < pushed["max_connections"] <= 50) or res.get("code") != 0, {"api_code": res.get("code"), "msg": res.get("msg"), "pushed_max_connections": pushed["max_connections"]})
    api.call("landing/node/update", {"id": sh, "maxConnections": original})


def s7_user_state(ctx):
    r, api = ctx.report, ctx.api
    u1 = ctx.users["acc_u1"]
    ss, sh = ctx.nodes["u1_ss"], ctx.nodes["u1_shared"]
    before = {k: links(ctx, ss, u1) for k in ("ss",)}["ss"]
    port = nview(ctx, ss)["port"]
    cl_d, cl_f = client(ctx, before["direct"], 26591), client(ctx, before["forward"], 26592)
    expect(r, "baseline: direct + rule links work", coreclient.try_download(26591, H, HTTP) and coreclient.try_download(26592, H, HTTP), None)
    orig = user_row(ctx, u1.uid)
    for label, change, restore, state in (
        ("flow exhausted", {"flow": 0}, {"flow": orig["flow"]}, "paused_quota"),
        ("expired", {"expTime": now_ms() - 60000}, {"expTime": orig["expTime"]}, "paused_expired"),
        ("disabled", {"status": 0}, {"status": 1}, "disabled"),
    ):
        user_update(ctx, u1.uid, **change)
        v = wait_node(ctx, ss, lambda x: x["status"] == state, f"{label}: node {state}")
        fw = wait_for(f"{label}: rule paused", lambda: (lambda f: f if f["status"] != 1 else None)(ledger(ctx, u1.uid)["forwards"][ctx.forward]), timeout=30)
        blocked = wait_for(f"{label}: links refused", lambda: not coreclient.try_download(26591, H, HTTP) and not coreclient.try_download(26592, H, HTTP), timeout=40)
        sync(ctx)
        body, _ = desired(ctx, 7001)
        shared_off = next(n for n in body["nodes"] if n["id"] == str(sh))["enabled"] is False
        expect(r, f"user {label}: landing nodes stop ({state}), rule paused, shared node disabled in desired", blocked and shared_off and v["status"] == state, {"forward_status": fw["status"], "landing_auto_paused": fw["landing_auto_paused"]})
        user_update(ctx, u1.uid, **restore)
        v = wait_node(ctx, ss, lambda x: x["status"] == "active", f"{label}: restored")
        fw = wait_for(f"{label}: rule resumed", lambda: (lambda f: f if f["status"] == 1 else None)(ledger(ctx, u1.uid)["forwards"][ctx.forward]), timeout=40)
        ok = wait_for(f"{label}: links work again", lambda: coreclient.try_download(26591, H, HTTP) and coreclient.try_download(26592, H, HTTP), timeout=40)
        sync(ctx)
        body, _ = desired(ctx, 7001)
        shared_on = next(n for n in body["nodes"] if n["id"] == str(sh))["enabled"] is True
        expect(r, f"user {label} lifted: node active with the same port + credentials, rule resumed, shared node re-enabled", ok and shared_on and links(ctx, ss, u1) == before and v["port"] == port, {"port": v["port"]})
    stop(ctx, cl_d)
    stop(ctx, cl_f)


def s8_delete(ctx):
    r, api = ctx.report, ctx.api
    u1 = ctx.users["acc_u1"]
    vl = ctx.nodes["u1_vless"]
    port = nview(ctx, vl)["port"]
    expect(r, "native node listener present before delete", wait_for("listening", lambda: ns_listening(port), timeout=20), port)
    wait_conns_zero(ctx, vl)
    api.call("landing/node/delete", {"id": vl}, u1)
    expect(r, "native delete removes the row", nview(ctx, vl) is None, None)
    expect(r, "native delete closes the Core listener", wait_for("listener closed", lambda: not ns_listening(port), timeout=40), port)
    n = create_node(ctx, u1, ctx.native, "vless_reality_vision", "u1-native-vless-2")
    expect(r, "released native port is reused by the next node", n["port"] == port, {"old": port, "new": n["port"]})
    ctx.nodes["u1_vless"] = n["id"]
    sh = ctx.nodes["u1_shared"]
    sport = nview(ctx, sh)["port"]
    fake(ctx, "counters", {"id": 7001, "node_id": sh, "connections": 0})
    sync(ctx)
    wait_conns_zero(ctx, sh, timeout=30)
    res = api.raw("landing/node/delete", {"id": sh}, u1)
    expect(r, "shared delete is accepted as pending until Custom confirms", res.get("code") == 0 and (res.get("data") or {}).get("pending") is True, res)
    gone = wait_for("shared delete finalized", lambda: (sync(ctx), nview(ctx, sh) is None)[1], timeout=60, interval=2)
    st = fake(ctx, "state")
    expect(r, "shared delete finalized after Custom reported the node removed", gone and str(sh) in st["removed"].get("7001", []), st["removed"])
    n2 = create_node(ctx, u1, ctx.shared, "ss2022", "u1-shared-ss-2")
    expect(r, "released shared port is reused", n2["port"] == sport, {"old": sport, "new": n2["port"]})
    ctx.nodes["u1_shared"] = n2["id"]
    wait_node(ctx, n2["id"], lambda x: x["remoteState"] == "active", "new shared node active", timeout=40, interval=1, do_sync=True)
    # Manual 立即同步 during backoff.
    fake(ctx, "fail", {"id": 7001, "status": 500, "code": "unavailable", "count": 1000})
    api.call("landing/node/update", {"id": n2["id"], "name": "u1-shared-ss-2b"})
    sv = wait_for("push failure recorded", lambda: (lambda s: s if s["sharedSyncError"] else None)(api.server(ctx.shared)), timeout=30)
    rev = sv["revision"]
    fake(ctx, "fail", {"id": 7001, "count": 0})
    t0 = time.monotonic()
    sync(ctx)
    body, _ = desired(ctx, 7001)
    sv = api.server(ctx.shared)
    expect(r, "立即同步 during backoff pushes at once after Custom recovers", body["revision"] >= rev and sv["sharedPushedRevision"] >= rev and time.monotonic() - t0 < 10, {"desired_revision": body["revision"], "server_revision": rev, "seconds": round(time.monotonic() - t0, 2), "syncError": sv["sharedSyncError"]})


def e1_default(ctx):
    r, api = ctx.report, ctx.api
    d = json.loads((ctx.lab.landing_dir() / "desired.json").read_text())
    expect(r, "default profile: agent desired has no egress plan (today's behaviour)", not d.get("egress"), list(d))
    v = api.server(ctx.shared)
    expect(r, "shared without capability: 出站 shown unsupported", v["egress"]["supported"] is False and "暂不支持" in v["egress"]["unsupportedReason"], v["egress"]["unsupportedReason"])
    o = api.call("landing/egress/outbound/save", {"name": "acc-direct-v4", "type": "direct", "ipVersion": "force_ipv4", "settings": {}})
    ctx.out_v4 = o["id"]
    o = api.call("landing/egress/outbound/save", {"name": "acc-direct-v6", "type": "direct", "ipVersion": "force_ipv6", "settings": {}})
    ctx.out_v6 = o["id"]
    p = api.call("landing/egress/profile/save", {"name": "acc-block-echo", "defaultAction": "direct", "rules": [{"id": "blk", "name": "block echo", "enabled": True, "ips": [H + "/32"], "ports": str(ECHO), "action": "block"}]})
    ctx.p_block = p["id"]
    msg = api.expect_error("landing/server/save", server_payload(api.server(ctx.shared), routeProfileId=ctx.p_block))
    expect(r, "assigning a profile to a share without capability is refused", True, msg)
    _, raw = desired(ctx, 7001)
    expect(r, "...and its desired payload still carries no egress", '"egress"' not in raw, None)


def e2_native(ctx):
    r, api = ctx.report, ctx.api
    u1 = ctx.users["acc_u1"]
    ss, vl = ctx.nodes["u1_ss"], ctx.nodes["u1_vless"]
    pids0 = ctx.lab.core_pids()
    c1 = client(ctx, links(ctx, ss, u1)["direct"], 26593)
    c2 = client(ctx, links(ctx, vl, u1)["direct"], 26594)
    res = api.call("landing/server/save", server_payload(api.server(ctx.native), routeProfileId=ctx.p_block))
    expect(r, "native server accepts a profile (no restart needed on a fresh Core)", res["egress"]["profileId"] == ctx.p_block and not res["egress"]["restartRequired"], res["egress"])
    wait_for("egress applied", lambda: api.server(ctx.native)["egress"]["state"] == "applied", timeout=40)
    blocked = wait_for("echo blocked", lambda: not coreclient.try_echo(26593, H, ECHO), timeout=30)
    expect(r, "block rule (target IP + port) applied hot: echo refused, HTTP still works", blocked and coreclient.try_download(26593, H, HTTP), None)
    expect(r, "user sees only the read-only 出站 label", next(x for x in api.servers(u1) if x["id"] == ctx.native).get("egressLabel") == "按规则分流", next(x for x in api.servers(u1) if x["id"] == ctx.native).get("egressLabel"))
    api.call("landing/node/update", {"id": ss, "routeProfileId": 0})
    ok = wait_for("ss override to default", lambda: coreclient.try_echo(26593, H, ECHO), timeout=30)
    expect(r, "node override 默认（直连） exempts only that node", ok and not coreclient.try_echo(26594, H, ECHO), None)
    p6 = api.call("landing/egress/profile/save", {"name": "acc-only-v6", "defaultAction": f"outbound:{ctx.out_v6}", "rules": []})
    p4 = api.call("landing/egress/profile/save", {"name": "acc-only-v4", "defaultAction": f"outbound:{ctx.out_v4}", "rules": []})
    H6 = labmod.HOST_IP6
    expect(r, "default outbound reaches both the IPv4 and the IPv6 fixture", coreclient.try_download(26593, H, HTTP) and coreclient.try_download(26593, H6, HTTP), None)
    api.call("landing/node/update", {"id": ss, "routeProfileId": p6["id"]})
    v6 = wait_for("force IPv6 refuses a v4 literal", lambda: not coreclient.try_download(26593, H, HTTP), timeout=30)
    expect(r, "强制 IPv6 outbound: IPv6 target works, an IPv4 literal cannot leave", v6 and coreclient.try_download(26593, H6, HTTP), None)
    api.call("landing/node/update", {"id": ss, "routeProfileId": p4["id"]})
    v4 = wait_for("force IPv4 works", lambda: coreclient.try_download(26593, H, HTTP), timeout=30)
    expect(r, "强制 IPv4 outbound: IPv4 target works, an IPv6 literal cannot leave", v4 and not coreclient.try_download(26593, H6, HTTP), None)
    api.call("landing/node/update", {"id": ss, "routeProfileId": None})
    api.call("landing/server/save", server_payload(api.server(ctx.native), routeProfileId=0))
    wait_for("back to default", lambda: coreclient.try_echo(26593, H, ECHO) and coreclient.try_echo(26594, H, ECHO), timeout=40)
    d = wait_for("desired egress cleared", lambda: (lambda x: x if not x.get("egress") else None)(json.loads((ctx.lab.landing_dir() / "desired.json").read_text())), timeout=30)
    expect(r, "back to 默认（直连）: no egress plan, both nodes pass everything", not d.get("egress"), None)
    expect(r, "all profile changes were hot (same Core process)", ctx.lab.core_pids() == pids0, {"before": pids0, "after": ctx.lab.core_pids()})
    stop(ctx, c1)
    stop(ctx, c2)


def e3_shared(ctx):
    r, api = ctx.report, ctx.api
    fake(ctx, "share", {"id": 7001, "_cfg": {"egress": {"versions": [1], "allowed": True, "geoip": False, "geosite": False}}})
    sync(ctx)
    v = wait_for("capability seen", lambda: (lambda s: s if s["egress"]["supported"] else None)(api.server(ctx.shared)), timeout=20)
    expect(r, "Custom advertising egress v1 -> profile selectable", v["egress"]["supported"], v["egress"])
    api.call("landing/server/save", server_payload(v, routeProfileId=ctx.p_block))
    sync(ctx)
    body = wait_for("egress in desired", lambda: (lambda b: b if b and b.get("egress") else None)(desired(ctx, 7001)[0]), timeout=20)
    rules = body["egress"]["rules"]
    tags = sorted(t for rr in rules for t in rr["inbound_tags"])
    want = sorted(f"flvxl-{n['id']}" for n in body["nodes"] if n["enabled"])
    expect(r, "desired carries the compiled plan for exactly this share's nodes", tags == want and rules[0]["outbound"] == "block" and body["egress"]["version"] == 1, {"rules": rules, "nodes": want})
    geo = api.call("landing/egress/profile/save", {"name": "acc-geo", "defaultAction": "direct", "rules": [{"id": "g", "name": "netflix", "enabled": True, "domains": ["geosite:netflix"], "action": "block"}]})
    msg = api.expect_error("landing/server/save", server_payload(api.server(ctx.shared), routeProfileId=geo["id"]))
    expect(r, "geo profile refused when Custom has no geo data", True, msg)
    fake(ctx, "share", {"id": 7001, "_cfg": {"egress": {"versions": [1], "allowed": False, "geoip": False, "geosite": False}}})
    sync(ctx)
    time.sleep(1)
    sync(ctx)
    body, raw = desired(ctx, 7001)
    v = api.server(ctx.shared)
    expect(r, "share policy 不允许 -> plan withdrawn (no egress key) and UI says so", '"egress"' not in raw and not v["egress"]["supported"], v["egress"]["unsupportedReason"])
    fake(ctx, "share", {"id": 7001, "_cfg": {"egress": None}})
    sync(ctx)


def r1_resources(ctx):
    r = ctx.report
    pids = ctx.lab.core_pids()
    agent = ctx.lab.agent.pid if ctx.lab.agent else None
    out = {"core": [ctx.lab.proc_usage(p) for p in pids]}
    # `ip netns exec` execs the agent, so the Popen pid is the agent itself.
    out["agent"] = ctx.lab.proc_usage(agent) if agent else None
    log = ctx.lab.scratch / "agent.log"
    out["agent_log_bytes"] = log.stat().st_size if log.exists() else 0
    landing_files = sum(p.stat().st_size for p in ctx.lab.landing_dir().rglob("*") if p.is_file() and p.name != "flvx-landing-core")
    out["landing_dir_bytes_excl_core"] = landing_files
    r.ok("resource snapshot after the whole run", out)
    expect(r, "Core still the original process after all scenarios", pids == ctx.core_pids0, {"start": ctx.core_pids0, "end": pids})


SCENARIOS = [
    ("S1", "shared caps host follow + manual edit kept", s1_hosts),
    ("S2", "grants: direct / group / none / revoke + re-grant", s2_grants),
    ("S3", "max nodes + admin create-for-user", s3_limits_count),
    ("S4", "links connect (native SS2022 + Reality, rule link), async Custom, old Helper", s4_connect),
    ("S5", "billing: landing, rule->own landing, shared counters, node cap", s5_billing),
    ("S6", "limits: max connections / online IPs, shared cap clamp", s6_limits),
    ("S7", "user flow / expiry / disable stop and restore nodes + rules", s7_user_state),
    ("S8", "delete releases ports (native + shared async), 立即同步 in backoff", s8_delete),
    ("E1", "egress default = today's payload; unsupported share", e1_default),
    ("E2", "egress on native: block rule, node override, force IPv4/IPv6, hot", e2_native),
    ("E3", "egress on shared: capability, plan in desired, geo refusal, policy off", e3_shared),
    ("R1", "resource snapshot", r1_resources),
]


def run(args, selected):
    ctx = Ctx()
    scratch = pathlib.Path(args.scratch)
    ctx.lab = labmod.Lab(scratch, args.panel, args.agent, args.core)
    ctx.core = args.core
    ctx.report = Report(args.report or scratch / "report.json")
    ctx.clients = []
    try:
        setup(ctx)
        run_scenarios(ctx, selected, SCENARIOS)
    finally:
        for c in list(ctx.clients):
            c.stop()
        if not args.keep:
            ctx.lab.cleanup()
    print(ctx.report.matrix())
    return 0 if ctx.report.passed else 1
