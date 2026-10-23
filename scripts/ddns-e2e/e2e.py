#!/usr/bin/env python3
"""Node DDNS netns E2E (plan 142).

The real agent runs in network namespace "flvxddns" with node-48-like SLAAC
addresses on eth0 (CT 240e + CU 2408, IPv4 only private). The real panel
(temporary SQLite) talks to a fake Cloudflare API (cffake.py). Proves:

  * first report → both records created (proxied=false, TTL 120), the node's
    main IPv6 / extra IP switched to the domains once, tunnel entry list shows
    the domains, rule list shows domain:port;
  * CT prefix rotation (ip addr del/add) → exactly one PUT, node fields and
    tunnel entries untouched (no second rewrite / redeploy);
  * an address lifetime refresh (same set) and an idle period → 0 API calls;
  * agent CPU / RSS against the base agent (optional --base-agent).

Usage (root):
  python3 e2e.py --panel BIN --agent BIN [--base-agent BIN] --scratch DIR [--idle 300] [--keep]
"""
import argparse
import json
import os
import pathlib
import shutil
import signal
import sqlite3
import subprocess
import sys
import time
import urllib.request

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE.parent / "landing-acceptance"))
from flvxapi import Panel  # noqa: E402

NS, HOST_IF, NS_TMP_IF = "flvxddns", "fxddh", "fxddn"
HOST_IP, NODE_IP = "10.251.0.1", "10.251.0.2"
PANEL_PORT, CF_PORT = 26600, 26601
CT_OLD = "240e:b8f:a30:1904:ca:8aff:fec0:2813"
CT_NEW = "240e:b8f:72d:5c04:ca:8aff:fec0:2813"
CU = "2408:820c:750b:dc64:ca:8aff:fec0:2813"
CT_DOM, CU_DOM = "e2e-hutai-ct.imgamer.top", "e2e-hutai-cu.imgamer.top"
CF_TOKEN = "e2e-fake-token"


def sh(*args, check=True):
    r = subprocess.run([str(a) for a in args], capture_output=True, text=True)
    if check and r.returncode != 0:
        raise RuntimeError(f"{' '.join(map(str, args))}: {r.stderr.strip()}")
    return r


def nsx(*args, check=True):
    return sh("ip", "netns", "exec", NS, *args, check=check)


class Lab:
    def __init__(self, a):
        self.a = a
        self.scratch = pathlib.Path(a.scratch)
        self.procs = []
        self.result = {}

    def log(self, msg):
        line = time.strftime("%H:%M:%S ") + msg
        print(line, flush=True)
        with open(self.scratch / "e2e.log", "a") as f:
            f.write(line + "\n")

    def start(self, name, argv, env=None, ns=False, cwd=None):
        out = open(self.scratch / f"{name}.log", "ab")
        argv = (["ip", "netns", "exec", NS] if ns else []) + [str(x) for x in argv]
        p = subprocess.Popen(argv, env=env, cwd=cwd, stdout=out, stderr=subprocess.STDOUT, start_new_session=True)
        self.procs.append((name, p))
        return p

    def stop(self, p):
        if p.poll() is None:
            os.killpg(p.pid, signal.SIGTERM)
            try:
                p.wait(timeout=10)
            except subprocess.TimeoutExpired:
                os.killpg(p.pid, signal.SIGKILL)

    def cleanup(self):
        for _, p in reversed(self.procs):
            try:
                self.stop(p)
            except ProcessLookupError:
                pass
        if NS in sh("ip", "netns", "list", check=False).stdout.split():
            for pid in sh("ip", "netns", "pids", NS, check=False).stdout.split():
                sh("kill", "-9", pid, check=False)
            sh("ip", "netns", "del", NS, check=False)
        sh("ip", "link", "del", HOST_IF, check=False)

    # ---- setup ----
    def network(self):
        sh("ip", "netns", "add", NS)
        sh("ip", "link", "add", HOST_IF, "type", "veth", "peer", "name", NS_TMP_IF)
        sh("ip", "link", "set", NS_TMP_IF, "netns", NS)
        sh("ip", "addr", "add", HOST_IP + "/24", "dev", HOST_IF)
        sh("ip", "-6", "addr", "add", "240e:b8f:a30:1904::1/64", "dev", HOST_IF, "nodad")
        sh("ip", "link", "set", HOST_IF, "up")
        nsx("ip", "link", "set", NS_TMP_IF, "name", "eth0")
        nsx("ip", "addr", "add", NODE_IP + "/24", "dev", "eth0")
        nsx("ip", "-6", "addr", "add", CT_OLD + "/64", "dev", "eth0", "nodad")
        nsx("ip", "-6", "addr", "add", CU + "/64", "dev", "eth0", "nodad")
        # Old address still on the NIC but deprecated: must not be reported.
        nsx("ip", "-6", "addr", "add", "240e:b8f:1111:2222::dead/64", "dev", "eth0", "nodad", "preferred_lft", "0")
        nsx("ip", "link", "set", "eth0", "up")
        nsx("ip", "link", "set", "lo", "up")
        nsx("ip", "route", "add", "default", "via", HOST_IP)
        nsx("ip", "-6", "route", "add", "default", "via", "240e:b8f:a30:1904::1")

    def panel(self):
        self.calls_log = self.scratch / "cf-calls.jsonl"
        self.calls_log.write_text("")
        self.start("cffake", [sys.executable, HERE / "cffake.py", CF_PORT, CF_TOKEN, self.calls_log])
        secret = "ddns-e2e-" + str(int(time.time()))
        env = dict(os.environ, SERVER_ADDR=f"{HOST_IP}:{PANEL_PORT}", DB_TYPE="sqlite", DB_PATH=str(self.scratch / "panel.db"),
                   JWT_SECRET=secret, LOG_DIR=str(self.scratch / "panel-logs"), FLUX_VERSION="3.0.27-ddns-e2e",
                   GOMAXPROCS="2", FLVX_DDNS_CF_API=f"http://127.0.0.1:{CF_PORT}")
        (self.scratch / "panel-logs").mkdir(exist_ok=True)
        self.start("panel", [self.a.panel], env=env, cwd=str(self.scratch))
        self.api = Panel(f"http://{HOST_IP}:{PANEL_PORT}", secret)
        deadline = time.time() + 60
        while time.time() < deadline:
            try:
                urllib.request.urlopen(f"http://{HOST_IP}:{PANEL_PORT}/flow/test", timeout=2)
                return
            except Exception:
                time.sleep(0.5)
        raise RuntimeError("panel not healthy")

    def db(self, sql, args=(), write=False):
        c = sqlite3.connect(str(self.scratch / "panel.db"), timeout=15)
        try:
            cur = c.execute(sql, args)
            rows = cur.fetchall()
            if write:
                c.commit()
            return rows
        finally:
            c.close()

    def cf_calls(self):
        return [json.loads(x) for x in self.calls_log.read_text().splitlines() if x.strip()]

    def cf_records(self, name):
        req = urllib.request.Request(f"http://127.0.0.1:{CF_PORT}/zones/zone-e2e/dns_records?name={name}",
                                     headers={"Authorization": "Bearer " + CF_TOKEN, "X-E2E": "1"})
        with urllib.request.urlopen(req, timeout=5) as r:
            return json.load(r)["result"]

    def node_row(self):
        return self.db("SELECT server_ip, server_ip_v6, extra_ips, status FROM node WHERE id = ?", (self.node_id,))[0]

    def node_view(self):
        for n in self.api.call("node/list", {}):
            if n["id"] == self.node_id:
                return n
        raise RuntimeError("node missing")

    def rewrites(self):
        return self.db("SELECT n FROM e2e_rewrites")[0][0]

    def wait(self, what, fn, timeout=40, step=0.25):
        deadline = time.time() + timeout
        while time.time() < deadline:
            v = fn()
            if v:
                return v
            time.sleep(step)
        raise AssertionError("timeout waiting for " + what)

    # ---- agent ----
    def agent_dir(self, tag):
        d = self.scratch / f"agent-{tag}"
        d.mkdir(exist_ok=True)
        (d / "config.json").write_text(json.dumps({"addr": f"{HOST_IP}:{PANEL_PORT}", "secret": self.secret, "node_id": self.node_id,
                                                   "http": 0, "tls": 0, "socks": 0, "block_other": 0}))
        if not (d / "gost.json").exists():
            (d / "gost.json").write_text("{}")
        return d

    def run_agent(self, binary, tag):
        d = self.agent_dir(tag)
        env = dict(os.environ, GOMAXPROCS="2", TMPDIR=str(self.scratch))
        p = self.start("agent-" + tag, [binary], env=env, ns=True, cwd=str(d))
        self.wait("agent online", lambda: self.node_row()[3] == 1, timeout=60)
        return p

    def agent_pid(self, p):
        # `ip netns exec` execs the agent in place: same pid.
        return p.pid

    def usage(self, pid):
        stat = pathlib.Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()
        ticks = int(stat[11]) + int(stat[12])
        status = pathlib.Path(f"/proc/{pid}/status").read_text()
        rss = int([l for l in status.splitlines() if l.startswith("VmRSS:")][0].split()[1])
        threads = int([l for l in status.splitlines() if l.startswith("Threads:")][0].split()[1])
        fds = len(os.listdir(f"/proc/{pid}/fd"))
        return {"cpu_s": ticks / os.sysconf("SC_CLK_TCK"), "rss_kb": rss, "threads": threads, "fds": fds}

    def measure(self, p, seconds):
        pid = self.agent_pid(p)
        time.sleep(5)
        a = self.usage(pid)
        time.sleep(seconds)
        b = self.usage(pid)
        return {"window_s": seconds, "cpu_s": round(b["cpu_s"] - a["cpu_s"], 2),
                "cpu_pct": round(100 * (b["cpu_s"] - a["cpu_s"]) / seconds, 3),
                "rss_kb": b["rss_kb"], "threads": b["threads"], "fds": b["fds"]}

    # ---- scenario ----
    def run(self):
        a = self.a
        self.cleanup()
        self.network()
        self.panel()
        p = self.api.call("node/ddns/provider/create", {"name": "Cloudflare imgamer.top (fake)", "providerType": "cloudflare", "token": CF_TOKEN})
        self.provider_id = p["id"]
        self.log(f"provider {p['id']} tokenStatus={p['tokenStatus']}")
        self.api.call("node/create", {"name": "e2e Leikwanhost 沪台IPLC", "serverIpV4": "", "serverIpV6": CT_OLD, "extraIPs": CU,
                                      "port": "10000-29999", "tcpListenAddr": "[::]", "udpListenAddr": "[::]"})
        self.node_id, self.secret = self.db("SELECT id, secret FROM node ORDER BY id DESC LIMIT 1")[0]
        now = int(time.time() * 1000)
        self.db("INSERT INTO tunnel(id, name, traffic_ratio, type, protocol, flow, created_time, updated_time, status, in_ip, inx) "
                "VALUES(901, 'e2e entry', 1, 1, 'tls', 1, ?, ?, 1, ?, 0)", (now, now, CT_OLD + "," + CU), write=True)
        self.db("INSERT INTO chain_tunnel(tunnel_id, chain_type, node_id, inx, protocol) VALUES(901, '1', ?, 1, 'tls')", (self.node_id,), write=True)
        self.db("INSERT INTO forward(id, user_id, user_name, name, tunnel_id, remote_addr, strategy, in_flow, out_flow, created_time, updated_time, status, inx) "
                "VALUES(901, 1, 'admin_user', 'e2e rule', 901, '1.1.1.1:443', 'fifo', 0, 0, ?, ?, 1, 0)", (now, now), write=True)
        self.db("INSERT INTO forward_port(forward_id, node_id, port) VALUES(901, ?, 10001)", (self.node_id,), write=True)
        self.db("CREATE TABLE IF NOT EXISTS e2e_rewrites(n INTEGER)", write=True)
        self.db("INSERT INTO e2e_rewrites VALUES(0)", write=True)
        self.db("CREATE TRIGGER IF NOT EXISTS e2e_count AFTER UPDATE OF in_ip ON tunnel BEGIN UPDATE e2e_rewrites SET n = n + 1; END", write=True)

        if a.base_agent:
            base = self.run_agent(a.base_agent, "base")
            self.result["agent_base"] = self.measure(base, a.measure)
            self.log(f"base agent: {self.result['agent_base']}")
            self.stop(base)
            self.wait("node offline", lambda: self.node_row()[3] == 0, timeout=30)

        agent = self.run_agent(a.agent, "new")
        # Enable DDNS on the running node (the form sends the stored fields + ddns).
        row = self.node_row()
        body = {"id": self.node_id, "name": "e2e Leikwanhost 沪台IPLC", "serverIpV4": "", "serverIpV6": row[1], "intranetIp": "",
                "extraIPs": "\n".join(json.loads(row[2] or "[]")), "port": "10000-29999", "tcpListenAddr": "[::]", "udpListenAddr": "[::]",
                "ddns": {"enabled": True, "providerId": self.provider_id, "records": [
                    {"domain": CT_DOM, "type": "AAAA", "role": "main", "source": "ct"},
                    {"domain": CU_DOM, "type": "AAAA", "role": "extra", "source": "cu"}]}}
        t0 = time.time()
        self.api.call("node/update", body)
        view = self.wait("DDNS synced", lambda: (self.node_view().get("ddns") or {}).get("status") == "synced" and self.node_view())
        self.result["first_sync_s"] = round(time.time() - t0, 2)
        d = view["ddns"]
        self.log(f"synced in {self.result['first_sync_s']} s; agent reported {d['agentAddrs']}")
        assert d["addrReport"] and sorted(d["agentAddrs"]["v6"]) == sorted([CT_OLD, CU]), d["agentAddrs"]  # kernel order
        ct, cu = self.cf_records(CT_DOM), self.cf_records(CU_DOM)
        assert len(ct) == 1 and ct[0]["content"] == CT_OLD and ct[0]["ttl"] == 120 and not ct[0]["proxied"], ct
        assert len(cu) == 1 and cu[0]["content"] == CU, cu
        row = self.node_row()
        assert row[0] == CT_DOM and row[1] == CT_DOM and json.loads(row[2]) == [CU_DOM], row
        tunnel = self.db("SELECT in_ip FROM tunnel WHERE id = 901")[0][0]
        assert tunnel == CT_DOM + "," + CU_DOM, tunnel
        rules = self.api.call("forward/list", {})
        items = rules.get("items", rules) if isinstance(rules, dict) else rules
        entry = [r for r in items if r["id"] == 901][0]["inIp"]
        assert entry == f"{CT_DOM}:10001,{CU_DOM}:10001", entry
        rewrites_after_switch = self.rewrites()
        calls_after_switch = len(self.cf_calls())
        self.result["switch"] = {"node": list(row), "tunnel_in_ip": tunnel, "rule_entries": entry,
                                 "tunnel_rewrites": rewrites_after_switch, "cf_calls": calls_after_switch,
                                 "cf_call_list": [c["method"] + " " + c["path"].split("/dns_records")[0][-9:] + ("/dns_records" if "dns_records" in c["path"] else "") for c in self.cf_calls()]}
        self.log(f"switch: {self.result['switch']}")

        # Lifetime refresh of an existing address (same set): no API call.
        nsx("ip", "-6", "addr", "change", CU + "/64", "dev", "eth0", "valid_lft", "7200", "preferred_lft", "3600")
        time.sleep(6)
        assert len(self.cf_calls()) == calls_after_switch, "lifetime refresh caused API calls"

        # CT prefix rotation like node 48 on 10-23.
        t0 = time.time()
        nsx("ip", "-6", "addr", "del", CT_OLD + "/64", "dev", "eth0")
        nsx("ip", "-6", "addr", "add", CT_NEW + "/64", "dev", "eth0", "nodad")
        self.wait("PUT after rotation", lambda: any(c["method"] == "PUT" for c in self.cf_calls()[calls_after_switch:]), timeout=30, step=0.1)
        self.result["rotation_to_put_s"] = round(time.time() - t0, 2)
        time.sleep(3)
        new_calls = self.cf_calls()[calls_after_switch:]
        ct = self.cf_records(CT_DOM)
        row = self.node_row()
        self.result["rotation"] = {"cf_calls": [c["method"] for c in new_calls], "ct_record": ct[0]["content"],
                                   "node": list(row), "tunnel_rewrites": self.rewrites() - rewrites_after_switch}
        self.log(f"rotation: {self.result['rotation']} in {self.result['rotation_to_put_s']} s")
        assert [c["method"] for c in new_calls] == ["PUT"], new_calls
        assert ct[0]["content"] == CT_NEW and row[1] == CT_DOM and self.rewrites() == rewrites_after_switch

        # Idle: no API calls (drift check is hourly), steady agent.
        calls = len(self.cf_calls())
        self.result["agent_new"] = self.measure(agent, a.measure)
        if a.idle > a.measure:
            time.sleep(a.idle - a.measure)
        self.result["idle"] = {"seconds": max(a.idle, a.measure), "cf_calls": len(self.cf_calls()) - calls}
        self.log(f"new agent: {self.result['agent_new']}; idle {self.result['idle']}")
        assert self.result["idle"]["cf_calls"] == 0
        deletes = [c for c in self.cf_calls() if c["method"] == "DELETE"]
        assert not deletes, deletes
        panel_log = (self.scratch / "panel.log").read_text(errors="replace")
        self.result["panel_ddns_log_lines"] = [l[20:] for l in panel_log.splitlines() if "[ddns]" in l]
        (self.scratch / "result.json").write_text(json.dumps(self.result, indent=2, ensure_ascii=False))
        self.log("PASS")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--panel", required=True)
    ap.add_argument("--agent", required=True)
    ap.add_argument("--base-agent")
    ap.add_argument("--scratch", required=True)
    ap.add_argument("--measure", type=int, default=120)
    ap.add_argument("--idle", type=int, default=300)
    ap.add_argument("--keep", action="store_true")
    a = ap.parse_args()
    pathlib.Path(a.scratch).mkdir(parents=True, exist_ok=True)
    lab = Lab(a)
    try:
        lab.run()
    finally:
        if not a.keep:
            lab.cleanup()


if __name__ == "__main__":
    main()
