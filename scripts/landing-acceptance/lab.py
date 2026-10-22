"""Isolated local lab for the landing acceptance runner (root required).

Layout (nothing touches production, other stacks or system services):

  host netns                                   netns "flvxacc"
  ------------------------------------------   ---------------------------------
  flvxacc0 198.51.100.1/24 (+ .11 .12 .13)  <-> flvxacc1 198.51.100.2/24
           2001:db8:acc::1/64                           2001:db8:acc::2/64
  panel (paneld, SQLite)   198.51.100.1:26481   FLVX agent (go-gost) + its landing
  traffic fixture HTTP/echo 198.51.100.1:26482/3  Core (pinned mmwxc-core, direct mode)
  Reality dest (openssl)   198.51.100.1:26443
  fake mmwx Custom         127.0.0.1:26490
  client Cores (SOCKS)     127.0.0.1:2658x

Every process is started in its own session and recorded; cleanup() stops
them, deletes the namespace and the veth pair, and leaves only the logs.
"""
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
import time
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
NS = "flvxacc"
HOST_IF, NS_IF = "flvxacc0", "flvxacc1"
HOST_IP, NODE_IP = "198.51.100.1", "198.51.100.2"
SOURCE_IPS = ["198.51.100.11", "198.51.100.12", "198.51.100.13"]
HOST_IP6, NODE_IP6 = "2001:db8:acc::1", "2001:db8:acc::2"
PANEL_PORT, HTTP_PORT, ECHO_PORT, REALITY_PORT, CUSTOM_PORT = 26481, 26482, 26483, 26443, 26490
REALITY_SNI = "landing.test"
# Plan 134: a private address the namespace can reach directly (layer-1 safety
# proof) and lab-only names resolved inside the namespace (/etc/netns hosts).
PRIVATE_IP = "10.255.77.1"
LAB_DOMAINS = ["allowed.lab.test", "blocked.lab.test", "usera.lab.test"]
NETNS_ETC = pathlib.Path("/etc/netns") / NS


def sh(*args, check=True, **kw):
    return subprocess.run([str(a) for a in args], check=check, capture_output=True, text=True, **kw)


def port_free(host, port):
    s = socket.socket()
    try:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind((host, port))
        return True
    except OSError:
        return False
    finally:
        s.close()


class Lab:
    def __init__(self, scratch, panel_bin, agent_bin, core_bin, log=print):
        self.scratch = pathlib.Path(scratch)
        self.panel_bin, self.agent_bin, self.core_bin = map(pathlib.Path, (panel_bin, agent_bin, core_bin))
        self.log = log
        self.procs = []  # (name, Popen)
        self.jwt_secret = secrets.token_hex(24)
        self.custom_token = "fake-" + secrets.token_hex(16)
        self.node_id = None
        self.node_secret = None
        self.agent = None
        self.netns_created = False

    # ------------------------------------------------------------------ setup
    def preflight(self):
        if os.geteuid() != 0:
            raise SystemExit("local mode needs root (network namespace)")
        for p in (self.panel_bin, self.agent_bin, self.core_bin):
            if not p.is_file():
                raise SystemExit(f"missing binary {p}")
        if NS in sh("ip", "netns", "list").stdout.split():
            raise SystemExit(f"namespace {NS} already exists (stale run?) - run with --cleanup-only first")
        if sh("ip", "link", "show", HOST_IF, check=False).returncode == 0:
            raise SystemExit(f"interface {HOST_IF} already exists - run with --cleanup-only first")
        for port in (PANEL_PORT, HTTP_PORT, ECHO_PORT, REALITY_PORT):
            if not port_free("0.0.0.0", port):
                raise SystemExit(f"port {port} busy")
        if not port_free("127.0.0.1", CUSTOM_PORT):
            raise SystemExit(f"port {CUSTOM_PORT} busy")
        avail = int(next(l.split()[1] for l in open("/proc/meminfo") if l.startswith("MemAvailable")))
        if avail < 500 * 1024:
            raise SystemExit(f"MemAvailable {avail // 1024} MiB < 500 MiB")
        self.scratch.mkdir(parents=True, exist_ok=True)

    def start(self, name, args, ns=False, cwd=None, env=None):
        log = open(self.scratch / f"{name}.log", "ab")
        argv = (["ip", "netns", "exec", NS] if ns else []) + [str(a) for a in args]
        p = subprocess.Popen(argv, cwd=cwd, env=env, stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        p.argv0 = argv[0]
        self.procs.append((name, p))
        (self.scratch / "pids.json").write_text(json.dumps([[n, q.pid, q.argv0] for n, q in self.procs]))
        return p

    def network(self):
        sh("ip", "netns", "add", NS)
        self.netns_created = True
        sh("ip", "link", "add", HOST_IF, "type", "veth", "peer", "name", NS_IF)
        sh("ip", "link", "set", NS_IF, "netns", NS)
        sh("ip", "addr", "add", HOST_IP + "/24", "dev", HOST_IF)
        for ip in SOURCE_IPS:
            sh("ip", "addr", "add", ip + "/24", "dev", HOST_IF)
        sh("ip", "-6", "addr", "add", HOST_IP6 + "/64", "dev", HOST_IF, "nodad")
        sh("ip", "addr", "add", PRIVATE_IP + "/32", "dev", HOST_IF)
        sh("ip", "link", "set", HOST_IF, "up")
        # `ip netns exec` bind-mounts /etc/netns/<ns>/hosts over /etc/hosts for
        # the agent and its Core only; the host's own /etc/hosts is untouched.
        NETNS_ETC.mkdir(parents=True, exist_ok=True)
        (NETNS_ETC / "hosts").write_text("127.0.0.1 localhost\n::1 localhost\n" + HOST_IP + " " + " ".join(LAB_DOMAINS) + "\n")
        nsx = ("ip", "netns", "exec", NS)
        sh(*nsx, "ip", "addr", "add", NODE_IP + "/24", "dev", NS_IF)
        sh(*nsx, "ip", "-6", "addr", "add", NODE_IP6 + "/64", "dev", NS_IF, "nodad")
        sh(*nsx, "ip", "link", "set", NS_IF, "up")
        sh(*nsx, "ip", "link", "set", "lo", "up")
        # Default routes like a real server: the agent detects its local source
        # address per family (forced IPv4/IPv6 outbounds bind to it).
        sh(*nsx, "ip", "route", "add", "default", "via", HOST_IP)
        sh(*nsx, "ip", "-6", "route", "add", "default", "via", HOST_IP6)

    def fixtures(self):
        self.start("fixtures", [sys.executable, HERE / "fixtures.py", "serve", HOST_IP, HTTP_PORT, ECHO_PORT])
        self.start("fixtures6", [sys.executable, HERE / "fixtures.py", "serve", HOST_IP6, HTTP_PORT, ECHO_PORT])
        self.start("fixtures-private", [sys.executable, HERE / "fixtures.py", "serve", PRIVATE_IP, HTTP_PORT, ECHO_PORT])
        key, crt = self.scratch / "tls.key", self.scratch / "tls.crt"
        sh("openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", key, "-out", crt, "-days", "1", "-subj", "/CN=" + REALITY_SNI)
        # REALITY dials its dest for every connection; since plan 128 the dest
        # leg runs through the guard (dokodemo -> freedom), which closes ~5 s
        # (Xray downlinkOnly) after REALITY drops it. An iterative dest
        # (openssl s_server) then stalls every following connection by 5 s,
        # so the dest must be concurrent (same as chain_lab).
        self.start("reality-dest", [sys.executable, HERE / "tlsdest.py", HOST_IP, REALITY_PORT, crt, key])
        self.start("customfake", [sys.executable, HERE / "customfake.py", "serve", CUSTOM_PORT, self.custom_token])
        for host, port in ((HOST_IP, HTTP_PORT), (HOST_IP, ECHO_PORT), (HOST_IP6, HTTP_PORT), (HOST_IP, REALITY_PORT), ("127.0.0.1", CUSTOM_PORT), (PRIVATE_IP, HTTP_PORT)):
            self.wait_port(host, port)

    def wait_port(self, host, port, timeout=20):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            try:
                socket.create_connection((host, port), 0.5).close()
                return
            except OSError:
                time.sleep(0.2)
        raise RuntimeError(f"{host}:{port} did not open")

    @property
    def panel_url(self):
        return f"http://{HOST_IP}:{PANEL_PORT}"

    def panel(self):
        env = dict(os.environ, SERVER_ADDR=f"{HOST_IP}:{PANEL_PORT}", DB_TYPE="sqlite", DB_PATH=str(self.scratch / "panel.db"),
                   JWT_SECRET=self.jwt_secret, LOG_DIR=str(self.scratch / "panel-logs"), FLUX_VERSION="3.0.27-acceptance", GOMAXPROCS="2")
        (self.scratch / "panel-logs").mkdir(exist_ok=True)
        self.start("panel", [self.panel_bin], env=env, cwd=str(self.scratch))
        deadline = time.monotonic() + 60
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen(self.panel_url + "/flow/test", timeout=2) as r:
                    if r.status == 200:
                        return
            except Exception:
                time.sleep(0.5)
        raise RuntimeError("panel did not become healthy")

    def db(self):
        c = sqlite3.connect(f"file:{self.scratch / 'panel.db'}?mode=ro", uri=True, timeout=10)
        c.row_factory = sqlite3.Row
        return c

    def agent_dir(self):
        return self.scratch / "agent"

    def landing_dir(self):
        return self.agent_dir() / "landing"

    def start_agent(self, node_id, secret):
        self.node_id, self.node_secret = node_id, secret
        d = self.agent_dir()
        (d / "landing").mkdir(parents=True, exist_ok=True)
        core = d / "landing" / "flvx-landing-core"
        if not core.exists():
            shutil.copy2(self.core_bin, core)
        (d / "config.json").write_text(json.dumps({"addr": f"{HOST_IP}:{PANEL_PORT}", "secret": secret, "node_id": node_id, "http": 0, "tls": 0, "socks": 0, "block_other": 0}))
        if not (d / "gost.json").exists():
            (d / "gost.json").write_text("{}")
        env = dict(os.environ, FLVX_LANDING_TEST_DIRECT="1", FLVX_LANDING_TEST_DIR=str(d / "landing"), GOMAXPROCS="2", TMPDIR=str(self.scratch))
        self.agent = self.start("agent", [self.agent_bin], ns=True, cwd=str(d), env=env)
        return self.agent

    def stop_agent(self):
        if self.agent and self.agent.poll() is None:
            os.killpg(self.agent.pid, signal.SIGTERM)
            try:
                self.agent.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(self.agent.pid, signal.SIGKILL)

    def core_pids(self):
        """PIDs of the landing Core inside the namespace."""
        out = sh("ip", "netns", "pids", NS, check=False).stdout.split()
        pids = []
        for pid in out:
            try:
                exe = os.readlink(f"/proc/{pid}/exe")
            except OSError:
                continue
            if exe.endswith("flvx-landing-core"):
                pids.append(int(pid))
        return sorted(pids)

    def proc_usage(self, pid):
        try:
            status = pathlib.Path(f"/proc/{pid}/status").read_text()
            rss = int(next(l.split()[1] for l in status.splitlines() if l.startswith("VmRSS")))
            threads = int(next(l.split()[1] for l in status.splitlines() if l.startswith("Threads")))
            fds = len(os.listdir(f"/proc/{pid}/fd"))
            stat = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
            cpu_ticks = int(stat[11]) + int(stat[12])
            return {"rss_kib": rss, "fds": fds, "threads": threads, "cpu_s": round(cpu_ticks / os.sysconf("SC_CLK_TCK"), 2)}
        except (OSError, StopIteration):
            return None

    # --------------------------------------------------------------- teardown
    def cleanup(self):
        for name, p in reversed(self.procs):
            if p.poll() is None:
                try:
                    os.killpg(p.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
        deadline = time.monotonic() + 8
        for name, p in reversed(self.procs):
            try:
                p.wait(timeout=max(0.1, deadline - time.monotonic()))
            except subprocess.TimeoutExpired:
                try:
                    os.killpg(p.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
        cleanup_namespace()


def cleanup_namespace():
    """Kill whatever still runs in the lab namespace (the Core survives agent
    restarts by design), then remove the namespace and veth pair."""
    if NS in sh("ip", "netns", "list", check=False).stdout.split():
        for pid in sh("ip", "netns", "pids", NS, check=False).stdout.split():
            try:
                os.kill(int(pid), signal.SIGKILL)
            except ProcessLookupError:
                pass
        time.sleep(0.3)
        sh("ip", "netns", "del", NS, check=False)
    if sh("ip", "link", "show", HOST_IF, check=False).returncode == 0:
        sh("ip", "link", "del", HOST_IF, check=False)
    shutil.rmtree(NETNS_ETC, ignore_errors=True)
