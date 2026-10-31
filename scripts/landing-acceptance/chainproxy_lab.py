#!/usr/bin/env python3
"""End-to-end lab for 落地「链式代理」 (plan 177). Root only; throwaway
processes in network namespaces; nothing touches production.

Topology (TEST-NET style addresses, no internet):

  host     panel 198.51.140.1:26681, the user's client (fork Core as SOCKS)
  flvxql   FLVX landing: agent + landing Core   198.51.140.2 (host/panel)
                                                198.51.141.1 (→ external)
                                                198.51.142.1 (→ target)
  flvxqx   the user's external node (xray: SS / SOCKS5 / VLESS Reality /
           VMess WS)                            198.51.141.2, 198.51.143.1
  flvxqt   "internet" target: /ip echoes the caller's address, /bytes/N
                                                198.51.142.2, 198.51.143.2

  direct : client → landing → target, the target sees 198.51.142.1
  chained: client → landing → external → target, the target sees 198.51.143.1

  chainproxy_lab.py run --panel paneld --agent flvx-agent --core mmwxc-core \
                        --scratch DIR [--geo DIR] [--keep]
  (core = the agent's pinned Core, geo = the pinned geoip.dat / geosite.dat)
  chainproxy_lab.py cleanup --scratch DIR
"""
import argparse
import base64
import http.client
import json
import os
import pathlib
import secrets
import shutil
import signal
import socket
import sqlite3
import subprocess
import sys
import threading
import time
import urllib.request
import uuid as uuidlib

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import coreclient  # noqa: E402
from flvxapi import Identity, Panel, server_payload, wait_for  # noqa: E402

NS_L, NS_X, NS_T = "flvxql", "flvxqx", "flvxqt"
HOST, LAND = "198.51.140.1", "198.51.140.2"
L_X, X_L = "198.51.141.1", "198.51.141.2"
L_T, T_L = "198.51.142.1", "198.51.142.2"
X_T, T_X = "198.51.143.1", "198.51.143.2"
PANEL_PORT, FIX_PORT, FIX_BLOCKED = 26681, 26682, 26683
SS_PORT, SOCKS_PORT, VLESS_PORT, VMESS_PORT, DEST_PORT = 26684, 26685, 26686, 26687, 26443
SS_PASSWORD, SOCKS_PASS = "chain-proxy-lab", "labpass"
DEST_SNI = "chain.test"
VLESS_ID, VMESS_ID = str(uuidlib.uuid4()), str(uuidlib.uuid4())
LINKS = [  # (name, ns_a, ip_a, ns_b, ip_b)
    ("lcq-hl", None, HOST, NS_L, LAND),
    ("lcq-lx", NS_L, L_X, NS_X, X_L),
    ("lcq-lt", NS_L, L_T, NS_T, T_L),
    ("lcq-xt", NS_X, X_T, NS_T, T_X),
]
ROUTES = [(NS_L, "default", HOST), (NS_X, "198.51.142.0/24", T_X)]


def sh(*args, check=True, **kw):
    return subprocess.run([str(a) for a in args], check=check, capture_output=True, text=True, **kw)


def nsx(ns, *args, check=True):
    return sh("ip", "netns", "exec", ns, *args, check=check)


# ------------------------------------------------------------- fixture


def fixture_main(bind, ports, logfile):
    """Target: /ip → the caller's address; /bytes/N → N pattern bytes. Every
    request is appended to logfile (the fail-closed check reads it)."""
    import http.server
    import socketserver
    import fixtures

    lock = threading.Lock()

    class H(fixtures.Bytes):
        def do_GET(self):
            with lock, open(logfile, "a") as f:
                f.write(f"{self.server.server_address[1]} {self.client_address[0]} {self.path}\n")
            if self.path == "/ip":
                body = self.client_address[0].encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.send_header("Connection", "close")
                self.end_headers()
                self.wfile.write(body)
                return
            super().do_GET()

    class S(http.server.ThreadingHTTPServer):
        daemon_threads = True
        allow_reuse_address = True
        request_queue_size = 256

    for port in ports[1:]:
        threading.Thread(target=S((bind, port), H).serve_forever, daemon=True).start()
    S((bind, ports[0]), H).serve_forever()


# ----------------------------------------------------------------- lab


class Lab:
    def __init__(self, a):
        self.panel_bin, self.agent_bin, self.core_bin = a.panel, a.agent, a.core
        self.scratch = pathlib.Path(a.scratch)
        self.keep = a.keep
        self.geo = a.geo
        self.procs = []
        self.results = {}
        self.jwt_secret = secrets.token_hex(24)

    def log(self, *msg):
        line = " ".join(str(m) for m in msg)
        print(time.strftime("%H:%M:%S"), line, flush=True)

    def start(self, name, argv, ns=None, cwd=None, env=None):
        logf = open(self.scratch / f"{name}.log", "ab")
        cmd = [str(a) for a in argv]
        if ns:
            cmd = ["ip", "netns", "exec", ns] + cmd
        p = subprocess.Popen(cmd, stdout=logf, stderr=subprocess.STDOUT, cwd=cwd, env=env, start_new_session=True)
        self.procs.append((name, p))
        return p

    def stop(self, name):
        for n, p in self.procs:
            if n == name and p.poll() is None:
                os.killpg(p.pid, signal.SIGTERM)
                p.wait(timeout=10)

    def save(self):
        (self.scratch / "results.json").write_text(json.dumps(self.results, indent=2, ensure_ascii=False))

    # ------------------------------------------------------------- setup
    def preflight(self):
        if os.geteuid() != 0:
            raise SystemExit("needs root")
        existing = sh("ip", "netns", "list").stdout.split()
        for ns in (NS_L, NS_X, NS_T):
            if ns in existing:
                raise SystemExit(f"namespace {ns} exists; run cleanup first")
        avail = int(next(l.split()[1] for l in open("/proc/meminfo") if l.startswith("MemAvailable")))
        if avail < 600 * 1024:
            raise SystemExit(f"MemAvailable {avail // 1024} MiB < 600 MiB")
        self.scratch.mkdir(parents=True, exist_ok=True)

    def network(self):
        for ns in (NS_L, NS_X, NS_T):
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
        for ns, cidr, via in ROUTES:
            nsx(ns, "ip", "route", "add", cidr, "via", via)

    def x25519(self):
        out = sh(self.core_bin, "x25519").stdout
        vals = [l.split(":", 1)[1].strip() for l in out.splitlines() if ":" in l]
        return vals[0], vals[1]

    def external_config(self):
        priv, self.reality_pub = self.x25519()
        return {"log": {"loglevel": "warning", "access": "none"}, "inbounds": [
            {"listen": "0.0.0.0", "port": SS_PORT, "protocol": "shadowsocks", "settings": {"method": "aes-256-gcm", "password": SS_PASSWORD, "network": "tcp,udp"}},
            {"listen": "0.0.0.0", "port": SOCKS_PORT, "protocol": "socks", "settings": {"auth": "password", "udp": False, "accounts": [{"user": f"u{i}", "pass": SOCKS_PASS} for i in range(8)]}},
            {"listen": "0.0.0.0", "port": VLESS_PORT, "protocol": "vless", "settings": {"clients": [{"id": VLESS_ID, "flow": "xtls-rprx-vision"}], "decryption": "none"},
             "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {"dest": f"{X_L}:{DEST_PORT}", "serverNames": [DEST_SNI], "privateKey": priv, "shortIds": ["ab12"]}}},
            {"listen": "0.0.0.0", "port": VMESS_PORT, "protocol": "vmess", "settings": {"clients": [{"id": VMESS_ID}]}, "streamSettings": {"network": "ws", "wsSettings": {"path": "/vm"}}},
        ], "outbounds": [{"protocol": "freedom"}]}

    def services(self):
        logfile = self.scratch / "target-requests.log"
        logfile.write_text("")
        self.start("target", [sys.executable, __file__, "fixture", "0.0.0.0", FIX_PORT, FIX_BLOCKED, logfile], ns=NS_T, env=dict(os.environ, PYTHONPATH=str(HERE)))
        d = self.scratch / "external"
        d.mkdir(exist_ok=True)
        (d / "config.json").write_text(json.dumps(self.external_config()))
        self.start_external()
        key, crt = self.scratch / "dest.key", self.scratch / "dest.crt"
        sh("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", crt, "-days", "1", "-subj", "/CN=" + DEST_SNI)
        self.start("reality-dest", [sys.executable, HERE / "tlsdest.py", X_L, DEST_PORT, crt, key], ns=NS_X)
        time.sleep(1.5)

    def start_external(self):
        self.start("external", [self.core_bin, "run", "-config", self.scratch / "external" / "config.json"], ns=NS_X)

    def panel(self):
        env = dict(os.environ, SERVER_ADDR=f"{HOST}:{PANEL_PORT}", DB_TYPE="sqlite", DB_PATH=str(self.scratch / "panel.db"),
                   JWT_SECRET=self.jwt_secret, LOG_DIR=str(self.scratch / "panel-logs"), FLUX_VERSION="3.0.27-chainproxy-lab", GOMAXPROCS="2")
        (self.scratch / "panel-logs").mkdir(exist_ok=True)
        self.start("panel", [self.panel_bin], env=env, cwd=str(self.scratch))
        wait_for("panel", lambda: urllib.request.urlopen(f"http://{HOST}:{PANEL_PORT}/flow/test", timeout=2).status == 200, timeout=60, interval=0.5)
        self.api = Panel(f"http://{HOST}:{PANEL_PORT}", self.jwt_secret)

    def q(self, sql, *args):
        c = sqlite3.connect(f"file:{self.scratch / 'panel.db'}?mode=ro", uri=True, timeout=10)
        c.row_factory = sqlite3.Row
        try:
            return [dict(r) for r in c.execute(sql, args).fetchall()]
        finally:
            c.close()

    def start_agent(self, node_id, secret):
        d = self.scratch / "agent"
        (d / "landing").mkdir(parents=True, exist_ok=True)
        shutil.copy2(self.core_bin, d / "landing" / "flvx-landing-core")
        # A fresh install enables the 大陆 global rules (plan 149): the agent
        # needs the pinned geo files, which it cannot download offline.
        for name in ("geoip.dat", "geosite.dat"):
            src = pathlib.Path(self.geo) / name
            if src.exists():
                shutil.copy2(src, d / "landing" / name)
        (d / "config.json").write_text(json.dumps({"addr": f"{HOST}:{PANEL_PORT}", "secret": secret, "node_id": node_id, "http": 0, "tls": 0, "socks": 0, "block_other": 0}))
        (d / "gost.json").write_text("{}")
        env = dict(os.environ, FLVX_LANDING_TEST_DIRECT="1", FLVX_LANDING_TEST_DIR=str(d / "landing"), GOMAXPROCS="2", TMPDIR=str(self.scratch))
        self.start("agent", [self.agent_bin], ns=NS_L, cwd=str(d), env=env)

    def setup(self):
        self.preflight()
        self.network()
        self.services()
        self.panel()
        api = self.api
        api.call("node/create", {"name": "HK-landing-lab", "serverIp": LAND, "serverIpV4": LAND, "port": "40000-40099"})
        n = self.q("select id, secret from node")[0]
        self.node = n["id"]
        self.start_agent(n["id"], n["secret"])
        wait_for("agent online with egress", lambda: any(r["status"] == 1 and r["landing_capable"] and r["landing_egress"] >= 1 for r in self.q("select status, landing_capable, landing_egress from node")), timeout=90)
        time.sleep(15)  # the panel redeploys a node's runtime 10 s after it connects
        s = api.call("landing/server/enable", {"nodeId": self.node})
        api.call("landing/server/save", server_payload(s, portMin=30000, portMax=30199, maxNodesPerUser=30, status="active"))
        self.server = s["id"]
        api.call("user/create", {"user": "chainproxy", "pwd": "Chain-proxy-lab-9", "name": "chain proxy lab", "flow": 500, "num": 99999, "status": 1, "expTime": int(time.time() * 1000) + 30 * 86400000, "flowResetTime": 1})
        uid = self.q("select id from user where user = 'chainproxy'")[0]["id"]
        self.user = Identity(uid, "chainproxy", 1)
        api.call("landing/user/save", {"userId": uid, "maxNodes": 30, "quotas": [], "serverIds": [self.server]})
        node = api.call("landing/node/create", {"landingServerId": self.server, "name": "我的香港落地", "protocol": "ss2022"}, self.user)
        self.landing = node["id"]
        wait_for("egress ready", lambda: self.q("select egress_ready from landing_server where id = ?", self.server)[0]["egress_ready"] == 1, timeout=60)
        wait_for("landing node active", lambda: self.api.node(self.landing, self.user).get("status") == "active", timeout=60)
        self.link = self.api.direct_link(self.landing, self.user)
        self.client = coreclient.CoreClient(self.core_bin, self.scratch / "client", self.link, 26690)
        (self.scratch / "client").mkdir(exist_ok=True)
        self.client.start()
        self.log("setup ok: node", self.node, "server", self.server, "landing", self.landing, "user", uid)

    # ----------------------------------------------------------- helpers
    def api_port(self):
        return json.loads((self.scratch / "agent" / "landing" / "api.json").read_text())["port"]

    def core_api(self, *args):
        return nsx(NS_L, self.scratch / "agent" / "landing" / "flvx-landing-core", "api", *args, f"--server=127.0.0.1:{self.api_port()}", check=False).stdout

    def outbound_tags(self):
        try:
            return sorted(o.get("tag") for o in json.loads(self.core_api("lso")).get("outbounds", []))
        except ValueError:
            return []

    def core_pid(self):
        for pid in sh("ip", "netns", "pids", NS_L, check=False).stdout.split():
            try:
                if os.readlink(f"/proc/{pid}/exe").endswith("flvx-landing-core"):
                    return int(pid)
            except OSError:
                pass
        return None

    def usage(self):
        pid = self.core_pid()
        status = pathlib.Path(f"/proc/{pid}/status").read_text()
        rss = int(next(l.split()[1] for l in status.splitlines() if l.startswith("VmRSS")))
        fds = os.listdir(f"/proc/{pid}/fd")
        sockets = 0
        for fd in fds:
            try:
                sockets += os.readlink(f"/proc/{pid}/fd/{fd}").startswith("socket:")
            except OSError:
                pass
        try:
            sys_stats = json.loads(self.core_api("statssys"))
            gor, heap = sys_stats.get("NumGoroutine"), sys_stats.get("Alloc")
        except ValueError:
            gor, heap = None, None
        stat = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        return {"rss_kib": rss, "heap_kib": (heap or 0) // 1024, "fds": len(fds), "socket_fds": sockets, "goroutines": gor, "cpu_s": round((int(stat[11]) + int(stat[12])) / os.sysconf("SC_CLK_TCK"), 2)}

    def idle_usage(self):
        samples = []
        for _ in range(5):
            time.sleep(1)
            samples.append(self.usage())
        return {k: min(s[k] for s in samples) for k in ("rss_kib", "heap_kib", "fds", "socket_fds", "goroutines")}

    def http_get(self, path, port=FIX_PORT, timeout=8, local=None):
        s = coreclient.socks_connect(local or self.client.port, T_L, port, timeout)
        try:
            s.sendall(f"GET {path} HTTP/1.1\r\nHost: target\r\nConnection: close\r\n\r\n".encode())
            resp = http.client.HTTPResponse(s)
            resp.begin()
            return resp.read().decode()
        finally:
            s.close()

    def exit_ip(self, port=FIX_PORT):
        try:
            return self.http_get("/ip", port)
        except Exception as e:  # noqa: BLE001
            return f"ERR {type(e).__name__}"

    def add_external(self, link, name):
        return self.api.call("landing/external/create", {"link": link, "name": name}, self.user)

    def chain(self, landing_id, external_id, who=None):
        before = self.q("select revision from landing_server where id = ?", self.server)[0]["revision"]
        out = self.api.call("landing/node/chain/save", {"id": landing_id, "externalId": external_id}, who or self.user, timeout=60)
        # Applied = the agent reported the plan hash the panel sent.
        wait_for("plan applied", lambda: self.q("select egress_state from landing_server where id = ?", self.server)[0]["egress_state"] in ("applied", "none"), timeout=30, interval=0.5)
        time.sleep(1.0)
        return out, before

    def links(self):
        ss = "ss://" + base64.urlsafe_b64encode(f"aes-256-gcm:{SS_PASSWORD}".encode()).decode().rstrip("=") + f"@{X_L}:{SS_PORT}#家里-SS"
        socks = lambda i: f"socks5://u{i}:{SOCKS_PASS}@{X_L}:{SOCKS_PORT}#家里-SOCKS-{i}"  # noqa: E731
        vless = (f"vless://{VLESS_ID}@{X_L}:{VLESS_PORT}?type=tcp&security=reality&sni={DEST_SNI}&pbk={self.reality_pub}&sid=ab12&fp=chrome&flow=xtls-rprx-vision#家里-Reality")
        vm = base64.b64encode(json.dumps({"v": "2", "ps": "家里-VMess", "add": X_L, "port": VMESS_PORT, "id": VMESS_ID, "aid": 0, "net": "ws", "path": "/vm", "tls": ""}).encode()).decode()
        return ss, socks, vless, "vmess://" + vm

    # --------------------------------------------------------- scenarios
    def run(self):
        r = self.results
        pid0 = self.core_pid()
        r["P1_direct_exit"] = self.exit_ip()
        assert r["P1_direct_exit"] == L_T, r
        idle0 = self.idle_usage()
        ss, socks, vless, vmess = self.links()
        ext = {name: self.add_external(link, name)["id"] for name, link in (("家里-SS", ss), ("家里-SOCKS-0", socks(0)), ("家里-Reality", vless), ("家里-VMess", vmess))}
        r["P2"] = {}
        for name in ("家里-SS", "家里-SOCKS-0", "家里-Reality", "家里-VMess"):
            t0 = time.monotonic()
            out, _ = self.chain(self.landing, ext[name])
            ip = self.exit_ip()
            r["P2"][name] = {"exit": ip, "probe": (out.get("probe") or {}), "apply_s": round(time.monotonic() - t0, 2), "core_pid_same": self.core_pid() == pid0,
                             "chain_outbounds": [t for t in self.outbound_tags() if t.startswith("flvxo-x")]}
            self.log("chained via", name, "→ exit", ip, r["P2"][name]["probe"])
            assert ip == X_T, (name, ip)
            assert r["P2"][name]["core_pid_same"], "Core restarted"
            assert len(r["P2"][name]["chain_outbounds"]) == 1, r["P2"][name]
        self.save()
        # P3: block rules keep applying first while chained.
        self.chain(self.landing, ext["家里-SS"])
        self.api.call("landing/node/rules/save", {"id": self.landing, "rules": [{"name": "封锁 26683", "enabled": True, "ports": str(FIX_BLOCKED), "action": "block"}], "defaultAction": ""}, self.user)
        wait_for("rules applied", lambda: self.exit_ip(FIX_BLOCKED).startswith("ERR"), timeout=30, interval=1)
        r["P3_block_first"] = {"blocked_port": self.exit_ip(FIX_BLOCKED), "other_port": self.exit_ip()}
        assert r["P3_block_first"]["other_port"] == X_T, r["P3_block_first"]
        self.api.call("landing/node/rules/save", {"id": self.landing, "rules": [], "defaultAction": ""}, self.user)
        wait_for("rules removed", lambda: self.exit_ip(FIX_BLOCKED) == X_T, timeout=30, interval=1)
        # P4: external node down → connections fail, never direct.
        log = self.scratch / "target-requests.log"
        seen_before = log.read_text().count(f" {L_T} ")
        self.stop("external")
        time.sleep(1)
        fails = [self.exit_ip() for _ in range(5)]
        r["P4_external_down"] = {"results": fails, "direct_requests_seen": log.read_text().count(f" {L_T} ") - seen_before}
        assert all(x.startswith("ERR") for x in fails) and r["P4_external_down"]["direct_requests_seen"] == 0, r["P4_external_down"]
        self.start_external()
        wait_for("external back", lambda: self.exit_ip() == X_T, timeout=20, interval=1)
        r["P4_external_back"] = self.exit_ip()
        # P5: back to 直连, hot.
        self.chain(self.landing, 0)
        r["P5_back_direct"] = {"exit": self.exit_ip(), "core_pid_same": self.core_pid() == pid0, "chain_outbounds": [t for t in self.outbound_tags() if t.startswith("flvxo-x")]}
        assert r["P5_back_direct"]["exit"] == L_T and not r["P5_back_direct"]["chain_outbounds"] and r["P5_back_direct"]["core_pid_same"], r["P5_back_direct"]
        self.save()
        self.resources(ext, socks)
        self.save()
        self.log("ALL PASS")

    def burst(self, n=1000, parallel=50, size=16384):
        """n byte-verified downloads through the client (parallel at a time),
        then the idle figures after 20 s (Go returns garbage lazily)."""
        before = self.idle_usage()
        errors, lock = [], threading.Lock()
        started = time.monotonic()

        def worker():
            for _ in range(n // parallel):
                try:
                    coreclient.download(self.client.port, T_L, FIX_PORT, size, timeout=30)
                except Exception as e:  # noqa: BLE001
                    with lock:
                        errors.append(str(e))
        threads = [threading.Thread(target=worker) for _ in range(parallel)]
        for t in threads:
            t.start()
        peak = self.usage()
        for t in threads:
            t.join()
        seconds = round(time.monotonic() - started, 1)
        right_after = self.usage()
        time.sleep(20)
        after = self.idle_usage()
        assert not errors, errors[:3]
        assert after["fds"] <= before["fds"] + 2 and after["goroutines"] <= before["goroutines"] + 2, (before, after)
        return {"connections": n, "parallel": parallel, "seconds": seconds, "errors": len(errors), "idle_before": before, "during": peak, "right_after": right_after, "idle_after_20s": after}

    def resources(self, ext, socks):
        # Same warmed-up Core for every figure (P1-P5 already ran, now 直连);
        # bulk setup as the admin (acting for the user; not rate limited).
        from flvxapi import ADMIN
        time.sleep(5)
        r = self.results["R"] = {}
        logs0 = {p.name: p.stat().st_size for p in self.scratch.glob("*.log")}
        state = self.scratch / "agent" / "landing"
        # Same 1000-connection burst direct, then chained (control vs chain).
        r["burst_direct"] = self.burst()
        r["idle_0_chained"] = self.idle_usage()
        self.chain(self.landing, ext["家里-SS"], ADMIN)
        r["idle_1_chained"] = self.idle_usage()
        r["burst_chained"] = self.burst()
        # ~20 chained nodes over 7 distinct external nodes (the server's
        # outbound room is 8; identical external nodes share one outbound).
        extra = [self.api.call("landing/external/create", {"link": socks(i), "name": f"家里-SOCKS-{i}", "userId": self.user.uid})["id"] for i in range(1, 7)]
        pool = [ext["家里-SS"]] + extra
        made = []
        for i in range(19):
            n = self.api.call("landing/node/create", {"landingServerId": self.server, "name": f"lab-{i}", "protocol": "ss2022", "userId": self.user.uid})
            made.append(n["id"])
        wait_for("nodes active", lambda: all(x.get("status") == "active" for x in self.api.nodes(self.user)), timeout=60)
        time.sleep(3)
        r["idle_20_nodes_1_chained"] = self.idle_usage()
        for i, nid in enumerate(made):
            self.chain(nid, pool[i % len(pool)], ADMIN)
        r["chain_outbounds_20"] = len([t for t in self.outbound_tags() if t.startswith("flvxo-x")])
        r["idle_20_chained"] = self.idle_usage()
        r["state_files_bytes"] = sum(p.stat().st_size for p in state.glob("*.json"))
        r["burst_20_chained"] = self.burst()
        r["log_growth_bytes"] = {p.name: p.stat().st_size - logs0.get(p.name, 0) for p in self.scratch.glob("*.log") if p.name in ("panel.log", "agent.log", "external.log")}
        panel_logs = self.scratch / "panel-logs"
        r["panel_log_dir_bytes"] = sum(p.stat().st_size for p in panel_logs.rglob("*") if p.is_file())
        r["core_mem_limit_note"] = "lab Core runs without the systemd MemoryMax of production (FLVX_LANDING_TEST_DIRECT)"

    # ----------------------------------------------------------- cleanup
    def cleanup(self):
        if getattr(self, "client", None):
            self.client.stop()
        for name, p in reversed(self.procs):
            if p.poll() is None:
                try:
                    os.killpg(p.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        for name, p in reversed(self.procs):
            try:
                p.wait(timeout=8)
            except subprocess.TimeoutExpired:
                os.killpg(p.pid, signal.SIGKILL)
        cleanup_namespaces()


def cleanup_namespaces():
    existing = sh("ip", "netns", "list", check=False).stdout.split()
    for ns in (NS_L, NS_X, NS_T):
        if ns in existing:
            for pid in sh("ip", "netns", "pids", ns, check=False).stdout.split():
                try:
                    os.kill(int(pid), signal.SIGKILL)
                except ProcessLookupError:
                    pass
            time.sleep(0.2)
            sh("ip", "netns", "del", ns, check=False)
    for name, *_ in LINKS:
        if sh("ip", "link", "show", name + "a", check=False).returncode == 0:
            sh("ip", "link", "del", name + "a", check=False)


def main():
    if len(sys.argv) > 1 and sys.argv[1] == "fixture":
        fixture_main(sys.argv[2], [int(sys.argv[3]), int(sys.argv[4])], sys.argv[5])
        return
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="cmd", required=True)
    run = sub.add_parser("run")
    run.add_argument("--panel", required=True)
    run.add_argument("--agent", required=True)
    run.add_argument("--core", required=True)
    run.add_argument("--scratch", required=True)
    run.add_argument("--keep", action="store_true")
    run.add_argument("--geo", default="/root/tmp-work/landing-egress/geo", help="dir with the pinned geoip.dat / geosite.dat")
    clean = sub.add_parser("cleanup")
    clean.add_argument("--scratch", required=True)
    a = ap.parse_args()
    if a.cmd == "cleanup":
        cleanup_namespaces()
        return
    lab = Lab(a)
    try:
        lab.setup()
        lab.run()
    except BaseException as e:
        lab.results["error"] = f"{type(e).__name__}: {e}"
        lab.save()
        raise
    finally:
        if not a.keep:
            lab.cleanup()


if __name__ == "__main__":
    main()
