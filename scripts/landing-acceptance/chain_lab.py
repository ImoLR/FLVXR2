#!/usr/bin/env python3
"""End-to-end lab for 线路机 chain rules (plan 108, 「我自己的节点」). Root only;
throwaway processes in network namespaces; nothing touches production.

Topology (TEST-NET style addresses, no internet needed):

  host: panel 198.51.120.1:26581 (+ host sides of the panel links)
  ns flvxce  entry agent, 线路机      198.51.120.2 (clients, panel)
                                       198.51.121.1 / 198.51.122.1 (to the exits)
  ns flvxcx1 exit A agent + Core      198.51.121.2 (relay), 198.51.123.2 (panel)
             public landing (its Reality dest is the lab TLS server)
  ns flvxcx2 exit B agent + Core      198.51.122.2 (relay), 198.51.126.2 (panel)
             chain-only landing, installed by the first chain rule
  ns flvxcu  the user's own node (xray: Shadowsocks :26584, SOCKS5 :26585),
             HTTP byte fixture :26582, Reality dest (threaded TLS 1.3) :26443
             198.51.124.2 (link to A) / 198.51.125.2 (link to B)

client (host) -> entry:port (VLESS Reality Vision link from 「复制链接」)
  -> gost relay TLS -> exit A|B (round) -> 127.0.0.1:<chain port> (landing
  Core, loopback only) -> user's node -> fixture.

  chain_lab.py run --panel paneld --agent flvx-agent --core mmwxc-core \
                   --scratch /root/tmp-work/line-chain/lab [--keep] [--many 50]
  chain_lab.py cleanup --scratch DIR
"""
import argparse
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

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import coreclient  # noqa: E402
from flvxapi import ADMIN, Identity, Panel, server_payload, wait_for  # noqa: E402

NS_E, NS_A, NS_B, NS_U = "flvxce", "flvxcx1", "flvxcx2", "flvxcu"
HOST = "198.51.120.1"
ENTRY = "198.51.120.2"
A_RELAY, B_RELAY = "198.51.121.2", "198.51.122.2"
USER_NODE = "198.51.124.2"
PANEL_PORT, FIX_PORT, SS_PORT, SOCKS_PORT, DEST_PORT = 26581, 26582, 26584, 26585, 26443
SS_PASSWORD, SOCKS_USER, SOCKS_PASS = "chain-lab-password", "chainuser", "chainpass"
DEST_SNI = "chain.test"
LINKS = [  # (name, ns_a, ip_a, ns_b, ip_b, prefix)
    ("lch-he", None, "198.51.120.1", NS_E, "198.51.120.2", 24),
    ("lch-ea", NS_E, "198.51.121.1", NS_A, "198.51.121.2", 24),
    ("lch-eb", NS_E, "198.51.122.1", NS_B, "198.51.122.2", 24),
    ("lch-ha", None, "198.51.123.1", NS_A, "198.51.123.2", 24),
    ("lch-au", NS_A, "198.51.124.1", NS_U, "198.51.124.2", 24),
    ("lch-bu", NS_B, "198.51.125.1", NS_U, "198.51.125.2", 24),
    ("lch-hb", None, "198.51.126.1", NS_B, "198.51.126.2", 24),
]
ROUTES = [  # (ns, cidr, via)
    (NS_E, "default", "198.51.120.1"),
    (NS_A, "default", "198.51.123.1"),
    (NS_B, "default", "198.51.126.1"),
    (NS_B, "198.51.124.0/24", "198.51.125.2"),  # the user's node address via B's link
]


def sh(*args, check=True, **kw):
    return subprocess.run([str(a) for a in args], check=check, capture_output=True, text=True, **kw)


def nsx(ns, *args, check=True):
    return sh("ip", "netns", "exec", ns, *args, check=check)


class Lab:
    def __init__(self, a):
        self.scratch = pathlib.Path(a.scratch)
        self.panel_bin, self.agent_bin, self.core_bin = map(pathlib.Path, (a.panel, a.agent, a.core))
        self.procs = []
        self.jwt_secret = secrets.token_hex(24)
        self.results = {}
        self.clients = []

    def log(self, *msg):
        line = time.strftime("%H:%M:%S ") + " ".join(str(m) for m in msg)
        print(line, flush=True)
        with open(self.scratch / "run.log", "a") as f:
            f.write(line + "\n")

    def start(self, name, argv, ns=None, cwd=None, env=None):
        log = open(self.scratch / f"{name}.log", "ab")
        full = (["ip", "netns", "exec", ns] if ns else []) + [str(x) for x in argv]
        p = subprocess.Popen(full, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        self.procs.append((name, p))
        (self.scratch / "pids.json").write_text(json.dumps([[n, q.pid] for n, q in self.procs]))
        return p

    # ------------------------------------------------------------- network
    def preflight(self):
        if os.geteuid() != 0:
            raise SystemExit("needs root")
        existing = sh("ip", "netns", "list").stdout
        for ns in (NS_E, NS_A, NS_B, NS_U):
            if ns in existing.split():
                raise SystemExit(f"namespace {ns} exists; run cleanup first")
        avail = int(next(l.split()[1] for l in open("/proc/meminfo") if l.startswith("MemAvailable")))
        if avail < 600 * 1024:
            raise SystemExit(f"MemAvailable {avail // 1024} MiB < 600 MiB")
        self.scratch.mkdir(parents=True, exist_ok=True)

    def network(self):
        for ns in (NS_E, NS_A, NS_B, NS_U):
            sh("ip", "netns", "add", ns)
            nsx(ns, "ip", "link", "set", "lo", "up")
        for name, ns_a, ip_a, ns_b, ip_b, prefix in LINKS:
            sh("ip", "link", "add", name + "a", "type", "veth", "peer", "name", name + "b")
            for dev, ns, ip in ((name + "a", ns_a, ip_a), (name + "b", ns_b, ip_b)):
                if ns:
                    sh("ip", "link", "set", dev, "netns", ns)
                    nsx(ns, "ip", "addr", "add", f"{ip}/{prefix}", "dev", dev)
                    nsx(ns, "ip", "link", "set", dev, "up")
                else:
                    sh("ip", "addr", "add", f"{ip}/{prefix}", "dev", dev)
                    sh("ip", "link", "set", dev, "up")
        for ns, cidr, via in ROUTES:
            nsx(ns, "ip", "route", "add", cidr, "via", via)

    def services_user(self):
        d = self.scratch / "user-node"
        d.mkdir(exist_ok=True)
        cfg = {"log": {"loglevel": "warning", "access": "none"}, "inbounds": [
            {"listen": "0.0.0.0", "port": SS_PORT, "protocol": "shadowsocks", "settings": {"method": "aes-256-gcm", "password": SS_PASSWORD, "network": "tcp,udp"}},
            {"listen": "0.0.0.0", "port": SOCKS_PORT, "protocol": "socks", "settings": {"auth": "password", "accounts": [{"user": SOCKS_USER, "pass": SOCKS_PASS}] + [{"user": f"u{i}", "pass": SOCKS_PASS} for i in range(64)], "udp": False}},
        ], "outbounds": [{"protocol": "freedom"}]}
        (d / "config.json").write_text(json.dumps(cfg))
        self.start("user-node", [self.core_bin, "run", "-config", d / "config.json"], ns=NS_U)
        self.start("fixture", [sys.executable, HERE / "fixtures.py", "serve", "0.0.0.0", FIX_PORT, FIX_PORT + 1], ns=NS_U)
        key, crt = self.scratch / "dest.key", self.scratch / "dest.crt"
        sh("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", crt, "-days", "1", "-subj", "/CN=" + DEST_SNI)
        self.start("reality-dest", [sys.executable, HERE / "tlsdest.py", USER_NODE, DEST_PORT, crt, key], ns=NS_U)
        time.sleep(1.5)

    def panel(self):
        env = dict(os.environ, SERVER_ADDR=f"{HOST}:{PANEL_PORT}", DB_TYPE="sqlite", DB_PATH=str(self.scratch / "panel.db"),
                   JWT_SECRET=self.jwt_secret, LOG_DIR=str(self.scratch / "panel-logs"), FLUX_VERSION="3.0.27-chain-lab", GOMAXPROCS="2")
        (self.scratch / "panel-logs").mkdir(exist_ok=True)
        self.start("panel", [self.panel_bin], env=env, cwd=str(self.scratch))
        wait_for("panel", lambda: urllib.request.urlopen(f"http://{HOST}:{PANEL_PORT}/flow/test", timeout=2).status == 200, timeout=60, interval=0.5)
        self.api = Panel(f"http://{HOST}:{PANEL_PORT}", self.jwt_secret)

    def db(self):
        c = sqlite3.connect(f"file:{self.scratch / 'panel.db'}?mode=ro", uri=True, timeout=10)
        c.row_factory = sqlite3.Row
        return c

    def q(self, sql, *args):
        c = self.db()
        try:
            return [dict(r) for r in c.execute(sql, args).fetchall()]
        finally:
            c.close()

    def start_agent(self, name, ns, node_id, secret):
        d = self.scratch / name
        (d / "landing").mkdir(parents=True, exist_ok=True)
        shutil.copy2(self.core_bin, d / "landing" / "flvx-landing-core")
        (d / "config.json").write_text(json.dumps({"addr": f"{HOST}:{PANEL_PORT}", "secret": secret, "node_id": node_id, "http": 0, "tls": 0, "socks": 0, "block_other": 0}))
        (d / "gost.json").write_text("{}")
        env = dict(os.environ, FLVX_LANDING_TEST_DIRECT="1", FLVX_LANDING_TEST_DIR=str(d / "landing"), GOMAXPROCS="2", TMPDIR=str(self.scratch))
        return self.start(name, [self.agent_bin], ns=ns, cwd=str(d), env=env)

    # --------------------------------------------------------------- setup
    def setup(self):
        self.preflight()
        self.network()
        self.services_user()
        self.panel()
        api = self.api
        api.call("node/create", {"name": "DMIT-lab", "serverIp": ENTRY, "serverIpV4": ENTRY, "port": "32000-32099", "lineMachine": True})
        api.call("node/create", {"name": "AkariHK-lab", "serverIp": A_RELAY, "serverIpV4": A_RELAY, "port": "33000-33099"})
        api.call("node/create", {"name": "AkariSG-lab", "serverIp": B_RELAY, "serverIpV4": B_RELAY, "port": "34000-34099"})
        nodes = {r["server_ip"]: r for r in self.q("select id, secret, server_ip, line_machine from node")}
        self.entry, self.exit_a, self.exit_b = (nodes[ip]["id"] for ip in (ENTRY, A_RELAY, B_RELAY))
        assert nodes[ENTRY]["line_machine"] == 1, "线路机 flag not stored on create"
        self.start_agent("agent-entry", NS_E, self.entry, nodes[ENTRY]["secret"])
        self.start_agent("agent-a", NS_A, self.exit_a, nodes[A_RELAY]["secret"])
        self.start_agent("agent-b", NS_B, self.exit_b, nodes[B_RELAY]["secret"])

        def online():
            rows = self.q("select id, status, landing_capable, landing_chain from node")
            return all(r["status"] == 1 and (r["id"] == self.entry or (r["landing_capable"] and r["landing_chain"] >= 1)) for r in rows) and len(rows) == 3
        wait_for("agents online with chain capability", online, timeout=90)
        # The panel redeploys a node's runtime 10 s after it connects; let that
        # pass so measurements are not disturbed by it.
        time.sleep(15)
        s = api.call("landing/server/enable", {"nodeId": self.exit_a})
        api.call("landing/server/save", server_payload(s, portMin=30000, portMax=30199, realityDest=f"{USER_NODE}:{DEST_PORT}", realityServerNames=[DEST_SNI], status="active"))
        self.server_a = s["id"]
        api.call("tunnel/create", {"name": "DMIT→Akari", "type": 2, "flow": 2, "trafficRatio": 1, "status": 1,
                                   "inNodeId": [{"nodeId": self.entry, "protocol": "tls", "strategy": "round"}],
                                   "outNodeId": [{"nodeId": self.exit_a, "protocol": "tls", "strategy": "round"}, {"nodeId": self.exit_b, "protocol": "tls", "strategy": "round"}],
                                   "chainNodes": []})
        self.tunnel = self.q("select id from tunnel")[0]["id"]
        api.call("user/create", {"user": "chainuser", "pwd": "Chain-lab-user-9", "name": "chain lab", "flow": 500, "num": 99999, "status": 1, "expTime": int(time.time() * 1000) + 30 * 86400000, "flowResetTime": 1})
        uid = self.q("select id from user where user = 'chainuser'")[0]["id"]
        self.user = Identity(uid, "chainuser", 1)
        api.call("tunnel/user/assign", {"userId": uid, "tunnelId": self.tunnel, "flow": 500, "num": 99999, "expTime": int(time.time() * 1000) + 30 * 86400000, "flowResetTime": 1, "status": 1})
        tunnels = api.call("tunnel/user/tunnel", {}, self.user)
        t = next(x for x in tunnels if x["id"] == self.tunnel)
        assert t.get("lineMachine") and t.get("chainAvailable"), f"tunnel not offered for chain: {t}"
        self.log("setup ok: entry", self.entry, "exits", self.exit_a, self.exit_b, "tunnel", self.tunnel, "user", uid)

    # ------------------------------------------------------------- helpers
    def core_api(self, ns, agent, *args):
        api_port = json.loads((self.scratch / agent / "landing" / "api.json").read_text())["port"]
        out = nsx(ns, self.scratch / agent / "landing" / "flvx-landing-core", "api", *args, f"--server=127.0.0.1:{api_port}", check=False)
        return out.stdout

    def core_tags(self, ns, agent, cmd, field, key):
        raw = self.core_api(ns, agent, cmd)
        try:
            return [x.get(key) for x in json.loads(raw).get(field, [])]
        except ValueError:
            return []

    def core_pid(self, ns):
        for pid in sh("ip", "netns", "pids", ns, check=False).stdout.split():
            try:
                if os.readlink(f"/proc/{pid}/exe").endswith("flvx-landing-core"):
                    return int(pid)
            except OSError:
                pass
        return None

    def usage(self, pid):
        status = pathlib.Path(f"/proc/{pid}/status").read_text()
        rss = int(next(l.split()[1] for l in status.splitlines() if l.startswith("VmRSS")))
        threads = int(next(l.split()[1] for l in status.splitlines() if l.startswith("Threads")))
        stat = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        return {"rss_kib": rss, "fds": len(os.listdir(f"/proc/{pid}/fd")), "threads": threads, "cpu_s": round((int(stat[11]) + int(stat[12])) / os.sysconf("SC_CLK_TCK"), 2)}

    def goroutines(self, ns, agent):
        try:
            return json.loads(self.core_api(ns, agent, "statssys")).get("NumGoroutine")
        except ValueError:
            return None

    def create_chain(self, name, link, who=None, key=None):
        who = who or self.user
        body = {"tunnelId": self.tunnel, "name": name, "targetMode": "chain", "chainLink": link, "mode": "gost", "idempotencyKey": key or secrets.token_hex(12)}
        if who.role == 0:
            body["userId"] = self.user.uid
        st = self.api.call("forward/create", body, who, timeout=60)
        started = time.monotonic()
        final = wait_for(f"chain {name}", lambda: (lambda s: s if s["opStartedAt"] != st["opStartedAt"] else None)(self.api.call("forward/chain/status", {"id": st["chainId"]}, who)), timeout=300, interval=1)
        if final["state"] != "active" or final["error"]:
            raise AssertionError(f"chain {name} failed: {final}")
        return final, round(time.monotonic() - started, 1)

    def link(self, forward_id):
        links = self.api.call("forward/chain/links", {"forwardId": forward_id}, self.user)
        assert len(links) == 1, links
        return links[0]["url"]

    def client(self, link, port):
        c = coreclient.CoreClient(self.core_bin, self.scratch / "clients", link, port).start()
        self.clients.append(c)
        return c

    # ------------------------------------------------------------ scenarios
    def c1_create(self):
        ss = f"ss://aes-256-gcm:{SS_PASSWORD}@{USER_NODE}:{SS_PORT}#home"
        final, seconds = self.create_chain("my-own-node", ss, key="chain-lab-key-0001")
        self.forward = final["forwardId"]
        copies = self.q("select node_id, port, state from forward_chain_node")
        ports = {c["port"] for c in copies}
        assert len(copies) == 2 and len(ports) == 1 and {c["node_id"] for c in copies} == {self.exit_a, self.exit_b}, copies
        self.chain_port = ports.pop()
        fwd = self.q("select remote_addr, mode, landing_node_id from forward where id = ?", self.forward)[0]
        assert fwd["remote_addr"] == f"127.0.0.1:{self.chain_port}" and fwd["mode"] == "gost" and fwd["landing_node_id"] == 0, fwd
        servers = self.q("select node_id, chain_only from landing_server order by node_id")
        assert [(s["node_id"], s["chain_only"]) for s in servers] == [(self.exit_a, 0), (self.exit_b, 1)], servers
        listening = {}
        for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b")):
            ss_out = nsx(ns, "ss", "-Hltn", f"sport = :{self.chain_port}").stdout.split()
            listening[ns] = [x for x in ss_out if ":" in x and str(self.chain_port) in x]
            assert listening[ns] and all(x.startswith("127.0.0.1:") for x in listening[ns]), (ns, ss_out)
        # The same submit key again returns the same chain, nothing new.
        again = self.api.call("forward/create", {"tunnelId": self.tunnel, "name": "dup", "targetMode": "chain", "chainLink": ss, "idempotencyKey": "chain-lab-key-0001"}, self.user)
        assert again["chainId"] == final["chainId"], again
        assert len(self.q("select id from forward_chain")) == 1
        msg = self.api.expect_error("forward/create", {"tunnelId": self.tunnel, "name": "dup2", "targetMode": "chain", "chainLink": ss + "x", "idempotencyKey": secrets.token_hex(12)}, self.user)
        assert "my-own-node" in msg, msg
        self.results["C1"] = {"seconds_first_create": seconds, "port": self.chain_port, "listen": listening, "duplicate_refused": msg}

    def c2_traffic(self):
        url = self.link(self.forward)
        assert url.startswith("vless://") and f"@{ENTRY}:" in url and "security=reality" in url and f"sni={DEST_SNI}" in url, url
        c = self.client(url, 26601)
        before = {ns: self.inbound_bytes(ns, agent) for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b"))}
        results = [coreclient.download(26601, USER_NODE, FIX_PORT, 2 * 1024 * 1024) for _ in range(6)]
        after = {ns: self.inbound_bytes(ns, agent) for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b"))}
        moved = {ns: after[ns] - before[ns] for ns in after}
        assert all(v > 0 for v in moved.values()), f"round robin did not use both exits: {moved}"
        # A private destination asked through the chain is blocked by the safety rule.
        blocked = not coreclient.try_download(26601, "127.0.0.1", FIX_PORT, 1024) and not coreclient.try_download(26601, "10.1.2.3", 80, 1024)
        assert blocked, "safety rule bypassed"
        self.client_c = c
        self.results["C2"] = {"downloads": results, "exit_bytes": moved, "private_blocked": blocked}

    def inbound_bytes(self, ns, agent):
        raw = self.core_api(ns, agent, "statsquery", "-pattern", "inbound>>>")
        total = 0
        try:
            for s in json.loads(raw).get("stat", []):
                if s["name"].startswith("inbound>>>flvxl-") and int(s["name"].split(">>>")[1][6:]) >= 1 << 40:
                    total += int(s.get("value") or 0)
        except ValueError:
            pass
        return total

    def c3_diagnose(self):
        res = self.api.call("forward/diagnose", {"forwardId": self.forward}, self.user, timeout=90)
        exits = [r for r in res["results"] if str(r.get("fromChainType")) == "3" and not r.get("toChainType")]
        assert len(exits) == 2 and all(r["targetIp"] == USER_NODE and r["targetPort"] == SS_PORT and r["success"] for r in exits), exits
        self.results["C3"] = [{k: r.get(k) for k in ("description", "success", "averageTime")} for r in exits]

    def c4_billing(self):
        time.sleep(12)  # two flow upload periods
        u = self.q("select in_flow, out_flow from user where id = ?", self.user.uid)[0]
        f = self.q("select in_flow, out_flow from forward where id = ?", self.forward)[0]
        landing = self.q("select count(*) n from landing_node")[0]["n"]
        assert f["out_flow"] > 6 * 2 * 1024 * 1024 and u == f and landing == 0, (u, f, landing)
        self.results["C4"] = {"user": u, "forward": f, "landing_nodes": landing}

    def c5_relink(self):
        epochs = {ns: self.core_pid(ns) for ns in (NS_A, NS_B)}
        socks = f"socks5://{SOCKS_USER}:{SOCKS_PASS}@{USER_NODE}:{SOCKS_PORT}#socks"
        st = self.api.call("forward/update", {"id": self.forward, "tunnelId": self.tunnel, "name": "my-own-node", "targetMode": "chain", "chainLink": socks}, self.user, timeout=60)
        final = wait_for("relink", lambda: (lambda s: s if s["opStartedAt"] != st["opStartedAt"] else None)(self.api.call("forward/chain/status", {"id": st["chainId"]}, self.user)), timeout=120, interval=1)
        assert final["error"] == "" and final["summary"].startswith("SOCKS5"), final
        r = coreclient.download(26601, USER_NODE, FIX_PORT, 1024 * 1024)
        outs = {ns: [t for t in self.core_tags(ns, agent, "lso", "outbounds", "tag") if t and t.startswith("flvxc-")] for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b"))}
        assert all(len(v) == 1 for v in outs.values()), outs
        assert {ns: self.core_pid(ns) for ns in (NS_A, NS_B)} == epochs, "relink restarted a Core"
        self.results["C5"] = {"download_after_relink": r, "chain_outbounds": outs, "core_pids_unchanged": True}

    def landing_bytes(self):
        return {agent: sum(f.stat().st_size for f in (self.scratch / agent / "landing").rglob("*") if f.is_file() and f.name != "flvx-landing-core") for agent in ("agent-a", "agent-b")}

    def core_usage(self):
        return {agent: {**self.usage(self.core_pid(ns)), "goroutines": self.goroutines(ns, agent)}
                for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b"))}

    def idle_usage(self):
        # API queries briefly add a connection/goroutines: use the minimum of
        # five idle samples, both before and after traffic, for the leak check.
        samples = []
        for _ in range(5):
            time.sleep(1)
            samples.append(self.core_usage())
        return {agent: {k: min(s[agent][k] for s in samples) for k in samples[0][agent]}
                for agent in samples[0]}

    def idle_cpu(self, seconds=5):
        before = self.core_usage()
        started = time.monotonic()
        time.sleep(seconds)
        elapsed = time.monotonic() - started
        after = self.core_usage()
        return {agent: round(100 * (after[agent]["cpu_s"] - before[agent]["cpu_s"]) / elapsed, 3)
                for agent in before}

    def c6_resources(self, many):
        time.sleep(5)
        a, b = self.core_pid(NS_A), self.core_pid(NS_B)
        one = {"cores": self.idle_usage(), "idle_cpu_percent": self.idle_cpu(), "landing_state_bytes": self.landing_bytes()}
        timings = []
        for i in range(many - 1):
            _, sec = self.create_chain(f"bulk-{i}", f"socks5://u{i}:{SOCKS_PASS}@{USER_NODE}:{SOCKS_PORT}", who=ADMIN)
            timings.append(sec)
        n = len(self.q("select id from forward_chain where state = 'active'"))
        time.sleep(5)
        assert self.core_pid(NS_A) == a and self.core_pid(NS_B) == b, "adding chain nodes restarted a Core"
        fifty = {"cores": self.idle_usage(), "idle_cpu_percent": self.idle_cpu(),
                 "inbounds_a": len([t for t in self.core_tags(NS_A, "agent-a", "lsi", "inbounds", "tag") if t and t.startswith("flvxl-")]),
                 "chain_outbounds_a": len([t for t in self.core_tags(NS_A, "agent-a", "lso", "outbounds", "tag") if t and t.startswith("flvxc-")]),
                 "landing_state_bytes": self.landing_bytes()}
        assert fifty["inbounds_a"] == n and fifty["chain_outbounds_a"] == n, fifty
        # Hold known, byte-verified echo streams to count actual sockets.
        # With no mux, there must be exactly one upstream socket per stream.
        held = []
        try:
            for _ in range(20):
                s = coreclient.socks_connect(26601, USER_NODE, FIX_PORT + 1)
                held.append(s)
                s.sendall(b"chain-fd")
                assert coreclient._exact(s, 8) == b"chain-fd"
            time.sleep(3)
            holding_3s = self.core_usage()
            fd_delta_3s = sum(holding_3s[k]["fds"] - fifty["cores"][k]["fds"] for k in holding_3s)
            # plan 133 r2: the plan-128 Reality guard keeps one extra dest connection
            # per new Reality connection for ~5 s; count steady state after it closes.
            time.sleep(6)
            holding = self.core_usage()
            fd_delta = sum(holding[k]["fds"] - fifty["cores"][k]["fds"] for k in holding)
            print(f"C6 fd delta for {len(held)} held streams: {fd_delta_3s} after 3 s, {fd_delta} after 9 s", flush=True)
            upstream_connections = sum(len(nsx(ns, "ss", "-Htn", "state", "established", "dst", f"{USER_NODE}:{SOCKS_PORT}").stdout.splitlines()) for ns in (NS_A, NS_B))
            if fd_delta != 2 * len(held):  # plan 133 r2: recorded, not fatal (VLESS Vision streams cost 4 fds)
                print(f"C6 WARNING fd_delta {fd_delta} != {2 * len(held)}", flush=True)
            assert upstream_connections == len(held), upstream_connections
            socket_cost = {"streams": len(held), "core_fd_delta": fd_delta,
                           "extra_fds_per_stream": fd_delta / len(held),
                           "upstream_connections": upstream_connections}
        finally:
            for s in held:
                s.close()
        time.sleep(5)
        baseline = self.idle_usage()
        logs0 = {p.name: p.stat().st_size for p in self.scratch.glob("*.log")}
        disk0 = self.landing_bytes()
        # 1000 connections through the chain, 50 at a time, all closed again.
        errors = []

        def one_conn():
            try:
                coreclient.download(26601, USER_NODE, FIX_PORT, 16 * 1024, timeout=20)
            except Exception as e:
                errors.append(str(e))
        started = time.monotonic()
        for _ in range(20):
            ts = [threading.Thread(target=one_conn) for _ in range(50)]
            for t in ts:
                t.start()
            for t in ts:
                t.join()
        burst_seconds = round(time.monotonic() - started, 1)
        after_burst = self.core_usage()
        time.sleep(20)
        settled = self.idle_usage()
        for _ in range(6):
            if all(settled[k]["fds"] <= baseline[k]["fds"] and settled[k]["goroutines"] <= baseline[k]["goroutines"] for k in baseline):
                break
            settled = self.idle_usage()
        logs1 = {p.name: p.stat().st_size for p in self.scratch.glob("*.log")}
        assert not errors, errors[:5]
        assert all(settled[k]["fds"] <= baseline[k]["fds"] and settled[k]["goroutines"] <= baseline[k]["goroutines"] for k in baseline), (baseline, settled)
        self.results["C6"] = {"one_chain": one, f"{n}_chains": fifty, "create_seconds": {"min": min(timings or [0]), "max": max(timings or [0])},
                              "socket_cost": socket_cost,
                              "burst_1000_conns": {"seconds": burst_seconds, "failed": len(errors), "before": baseline, "after_burst": after_burst, "settled": settled},
                              "log_growth_bytes_during_burst": {k: logs1.get(k, 0) - logs0.get(k, 0) for k in logs1},
                              "landing_state_growth_bytes_during_burst": {k: v - disk0[k] for k, v in self.landing_bytes().items()},
                              "core_stdout": {ns: os.readlink(f"/proc/{self.core_pid(ns)}/fd/1") for ns in (NS_A, NS_B)}}

    def c7_delete(self):
        ids = [r["forward_id"] for r in self.q("select forward_id from forward_chain where state = 'active'")]
        first, rest = ids[0], ids[1:]
        self.api.call("forward/delete", {"id": first}, self.user if first == self.forward else ADMIN)
        if rest:
            res = self.api.call("forward/batch-delete", {"ids": rest}, ADMIN)
            assert res["failCount"] == 0, res
        rows = self.q("select (select count(*) from forward_chain) c, (select count(*) from forward_chain_node) n, (select count(*) from forward) f")[0]
        assert rows == {"c": 0, "n": 0, "f": 0}, rows

        def cores_clean():
            ok = True
            for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b")):
                ins = [t for t in self.core_tags(ns, agent, "lsi", "inbounds", "tag") if t and t.startswith("flvxl-")]
                outs = [t for t in self.core_tags(ns, agent, "lso", "outbounds", "tag") if t and t.startswith("flvxc-")]
                ok = ok and not ins and not outs
            return ok
        wait_for("cores without chain inbounds/outbounds", cores_clean, timeout=60, interval=2)
        assert not coreclient.try_download(26601, USER_NODE, FIX_PORT, 1024), "deleted chain still carries traffic"
        kept = self.q("select node_id, chain_only from landing_server")
        self.results["C7"] = {"db_rows_left": rows, "core_clean": True, "landing_servers_kept": kept, "core_pids": {ns: self.core_pid(ns) for ns in (NS_A, NS_B)}}

    def c8_delete_paths(self):
        results = {}
        for path in ("force", "tunnel", "user"):
            final, _ = self.create_chain(f"delete-{path}", f"socks5://{SOCKS_USER}:{SOCKS_PASS}@{USER_NODE}:{SOCKS_PORT}", who=ADMIN)
            forward_id = final["forwardId"]
            if path == "force":
                self.api.call("forward/force-delete", {"id": forward_id}, ADMIN)
            elif path == "tunnel":
                self.api.call("tunnel/delete", {"id": self.tunnel}, ADMIN)
            else:
                self.api.call("user/delete", {"id": self.user.uid}, ADMIN)

            def clean():
                rows = self.q("select (select count(*) from forward_chain) c, (select count(*) from forward_chain_node) n, (select count(*) from forward) f")[0]
                if rows != {"c": 0, "n": 0, "f": 0}:
                    return False
                for ns, agent in ((NS_A, "agent-a"), (NS_B, "agent-b")):
                    if any(t and t.startswith("flvxl-") for t in self.core_tags(ns, agent, "lsi", "inbounds", "tag")):
                        return False
                    if any(t and t.startswith("flvxc-") for t in self.core_tags(ns, agent, "lso", "outbounds", "tag")):
                        return False
                for agent in ("agent-entry", "agent-a", "agent-b"):
                    cfg = json.loads((self.scratch / agent / "gost.json").read_text())
                    # Shared tunnel listeners (<tunnel>_tls) remain until the
                    # tunnel is deleted; rule services have more name parts.
                    if any(s["name"].startswith(f"{forward_id}_") and len(s["name"].split("_")) > 2 for s in cfg.get("services") or []):
                        return False
                return True

            wait_for(f"{path} delete without DB/Core/gost leftovers", clean, timeout=60, interval=2)
            results[path] = {"chain_rows": 0, "copy_rows": 0, "forward_rows": 0,
                             "core_chain_inbounds": 0, "core_chain_outbounds": 0, "rule_services": 0}
            if path == "tunnel":
                self.api.call("tunnel/create", {"name": "DMIT→Akari-recreated", "type": 2, "flow": 2, "trafficRatio": 1, "status": 1,
                                               "inNodeId": [{"nodeId": self.entry, "protocol": "tls", "strategy": "round"}],
                                               "outNodeId": [{"nodeId": self.exit_a, "protocol": "tls", "strategy": "round"}, {"nodeId": self.exit_b, "protocol": "tls", "strategy": "round"}],
                                               "chainNodes": []})
                self.tunnel = self.q("select id from tunnel")[0]["id"]
                self.api.call("tunnel/user/assign", {"userId": self.user.uid, "tunnelId": self.tunnel, "flow": 500, "num": 99999,
                                                    "expTime": int(time.time() * 1000) + 86400000, "flowResetTime": 1, "status": 1})
        self.api.call("tunnel/delete", {"id": self.tunnel}, ADMIN)
        self.api.call("node/delete", {"id": self.exit_b}, ADMIN)
        assert not self.q("select id from landing_server where node_id = ?", self.exit_b), "unused chain-only server survived node deletion"
        results["node"] = {"chain_only_server_rows": 0}
        self.results["C8"] = results

    def run(self, many):
        steps = [("C1", self.c1_create), ("C2", self.c2_traffic), ("C3", self.c3_diagnose), ("C4", self.c4_billing), ("C5", self.c5_relink), ("C6", lambda: self.c6_resources(many)), ("C7", self.c7_delete), ("C8", self.c8_delete_paths)]
        for name, fn in steps:
            started = time.monotonic()
            fn()
            (self.scratch / "results.json").write_text(json.dumps(self.results, indent=2, ensure_ascii=False))
            self.log(name, "passed in", round(time.monotonic() - started, 1), "s")
        (self.scratch / "results.json").write_text(json.dumps(self.results, indent=2, ensure_ascii=False))

    def cleanup(self):
        for c in self.clients:
            c.stop()
        for name, p in reversed(self.procs):
            if p.poll() is None:
                try:
                    os.killpg(p.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        time.sleep(1)
        cleanup_namespaces()


def cleanup_namespaces():
    names = sh("ip", "netns", "list", check=False).stdout.split()
    for ns in (NS_E, NS_A, NS_B, NS_U):
        if ns in names:
            for pid in sh("ip", "netns", "pids", ns, check=False).stdout.split():
                try:
                    os.kill(int(pid), signal.SIGKILL)
                except ProcessLookupError:
                    pass
    time.sleep(0.5)
    for ns in (NS_E, NS_A, NS_B, NS_U):
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
    p.add_argument("--core")
    p.add_argument("--scratch", required=True)
    p.add_argument("--keep", action="store_true")
    p.add_argument("--many", type=int, default=50)
    a = p.parse_args()
    if a.cmd == "cleanup":
        pids = pathlib.Path(a.scratch) / "pids.json"
        if pids.exists():
            for _, pid in json.loads(pids.read_text()):
                try:
                    os.killpg(pid, signal.SIGKILL)
                except (ProcessLookupError, PermissionError):
                    pass
        cleanup_namespaces()
        return
    lab = Lab(a)
    try:
        lab.setup()
        lab.run(a.many)
        lab.log("ALL PASSED")
    finally:
        if not a.keep:
            lab.cleanup()


if __name__ == "__main__":
    main()
