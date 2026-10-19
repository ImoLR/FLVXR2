#!/usr/bin/env python3
"""Fake mmwx Custom FLVX tenant API for the landing acceptance runner.

  customfake.py serve <port> <token>

Implements the subset of /api/custom/flvx/v1 FLVX uses (contract v1 plus the
optional landing-egress-v1 capability) the way the real Custom + Helper behave:

* PUT /shares/{id}/desired is accepted at once; an "async" share applies it
  only after `delay` seconds (like a Helper report cycle). Desired nodes are
  reported "active" when enabled, "removed" when disabled; nodes missing from
  the latest desired state are reported "removed" (tombstones), as the Helper
  does (cmd/mmwxc-helper/flvx_runtime.go record()).
* An "old_helper" share accepts desired state but never applies it: core
  state "stopped" (or `core_state`), applied_revision 0, no nodes - what
  Custom reported for server 5 on Helper v0.7.4.
* Failures can be injected for PUT desired (HTTP status + error code, N times).
* Counters are cumulative per node and set through the control API.

Control API (loopback, no auth): POST /_fake/share, /_fake/fail, /_fake/counters,
/_fake/remove-share; GET /_fake/state. Nothing is written to disk.
"""
import copy
import http.server
import json
import re
import sys
import threading
import time

PREFIX = "/api/custom/flvx/v1"
SHARE_DEFAULTS = {
    "name": "fake share",
    "hosts": {"ipv4": "", "ipv6": "", "domain": ""},
    "status": "active",
    "port_min": 40000,
    "port_max": 40099,
    "traffic_limit_bytes": 100 << 30,
    "traffic_used_bytes": 0,
    "reset_day": 1,
    "max_connections": 500,
    "connections": 0,
    "share_until": None,
    "core": {"state": "running", "version": "v1.4.59"},
    "protocols": ["ss2022", "vless_reality_vision"],
    "dual_install": False,
}


class State:
    def __init__(self):
        self.lock = threading.Lock()
        self.shares = {}  # id -> public share fields
        self.cfg = {}  # id -> {"mode", "delay", "core_state", "egress"}
        self.desired = {}  # id -> {"body", "raw", "at"}
        self.history = {}  # id -> [{"revision", "raw", "at"}]
        self.applied = {}  # id -> {"revision", "nodes": {node_id: enabled}, "egress", "at"}
        self.removed = {}  # id -> set(node_id) tombstones reported as removed
        self.counters = {}  # id -> {node_id: {"up", "down", "connections"}}
        self.fail = {}  # id -> {"status", "code", "count"}
        self.hosts = None
        self.log = []  # (t, method, path, status)

    def note(self, method, path, status):
        self.log.append((round(time.time(), 3), method, path, status))
        del self.log[:-400]

    def apply_due(self, sid, now):
        d = self.desired.get(sid)
        cfg = self.cfg.get(sid, {})
        if d is None or cfg.get("mode", "async") != "async":
            return
        if now < d["at"] + float(cfg.get("delay", 2.0)):
            return
        body = d["body"]
        prev = self.applied.get(sid)
        if prev and prev["revision"] == body["revision"]:
            return
        nodes = {n["id"]: bool(n.get("enabled")) for n in body.get("nodes", [])}
        gone = self.removed.setdefault(sid, set())
        if prev:
            for nid in prev["nodes"]:
                if nid not in nodes:
                    gone.add(nid)
        for nid in nodes:
            gone.discard(nid)
        self.applied[sid] = {"revision": body["revision"], "nodes": nodes, "egress": body.get("egress"), "at": now}

    def status(self, sid, now):
        cfg = self.cfg.get(sid, {})
        collected = int(now * 1000)
        if cfg.get("mode") == "old_helper":
            return {"applied_revision": 0, "core": {"state": cfg.get("core_state") or "stopped", "version": ""}, "nodes": [], "share_traffic_used_bytes": 0, "collected_at": collected}
        self.apply_due(sid, now)
        a = self.applied.get(sid)
        counters = self.counters.get(sid, {})
        nodes = []
        if a:
            for nid, enabled in sorted(a["nodes"].items()):
                c = counters.get(nid, {})
                nodes.append({"id": nid, "state": "active" if enabled else "removed", "error": "", "up_bytes": c.get("up", 0), "down_bytes": c.get("down", 0), "connections": c.get("connections", 0) if enabled else 0})
            for nid in sorted(self.removed.get(sid, set())):
                c = counters.get(nid, {})
                nodes.append({"id": nid, "state": "removed", "error": "", "up_bytes": c.get("up", 0), "down_bytes": c.get("down", 0), "connections": 0})
        used = sum(c.get("up", 0) + c.get("down", 0) for c in counters.values())
        out = {"applied_revision": a["revision"] if a else 0, "core": {"state": cfg.get("core_state") or "running", "version": "v1.4.59", "epoch": "fake-1"}, "nodes": nodes, "share_traffic_used_bytes": used, "collected_at": collected}
        if a and a.get("egress") is not None and cfg.get("egress"):
            out["egress"] = {"state": "applied", "applied_revision": a["revision"]}
        return out


STATE = State()
TOKEN = ""


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *args):
        pass

    def reply(self, status, obj):
        raw = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)
        STATE.note(self.command, self.path, status)

    def body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n) if n else b""

    def authorized(self):
        return self.headers.get("Authorization") == "Bearer " + TOKEN

    def do_GET(self):
        now = time.time()
        with STATE.lock:
            if self.path == "/_fake/state":
                return self.reply(200, {
                    "shares": STATE.shares, "cfg": STATE.cfg,
                    "desired": {k: {"raw": v["raw"], "at": v["at"], "revision": v["body"]["revision"]} for k, v in STATE.desired.items()},
                    "history": STATE.history,
                    "applied": STATE.applied,
                    "removed": {k: sorted(v) for k, v in STATE.removed.items()},
                    "counters": STATE.counters, "hosts": STATE.hosts, "log": STATE.log[-100:],
                })
            if not self.path.startswith(PREFIX):
                return self.reply(404, {"error": {"code": "not_found"}})
            if not self.authorized():
                return self.reply(401, {"error": {"code": "unauthorized"}})
            path = self.path[len(PREFIX):]
            if path == "/shares":
                out = []
                for sid in sorted(STATE.shares):
                    s = copy.deepcopy(STATE.shares[sid])
                    s["server_id"] = sid
                    eg = STATE.cfg.get(sid, {}).get("egress")
                    if eg:
                        s["egress"] = eg
                    if STATE.cfg.get(sid, {}).get("core_state"):
                        s["core"] = {"state": STATE.cfg[sid]["core_state"], "version": ""}
                    out.append(s)
                return self.reply(200, {"shares": out})
            m = re.fullmatch(r"/shares/(\d+)/status", path)
            if m:
                sid = int(m.group(1))
                if sid not in STATE.shares:
                    return self.reply(404, {"error": {"code": "not_found"}})
                return self.reply(200, STATE.status(sid, now))
            return self.reply(404, {"error": {"code": "not_found"}})

    def do_PUT(self):
        raw = self.body()
        now = time.time()
        with STATE.lock:
            if not self.path.startswith(PREFIX):
                return self.reply(404, {"error": {"code": "not_found"}})
            if not self.authorized():
                return self.reply(401, {"error": {"code": "unauthorized"}})
            path = self.path[len(PREFIX):]
            if path == "/landing-hosts":
                STATE.hosts = json.loads(raw or b"{}")
                return self.reply(200, {})
            m = re.fullmatch(r"/shares/(\d+)/desired", path)
            if not m:
                return self.reply(404, {"error": {"code": "not_found"}})
            sid = int(m.group(1))
            if sid not in STATE.shares:
                return self.reply(404, {"error": {"code": "not_found"}})
            f = STATE.fail.get(sid)
            if f and f.get("count", 0) > 0:
                f["count"] -= 1
                return self.reply(int(f.get("status", 500)), {"error": {"code": f.get("code", "unavailable")}})
            try:
                body = json.loads(raw)
            except ValueError:
                return self.reply(400, {"error": {"code": "invalid_json"}})
            rev = int(body.get("revision", 0))
            cur = STATE.desired.get(sid)
            if cur and rev < cur["body"]["revision"]:
                return self.reply(409, {"error": {"code": "revision_conflict"}})
            if cur and rev == cur["body"]["revision"] and raw.decode() != cur["raw"]:
                return self.reply(409, {"error": {"code": "revision_conflict"}})
            if "egress" in body and not STATE.cfg.get(sid, {}).get("egress"):
                # An old Custom would ignore it; the runner asserts FLVX never sends it.
                return self.reply(422, {"error": {"code": "unsupported"}, "errors": [{"field": "egress", "message": "egress not supported"}]})
            if not cur or cur["raw"] != raw.decode():
                STATE.desired[sid] = {"body": body, "raw": raw.decode(), "at": now}
                STATE.history.setdefault(sid, []).append({"revision": rev, "raw": raw.decode(), "at": now})
                del STATE.history[sid][:-50]
            return self.reply(200, {"accepted_revision": rev})

    def do_POST(self):
        raw = self.body()
        with STATE.lock:
            try:
                req = json.loads(raw or b"{}")
            except ValueError:
                return self.reply(400, {"error": "bad json"})
            if self.path == "/_fake/share":
                sid = int(req.pop("id"))
                cfg = req.pop("_cfg", None)
                share = STATE.shares.get(sid) or copy.deepcopy(SHARE_DEFAULTS)
                for k, v in req.items():
                    share[k] = v
                STATE.shares[sid] = share
                if cfg is not None:
                    STATE.cfg.setdefault(sid, {}).update(cfg)
                    for k in [k for k, v in STATE.cfg[sid].items() if v is None]:
                        del STATE.cfg[sid][k]
                return self.reply(200, {"ok": True})
            if self.path == "/_fake/remove-share":
                STATE.shares.pop(int(req["id"]), None)
                return self.reply(200, {"ok": True})
            if self.path == "/_fake/fail":
                STATE.fail[int(req["id"])] = {"status": req.get("status", 500), "code": req.get("code", "unavailable"), "count": req.get("count", 1)}
                return self.reply(200, {"ok": True})
            if self.path == "/_fake/counters":
                c = STATE.counters.setdefault(int(req["id"]), {}).setdefault(str(req["node_id"]), {})
                for k in ("up", "down", "connections"):
                    if k in req:
                        c[k] = int(req[k])
                return self.reply(200, {"ok": True})
            return self.reply(404, {"error": "unknown"})


class Server(http.server.ThreadingHTTPServer):
    daemon_threads = True
    allow_reuse_address = True


if __name__ == "__main__":
    if len(sys.argv) != 4 or sys.argv[1] != "serve":
        raise SystemExit(__doc__)
    TOKEN = sys.argv[3]
    Server(("127.0.0.1", int(sys.argv[2])), Handler).serve_forever()
