#!/usr/bin/env python3
"""Plan 138 reconnect lab (root only; throwaway network namespaces, nothing
touches production).

Topology (TEST-NET addresses, no internet needed):

  host   panel 127.0.0.1:26781, reset proxy 0.0.0.0:26780 -> panel,
         byte fixture 0.0.0.0:26782
  rcle   entry agent  198.51.140.2 (host link) / 198.51.143.1 (to the hop)
  rclh   hop agent    198.51.141.2 (host link) / 198.51.143.2, 198.51.144.1
  rclx   exit agent   198.51.142.2 (host link) / 198.51.144.2 (from the hop)

Every agent reaches the panel through the reset proxy, which can reset an
agent's connection like Cloudflare does on node 45 (RST to the agent only, the
panel-side leg stays open), reset both legs, or refuse an agent (offline).

  T1 (type 2): entry -> hop -> exit, forward F1 (gost) on the entry -> fixture
  T2 (type 1): entry only, forward F2 (nftables) on the entry -> fixture

  reconnect_lab.py run --panel paneld --agent flvx-agent [--old-agent agent] \
                       --scratch DIR [--only R1,R8] [--keep]
  reconnect_lab.py cleanup --scratch DIR
"""
import argparse
import json
import os
import pathlib
import re
import secrets
import signal
import socket
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent / "landing-acceptance"))
from flvxapi import ADMIN, Identity, Panel, wait_for  # noqa: E402

PATTERN = bytes(range(256)) * 256
NS = {"entry": "rcle", "hop": "rclh", "exit": "rclx"}
HOST_SIDE = {"entry": "198.51.140.1", "hop": "198.51.141.1", "exit": "198.51.142.1"}
LINKS = [  # (name, ns_a, ip_a, ns_b, ip_b)
    ("rclhe", None, "198.51.140.1", "rcle", "198.51.140.2"),
    ("rclhh", None, "198.51.141.1", "rclh", "198.51.141.2"),
    ("rclhx", None, "198.51.142.1", "rclx", "198.51.142.2"),
    ("rcleh", "rcle", "198.51.143.1", "rclh", "198.51.143.2"),
    ("rclhz", "rclh", "198.51.144.1", "rclx", "198.51.144.2"),
]
NODE_IP = {"entry": "198.51.140.2", "hop": "198.51.143.2", "exit": "198.51.144.2"}
AGENT_SRC = {"entry": "198.51.140.2", "hop": "198.51.141.2", "exit": "198.51.142.2"}  # source of the panel connection
PANEL_PORT, PROXY_PORT, FIX_PORT = 26781, 26780, 26782
MUTATING = ("AddService", "UpdateService", "DeleteService", "AddChains", "DeleteChains", "UpdateChains",
            "AddLimiters", "UpdateLimiters", "DeleteLimiters", "AddNftablesRules", "DeleteNftablesRules", "UpdateNftablesRules")


def sh(*args, check=True, **kw):
    return subprocess.run([str(a) for a in args], check=check, capture_output=True, text=True, **kw)


def nsx(ns, *args, check=True):
    return sh("ip", "netns", "exec", ns, *args, check=check)


# --------------------------------------------------------------- reset proxy
class ResetProxy:
    """TCP proxy agent -> panel that can reset agent connections on demand."""

    def __init__(self, upstream_port):
        self.upstream = ("127.0.0.1", upstream_port)
        self.lock = threading.Lock()
        self.conns = []  # (client ip, client sock, upstream sock)
        self.blocked = set()
        self.accepted = {}
        self.silent = set()  # id() of sockets whose peer leg must stay open
        self.dangling = []
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("0.0.0.0", PROXY_PORT))
        self.sock.listen(64)
        threading.Thread(target=self.loop, daemon=True).start()

    @staticmethod
    def rst(s):
        # SO_LINGER 0 + close sends a RST, but only once no thread is blocked in
        # recv on the socket: SHUT_RD wakes the pump thread without a FIN.
        try:
            s.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, b"\x01\x00\x00\x00\x00\x00\x00\x00")
            s.shutdown(socket.SHUT_RD)
        except OSError:
            pass
        try:
            s.close()
        except OSError:
            pass

    def loop(self):
        while True:
            try:
                c, addr = self.sock.accept()
            except OSError:
                return
            ip = addr[0]
            with self.lock:
                blocked = ip in self.blocked
                self.accepted[ip] = self.accepted.get(ip, 0) + 1
            if blocked:
                self.rst(c)
                continue
            try:
                u = socket.create_connection(self.upstream, timeout=5)
                u.settimeout(None)
            except OSError:
                self.rst(c)
                continue
            with self.lock:
                self.conns.append((ip, c, u))
            for a, b in ((c, u), (u, c)):
                threading.Thread(target=self.pump, args=(a, b), daemon=True).start()

    def pump(self, a, b):
        try:
            while True:
                data = a.recv(65536)
                if not data:
                    break
                b.sendall(data)
        except OSError:
            pass
        # A leg reset "like Cloudflare" (agent side only) leaves the other leg
        # open and silent: the panel only learns about it when the agent's new
        # connection replaces the old session (or after its pong timeout).
        if id(b) in self.silent or id(a) in self.silent:
            return
        try:
            b.shutdown(socket.SHUT_WR)
        except OSError:
            pass

    def reset(self, ip, both=False):
        """agent leg: RST (like a foreign Cloudflare colo). Panel leg: kept open
        and silent (Cloudflare never closes it) unless both=True."""
        with self.lock:
            hit = [x for x in self.conns if x[0] == ip]
            self.conns = [x for x in self.conns if x[0] != ip]
        for _, c, u in hit:
            if not both:
                self.silent.add(id(c))
                self.silent.add(id(u))
                self.dangling.append(u)
            self.rst(c)
            if both:
                self.rst(u)
        return len(hit)

    def block(self, ip):
        with self.lock:
            self.blocked.add(ip)
        return self.reset(ip, both=True)

    def unblock(self, ip):
        with self.lock:
            self.blocked.discard(ip)


# ------------------------------------------------------------------ download
class Download(threading.Thread):
    """GET /bytes/n through a forward, verifying every byte."""

    def __init__(self, host, port, n, delay=0.0, connect_timeout=5):
        super().__init__(daemon=True)
        self.host, self.port, self.n, self.delay, self.ct = host, port, n, delay, connect_timeout
        self.got, self.error, self.ok, self.seconds = 0, None, False, None

    def run(self):
        start = time.monotonic()
        try:
            s = socket.create_connection((self.host, self.port), timeout=self.ct)
            s.settimeout(30)
            q = f"?delay={self.delay}" if self.delay else ""
            s.sendall(f"GET /bytes/{self.n}{q} HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n".encode())
            buf = b""
            while b"\r\n\r\n" not in buf:
                chunk = s.recv(65536)
                if not chunk:
                    raise IOError("closed in headers")
                buf += chunk
            head, body = buf.split(b"\r\n\r\n", 1)
            if b" 200 " not in head.split(b"\r\n")[0]:
                raise IOError(head[:80])
            pos = 0

            def check(data):
                nonlocal pos
                for i in range(0, len(data), 65536):
                    part = data[i:i + 65536]
                    off = pos % len(PATTERN)
                    want = (PATTERN[off:] + PATTERN)[:len(part)]
                    if part != want:
                        raise IOError(f"byte mismatch at {pos}")
                    pos += len(part)
            check(body)
            while pos < self.n:
                chunk = s.recv(65536)
                if not chunk:
                    break
                check(chunk)
            s.close()
            self.got = pos
            self.ok = pos == self.n
            if not self.ok:
                self.error = f"short body {pos}/{self.n}"
        except Exception as e:  # noqa: BLE001
            self.error = f"{type(e).__name__}: {e}"
        self.seconds = round(time.monotonic() - start, 2)


class UDPEcho:
    """UDP echo on the host (the forward target's UDP side); remembers which
    source addresses (= relay sessions of the exit agent) it saw."""

    def __init__(self, port):
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("0.0.0.0", port))
        self.lock = threading.Lock()
        self.sources = {}
        threading.Thread(target=self.loop, daemon=True).start()

    def loop(self):
        while True:
            try:
                data, addr = self.sock.recvfrom(2048)
            except OSError:
                return
            with self.lock:
                self.sources[f"{addr[0]}:{addr[1]}"] = self.sources.get(f"{addr[0]}:{addr[1]}", 0) + 1
            try:
                self.sock.sendto(data, addr)
            except OSError:
                pass

    def take(self):
        with self.lock:
            out, self.sources = self.sources, {}
        return out


class UDPFlow(threading.Thread):
    """One datagram every 100 ms through a forward's UDP service; measures
    echoes and the longest gap between two echoes."""

    def __init__(self, host, port):
        super().__init__(daemon=True)
        self.addr = (host, port)
        self.stop_flag = threading.Event()
        self.sent = self.echoed = 0
        self.max_gap = 0.0

    def run(self):
        s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        s.settimeout(0.05)
        last = time.monotonic()
        seq = 0
        next_send = time.monotonic()
        while not self.stop_flag.is_set():
            now = time.monotonic()
            if now >= next_send:
                s.sendto(f"seq {seq}".encode(), self.addr)
                seq += 1
                self.sent += 1
                next_send = now + 0.1
            try:
                s.recvfrom(2048)
                t = time.monotonic()
                self.max_gap = max(self.max_gap, t - last)
                last = t
                self.echoed += 1
            except socket.timeout:
                pass
        self.max_gap = max(self.max_gap, time.monotonic() - last)
        s.close()

    def result(self):
        self.stop_flag.set()
        self.join(5)
        return {"sent": self.sent, "echoed": self.echoed, "max_gap_s": round(self.max_gap, 2)}


def fixture_server():
    """HTTP byte fixture (/bytes/<n>?delay=<s> per 64 KiB) on the host."""
    import http.server
    import socketserver
    import urllib.parse

    class H(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.1"

        def do_GET(self):
            u = urllib.parse.urlsplit(self.path)
            n = int(u.path.split("/")[-1])
            delay = float(urllib.parse.parse_qs(u.query).get("delay", ["0"])[0] or 0)
            self.send_response(200)
            self.send_header("Content-Length", str(n))
            self.send_header("Connection", "close")
            self.end_headers()
            left = n
            try:
                while left > 0:
                    chunk = PATTERN[: min(left, len(PATTERN))]
                    self.wfile.write(chunk)
                    left -= len(chunk)
                    if delay:
                        time.sleep(delay)
            except OSError:
                pass

        def log_message(self, *a):
            pass

    class S(socketserver.ThreadingMixIn, http.server.HTTPServer):
        daemon_threads = True
        allow_reuse_address = True

    srv = S(("0.0.0.0", FIX_PORT), H)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


# ----------------------------------------------------------------------- lab
class Lab:
    def __init__(self, a):
        self.scratch = pathlib.Path(a.scratch)
        self.panel_bin = pathlib.Path(a.panel)
        self.agent_bin = pathlib.Path(a.agent)
        self.old_agent_bin = pathlib.Path(a.old_agent) if a.old_agent else None
        self.procs = {}
        self.jwt_secret = secrets.token_hex(24)
        self.results = {}
        self.marks = {}

    def log(self, *msg):
        line = time.strftime("%H:%M:%S ") + " ".join(str(m) for m in msg)
        print(line, flush=True)
        with open(self.scratch / "run.log", "a") as f:
            f.write(line + "\n")

    def start(self, name, argv, ns=None, cwd=None, env=None):
        log = open(self.scratch / f"{name}.log", "ab")
        full = (["ip", "netns", "exec", ns] if ns else []) + [str(x) for x in argv]
        p = subprocess.Popen(full, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        self.procs[name] = p
        (self.scratch / "pids.json").write_text(json.dumps({n: q.pid for n, q in self.procs.items()}))
        return p

    def stop(self, name, sig=signal.SIGTERM):
        p = self.procs.pop(name, None)
        if p and p.poll() is None:
            try:
                os.killpg(p.pid, sig)
            except ProcessLookupError:
                pass
            try:
                p.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(p.pid, signal.SIGKILL)

    # ---------------------------------------------------------- network
    def preflight(self):
        if os.geteuid() != 0:
            raise SystemExit("needs root")
        existing = sh("ip", "netns", "list").stdout.split()
        for ns in NS.values():
            if ns in existing:
                raise SystemExit(f"namespace {ns} exists; run cleanup first")
        avail = int(next(l.split()[1] for l in open("/proc/meminfo") if l.startswith("MemAvailable")))
        if avail < 700 * 1024:
            raise SystemExit(f"MemAvailable {avail // 1024} MiB < 700 MiB")
        self.scratch.mkdir(parents=True, exist_ok=True)

    def network(self):
        for ns in NS.values():
            sh("ip", "netns", "add", ns)
            nsx(ns, "ip", "link", "set", "lo", "up")
        for name, ns_a, ip_a, ns_b, ip_b in LINKS:
            sh("ip", "link", "add", name + "a", "type", "veth", "peer", "name", name + "b")
            for dev, ns, ip in ((name + "a", ns_a, ip_a), (name + "b", ns_b, ip_b)):
                if ns:
                    sh("ip", "link", "set", dev, "netns", ns)
                    nsx(ns, "ip", "addr", "add", f"{ip}/24", "dev", dev)
                    nsx(ns, "ip", "link", "set", dev, "up")
                else:
                    sh("ip", "addr", "add", f"{ip}/24", "dev", dev)
                    sh("ip", "link", "set", dev, "up")
        for role, ns in NS.items():
            nsx(ns, "ip", "route", "add", "default", "via", HOST_SIDE[role])

    # ------------------------------------------------------------ panel
    def panel(self, binary=None):
        env = dict(os.environ, SERVER_ADDR=f"127.0.0.1:{PANEL_PORT}", DB_TYPE="sqlite", DB_PATH=str(self.scratch / "panel.db"),
                   JWT_SECRET=self.jwt_secret, LOG_DIR=str(self.scratch / "panel-logs"), FLUX_VERSION="3.0.27-reconnect-lab", GOMAXPROCS="2")
        (self.scratch / "panel-logs").mkdir(exist_ok=True)
        self.start("panel", [binary or self.panel_bin], env=env, cwd=str(self.scratch))
        wait_for("panel", lambda: urllib.request.urlopen(f"http://127.0.0.1:{PANEL_PORT}/flow/test", timeout=2).status == 200, timeout=60, interval=0.5)
        self.api = Panel(f"http://127.0.0.1:{PANEL_PORT}", self.jwt_secret)

    def q(self, sql, *args):
        c = sqlite3.connect(f"file:{self.scratch / 'panel.db'}?mode=ro", uri=True, timeout=10)
        c.row_factory = sqlite3.Row
        try:
            return [dict(r) for r in c.execute(sql, args).fetchall()]
        finally:
            c.close()

    def panel_lines(self, pattern, since=0):
        text = (self.scratch / "panel.log").read_text(errors="replace")
        return [l for l in text.splitlines()[since:] if re.search(pattern, l)]

    def panel_mark(self):
        return len((self.scratch / "panel.log").read_text(errors="replace").splitlines())

    # ----------------------------------------------------------- agents
    def start_agent(self, role, binary=None):
        d = self.scratch / f"agent-{role}"
        d.mkdir(parents=True, exist_ok=True)
        node = self.nodes[role]
        cfg = d / "config.json"
        if not cfg.exists():
            cfg.write_text(json.dumps({"addr": f"{HOST_SIDE[role]}:{PROXY_PORT}", "secret": node["secret"], "node_id": node["id"],
                                       "http": 0, "tls": 0, "socks": 0, "block_other": 0}))
            (d / "gost.json").write_text("{}")
        env = dict(os.environ, GOMAXPROCS="2", TMPDIR=str(self.scratch))
        self.start(f"agent-{role}", [binary or self.agent_bin], ns=NS[role], cwd=str(d), env=env)

    def agent_log(self, role):
        return (self.scratch / f"agent-{role}.log").read_text(errors="replace").splitlines()

    def agent_mark(self, role):
        return len(self.agent_log(role))

    def agent_mutations(self, role, since):
        out = []
        for l in self.agent_log(role)[since:]:
            m = re.search(r"收到命令: (\w+)", l)
            if m and m.group(1) in MUTATING:
                out.append(m.group(1))
        return out

    def agent_lines(self, role, pattern, since=0):
        return [l for l in self.agent_log(role)[since:] if re.search(pattern, l)]

    def agent_pid(self, role):
        p = self.procs.get(f"agent-{role}")
        if not p:
            return None
        # ip netns exec execs the agent in place
        return p.pid

    def usage(self, role):
        pid = self.agent_pid(role)
        status = pathlib.Path(f"/proc/{pid}/status").read_text()
        rss = int(next(l.split()[1] for l in status.splitlines() if l.startswith("VmRSS")))
        threads = int(next(l.split()[1] for l in status.splitlines() if l.startswith("Threads")))
        stat = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        return {"rss_kib": rss, "fds": len(os.listdir(f"/proc/{pid}/fd")), "threads": threads,
                "cpu_s": round((int(stat[11]) + int(stat[12])) / os.sysconf("SC_CLK_TCK"), 2)}

    def listener_inode(self, role, port):
        out = nsx(NS[role], "ss", "-ltne", f"sport = :{port}", check=False).stdout
        m = re.search(r"ino:(\d+)", out)
        return m.group(1) if m else None

    def nft_has_port(self, port):
        out = nsx(NS["entry"], "nft", "list", "ruleset", check=False).stdout
        return f"dport {port}" in out

    # ------------------------------------------------------------ setup
    def setup(self):
        self.preflight()
        self.network()
        self.fixture = fixture_server()
        self.udp_echo = UDPEcho(FIX_PORT)
        self.proxy = ResetProxy(PANEL_PORT)
        self.panel()
        api = self.api
        api.call("node/create", {"name": "entry-lab", "serverIp": NODE_IP["entry"], "serverIpV4": NODE_IP["entry"], "port": "40000-40099"})
        api.call("node/create", {"name": "hop-lab", "serverIp": NODE_IP["hop"], "serverIpV4": NODE_IP["hop"], "port": "41000-41099"})
        api.call("node/create", {"name": "exit-lab", "serverIp": NODE_IP["exit"], "serverIpV4": NODE_IP["exit"], "port": "42000-42099"})
        rows = {r["server_ip"]: r for r in self.q("select id, secret, server_ip from node")}
        self.nodes = {role: rows[ip] for role, ip in NODE_IP.items()}
        for role in NS:
            self.start_agent(role)
        wait_for("agents online", lambda: all(r["status"] == 1 for r in self.q("select status from node")), timeout=90)
        api.call("tunnel/create", {"name": "T1 entry-hop-exit", "type": 2, "flow": 2, "trafficRatio": 1, "status": 1,
                                   "inNodeId": [{"nodeId": self.nodes["entry"]["id"], "protocol": "tls", "strategy": "round"}],
                                   "chainNodes": [[{"nodeId": self.nodes["hop"]["id"], "protocol": "tls", "strategy": "round"}]],
                                   "outNodeId": [{"nodeId": self.nodes["exit"]["id"], "protocol": "tls", "strategy": "round"}]})
        api.call("tunnel/create", {"name": "T2 entry port", "type": 1, "flow": 2, "trafficRatio": 1, "status": 1,
                                   "inNodeId": [{"nodeId": self.nodes["entry"]["id"], "protocol": "tls", "strategy": "round"}],
                                   "outNodeId": [], "chainNodes": []})
        tunnels = {r["name"]: r["id"] for r in self.q("select id, name from tunnel")}
        self.t1, self.t2 = tunnels["T1 entry-hop-exit"], tunnels["T2 entry port"]
        exp = int(time.time() * 1000) + 30 * 86400000
        api.call("user/create", {"user": "labuser", "pwd": "Reconnect-lab-9", "name": "lab", "flow": 500, "num": 99999, "status": 1, "expTime": exp, "flowResetTime": 1})
        uid = self.q("select id from user where user = 'labuser'")[0]["id"]
        self.user = Identity(uid, "labuser", 1)
        for t in (self.t1, self.t2):
            api.call("tunnel/user/assign", {"userId": uid, "tunnelId": t, "flow": 500, "num": 99999, "expTime": exp, "flowResetTime": 1, "status": 1})
        api.call("forward/create", {"name": "F1", "tunnelId": self.t1, "remoteAddr": f"198.51.142.1:{FIX_PORT}", "mode": "gost"}, self.user, timeout=60)
        api.call("forward/create", {"name": "F2", "tunnelId": self.t2, "remoteAddr": f"198.51.140.1:{FIX_PORT}", "mode": "nftables"}, self.user, timeout=60)
        fw = {r["name"]: r for r in self.q("select f.id, f.name, p.port from forward f join forward_port p on p.forward_id = f.id")}
        self.f1, self.f2 = fw["F1"], fw["F2"]
        chain = {r["node_id"]: r["port"] for r in self.q("select node_id, port from chain_tunnel where tunnel_id = ?", self.t1)}
        self.svc_port = {"entry": self.f1["port"], "hop": chain[self.nodes["hop"]["id"]], "exit": chain[self.nodes["exit"]["id"]]}
        self.log("setup ok: nodes", {r: n["id"] for r, n in self.nodes.items()}, "T1", self.t1, "T2", self.t2,
                 "F1", self.f1["id"], self.f1["port"], "F2", self.f2["id"], self.f2["port"], "service ports", self.svc_port)
        # initial reconnect redeploys of the first connections finish 10 s later
        time.sleep(14)
        d1, d2 = Download(NODE_IP["entry"], self.f1["port"], 2 << 20), Download(NODE_IP["entry"], self.f2["port"], 2 << 20)
        for d in (d1, d2):
            d.start()
        for d in (d1, d2):
            d.join(60)
        assert d1.ok and d2.ok, (d1.error, d2.error)
        self.results["setup"] = {"F1": d1.seconds, "F2": d2.seconds}

    # -------------------------------------------------------- scenarios
    def snapshot(self):
        return {"inodes": {r: self.listener_inode(r, p) for r, p in self.svc_port.items()},
                "nft_f2": self.nft_has_port(self.f2["port"]),
                "panel": self.panel_mark(), "agents": {r: self.agent_mark(r) for r in NS}}

    def wait_redeploy(self, node_id, since, timeout=40):
        return wait_for(f"redeploy of node {node_id}", lambda: self.panel_lines(rf"redeploy: node {node_id} done", since), timeout=timeout, interval=0.5)

    def reconnect_case(self, role, both=False, slow_bytes=12 << 20):
        """Reset one agent's panel connection while a slow download flows
        through T1 (and one through F2); nothing may be re-deployed."""
        node_id = self.nodes[role]["id"]
        before = self.snapshot()
        usage_before = self.usage(role)
        slow = Download(NODE_IP["entry"], self.f1["port"], slow_bytes, delay=0.03)
        slow_nft = Download(NODE_IP["entry"], self.f2["port"], slow_bytes, delay=0.03)
        slow.start()
        slow_nft.start()
        udp = UDPFlow(NODE_IP["entry"], self.f1["port"])
        udp.start()
        time.sleep(1.0)
        self.udp_echo.take()
        t0 = time.monotonic()
        n = self.proxy.reset(AGENT_SRC[role], both=both)
        # new connections right after the reset (nft rules must survive)
        probes = []
        for i in range(8):
            d = Download(NODE_IP["entry"], self.f2["port"], 64 << 10, connect_timeout=2)
            d.run()
            probes.append({"t": round(time.monotonic() - t0, 1), "ok": d.ok, "error": d.error})
            time.sleep(1.5)
        lines = self.wait_redeploy(node_id, before["panel"])
        time.sleep(2)
        udp_res = udp.result()
        udp_res["relay_sessions_at_target"] = len(self.udp_echo.take())
        slow.join(120)
        slow_nft.join(120)
        after = self.snapshot()
        res = {
            "udp_flow": udp_res,
            "reset_conns": n, "both_legs": both,
            "redeploy_line": lines[-1].split("redeploy: ", 1)[1] if lines else None,
            "panel_session_lines": [l.split("[ws] ", 1)[1] for l in self.panel_lines(rf"\[ws\] node {node_id} session ended", before["panel"])],
            "agent_close_lines": self.agent_lines(role, "WebSocket连接已关闭", before["agents"][role]),
            "agent_mutations": self.agent_mutations(role, before["agents"][role]),
            "other_agents_mutations": {r: self.agent_mutations(r, before["agents"][r]) for r in NS if r != role},
            "listener_inodes_before": before["inodes"], "listener_inodes_after": after["inodes"],
            "nft_rule_present_after": after["nft_f2"],
            "new_nft_connections_after_reset": probes,
            "slow_download_t1": {"ok": slow.ok, "bytes": slow.got, "error": slow.error, "seconds": slow.seconds},
            "slow_download_f2": {"ok": slow_nft.ok, "bytes": slow_nft.got, "error": slow_nft.error, "seconds": slow_nft.seconds},
            "usage_before": usage_before, "usage_after": self.usage(role),
        }
        return res

    def r1_agent_side_reset(self):
        out = {}
        for role in ("entry", "hop", "exit"):
            res = self.reconnect_case(role)
            out[role] = res
            self.log("R1", role, res["redeploy_line"], "mutations", res["agent_mutations"], "t1", res["slow_download_t1"]["ok"], "f2", res["slow_download_f2"]["ok"])
            time.sleep(11)  # session close lines are limited to one per 10 s per node
        self.results["R1"] = out

    def r2_both_legs_reset(self):
        res = self.reconnect_case("entry", both=True)
        self.results["R2"] = res
        self.log("R2", res["redeploy_line"], res["panel_session_lines"][-1:] if res["panel_session_lines"] else None)
        time.sleep(11)

    def r3_panel_restart(self):
        before = self.snapshot()
        slow = Download(NODE_IP["entry"], self.f1["port"], 12 << 20, delay=0.03)
        slow.start()
        time.sleep(1)
        self.stop("panel")
        time.sleep(2)
        self.panel()
        lines = {r: self.wait_redeploy(self.nodes[r]["id"], before["panel"], timeout=60) for r in NS}
        slow.join(120)
        after = self.snapshot()
        self.results["R3"] = {
            "redeploy_lines": {r: lines[r][-1].split("redeploy: ", 1)[1] for r in NS},
            "agent_mutations": {r: self.agent_mutations(r, before["agents"][r]) for r in NS},
            "agent_close_lines": {r: self.agent_lines(r, "WebSocket连接已关闭", before["agents"][r]) for r in NS},
            "listener_inodes_before": before["inodes"], "listener_inodes_after": after["inodes"],
            "nft_rule_present_after": after["nft_f2"],
            "slow_download_t1": {"ok": slow.ok, "bytes": slow.got, "error": slow.error},
        }
        self.log("R3", self.results["R3"]["redeploy_lines"], self.results["R3"]["agent_mutations"])
        time.sleep(11)

    def r4_agent_restart(self):
        before = self.snapshot()
        node_id = self.nodes["entry"]["id"]
        self.stop("agent-entry")
        self.start_agent("entry")
        lines = self.wait_redeploy(node_id, before["panel"], timeout=60)
        time.sleep(2)
        d = Download(NODE_IP["entry"], self.f1["port"], 2 << 20)
        d.run()
        dn = Download(NODE_IP["entry"], self.f2["port"], 2 << 20)
        dn.run()
        self.results["R4"] = {
            "redeploy_line": lines[-1].split("redeploy: ", 1)[1],
            "agent_mutations": self.agent_mutations("entry", before["agents"]["entry"]),
            "download_after": {"t1": d.ok, "f2": dn.ok, "errors": [d.error, dn.error]},
        }
        self.log("R4", self.results["R4"]["redeploy_line"], self.results["R4"]["agent_mutations"])
        time.sleep(11)

    def r5_changed_while_offline(self):
        entry_id = self.nodes["entry"]["id"]
        self.proxy.block(AGENT_SRC["entry"])
        wait_for("entry offline", lambda: self.q("select status from node where id = ?", entry_id)[0]["status"] == 0, timeout=70)
        before = self.snapshot()
        upd = self.api.raw("forward/update", {"id": self.f1["id"], "name": "F1", "tunnelId": self.t1, "remoteAddr": f"198.51.141.1:{FIX_PORT}", "mode": "gost"}, self.user)
        self.proxy.unblock(AGENT_SRC["entry"])
        lines = self.wait_redeploy(entry_id, before["panel"], timeout=60)
        d = Download(NODE_IP["entry"], self.f1["port"], 2 << 20)
        d.run()
        self.results["R5"] = {"update_response": {"code": upd.get("code"), "msg": upd.get("msg")},
                              "redeploy_line": lines[-1].split("redeploy: ", 1)[1],
                              "agent_mutations": self.agent_mutations("entry", before["agents"]["entry"]),
                              "download_after": {"ok": d.ok, "error": d.error}}
        self.log("R5", self.results["R5"])
        time.sleep(11)

    def r6_deleted_while_offline(self):
        entry_id = self.nodes["entry"]["id"]
        self.proxy.block(AGENT_SRC["entry"])
        wait_for("entry offline", lambda: self.q("select status from node where id = ?", entry_id)[0]["status"] == 0, timeout=70)
        before = self.snapshot()
        dl = self.api.raw("forward/delete", {"id": self.f2["id"]}, self.user)
        still = self.nft_has_port(self.f2["port"])
        self.proxy.unblock(AGENT_SRC["entry"])
        lines = self.wait_redeploy(entry_id, before["panel"], timeout=60)
        time.sleep(1)
        self.results["R6"] = {"delete_response": {"code": dl.get("code"), "msg": dl.get("msg")},
                              "nft_rule_while_offline": still,
                              "nft_rule_after_reconnect": self.nft_has_port(self.f2["port"]),
                              "redeploy_line": lines[-1].split("redeploy: ", 1)[1],
                              "agent_mutations": self.agent_mutations("entry", before["agents"]["entry"]),
                              "t1_inode_same": before["inodes"]["entry"] == self.listener_inode("entry", self.svc_port["entry"])}
        self.log("R6", self.results["R6"])
        time.sleep(11)

    def r7_old_agent(self):
        if not self.old_agent_bin:
            return
        hop_id = self.nodes["hop"]["id"]
        before = self.snapshot()
        self.stop("agent-hop")
        self.start_agent("hop", self.old_agent_bin)
        first = self.wait_redeploy(hop_id, before["panel"], timeout=60)
        mid = self.snapshot()
        self.proxy.reset(AGENT_SRC["hop"])
        second = self.wait_redeploy(hop_id, mid["panel"], timeout=60)
        d = Download(NODE_IP["entry"], self.f1["port"], 2 << 20)
        d.run()
        self.results["R7"] = {"after_restart": first[-1].split("redeploy: ", 1)[1], "after_reset": second[-1].split("redeploy: ", 1)[1],
                              "agent_mutations_on_reset": self.agent_mutations("hop", mid["agents"]["hop"]),
                              "download": {"ok": d.ok, "error": d.error}}
        self.log("R7", self.results["R7"])
        self.stop("agent-hop")
        self.start_agent("hop")
        self.wait_redeploy(hop_id, mid["panel"] + 1, timeout=60)
        time.sleep(11)

    def r9_storm(self, n=10):
        """n real reconnects of the entry, each after a healthy (>60 s) session
        (shorter sessions put the agent into its failover backoff)."""
        node_id = self.nodes["entry"]["id"]
        before = self.snapshot()
        u0 = self.usage("entry")
        cpu_panel0 = self.panel_cpu()
        done = 0
        for i in range(n):
            wait_for("entry connected", lambda: any(x[0] == AGENT_SRC["entry"] for x in self.proxy.conns), timeout=180, interval=0.5)
            time.sleep(61)
            done += self.proxy.reset(AGENT_SRC["entry"]) > 0
        wait_for("entry connected", lambda: any(x[0] == AGENT_SRC["entry"] for x in self.proxy.conns), timeout=180, interval=0.5)
        time.sleep(14)
        u1 = self.usage("entry")
        redeploys = self.panel_lines(rf"redeploy: node {node_id} done", before["panel"])
        self.results["R9"] = {
            "resets": done,
            "panel_session_lines": len(self.panel_lines(rf"\[ws\] node {node_id} session ended|GET /system-info", before["panel"])),
            "agent_close_lines": len(self.agent_lines("entry", "WebSocket连接已关闭", before["agents"]["entry"])),
            "redeploys": len(redeploys), "redeploy_samples": [l.split("redeploy: ", 1)[1] for l in redeploys[-2:]],
            "agent_mutations": len(self.agent_mutations("entry", before["agents"]["entry"])),
            "agent_lines": self.agent_mark("entry") - before["agents"]["entry"],
            "usage_before": u0, "usage_after": u1, "panel_cpu_s": round(self.panel_cpu() - cpu_panel0, 2),
        }
        self.log("R9", self.results["R9"])

    def panel_cpu(self):
        p = self.procs.get("panel")
        stat = pathlib.Path(f"/proc/{p.pid}/stat").read_text().rsplit(")", 1)[1].split()
        return (int(stat[11]) + int(stat[12])) / os.sysconf("SC_CLK_TCK")

    def run(self, only):
        steps = [("R1", self.r1_agent_side_reset), ("R2", self.r2_both_legs_reset), ("R3", self.r3_panel_restart),
                 ("R4", self.r4_agent_restart), ("R5", self.r5_changed_while_offline), ("R6", self.r6_deleted_while_offline),
                 ("R7", self.r7_old_agent), ("R9", self.r9_storm)]
        for name, fn in steps:
            if only and name not in only:
                continue
            started = time.monotonic()
            try:
                fn()
            finally:
                (self.scratch / "results.json").write_text(json.dumps(self.results, indent=2, ensure_ascii=False))
            self.log(name, "done in", round(time.monotonic() - started, 1), "s")

    def cleanup(self):
        for name in list(self.procs):
            self.stop(name)
        cleanup_namespaces()


def cleanup_namespaces():
    names = sh("ip", "netns", "list", check=False).stdout.split()
    for ns in NS.values():
        if ns in names:
            for pid in sh("ip", "netns", "pids", ns, check=False).stdout.split():
                try:
                    os.kill(int(pid), signal.SIGKILL)
                except ProcessLookupError:
                    pass
    time.sleep(0.5)
    for ns in NS.values():
        if ns in names:
            sh("ip", "netns", "del", ns, check=False)
    for name, *_ in LINKS:
        if sh("ip", "link", "show", name + "a", check=False).returncode == 0:
            sh("ip", "link", "del", name + "a", check=False)


def main():
    p = argparse.ArgumentParser()
    p.add_argument("cmd", choices=["run", "cleanup"])
    p.add_argument("--panel")
    p.add_argument("--agent")
    p.add_argument("--old-agent")
    p.add_argument("--scratch", required=True)
    p.add_argument("--only", default="")
    p.add_argument("--keep", action="store_true")
    a = p.parse_args()
    if a.cmd == "cleanup":
        pids = pathlib.Path(a.scratch) / "pids.json"
        if pids.exists():
            for pid in json.loads(pids.read_text()).values():
                try:
                    os.killpg(pid, signal.SIGKILL)
                except (ProcessLookupError, PermissionError):
                    pass
        cleanup_namespaces()
        return
    lab = Lab(a)
    try:
        lab.setup()
        lab.run(set(x for x in a.only.split(",") if x))
        lab.log("ALL DONE")
    finally:
        if not a.keep:
            lab.cleanup()


if __name__ == "__main__":
    main()
