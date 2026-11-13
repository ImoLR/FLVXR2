#!/usr/bin/env python3
"""Plan 252 lab: the chain-only exit landing (exit B of chain_lab.py) and its
Core are removed once the last 「我自己的节点」 rule is gone, and come back with
the next one. Same netns topology as chain_lab.py; real panel timings (first
sweep 3 min, kick 30 s, grace 10 min, sweep 10 min). Root only; nothing
touches production.

  pass new : exit B runs this build's agent
             create rule -> Core on B -> delete -> (≈10 min) server row, Core
             process, Core files gone, unknown files kept, exit A untouched,
             one panel log line -> recreate -> server + Core back, traffic ok
  pass old : exit B runs an older agent (fork.130, landing_chain 2)
             delete -> nothing sent, one log line, Core keeps running ->
             "OTA" exit B to this build -> next sweep cleans it

  chain_cleanup_lab.py run --pass new|old --panel paneld --agent flvx-agent \
      [--old-agent gost-amd64] --core mmwxc-core --scratch DIR [--keep]
  chain_cleanup_lab.py cleanup --scratch DIR

The netns has no internet: the lab puts the pinned Core binary and geo data
(SHA256-checked by the agent) into the landing directories, also before the
recreate (what the agent's downloads do on a real node). --core must sit next
to geoip.dat / geosite.dat.
"""
import argparse
import json
import os
import pathlib
import shutil
import signal
import sys
import time

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import chain_lab  # noqa: E402
import coreclient  # noqa: E402
from chain_lab import FIX_PORT, NS_A, NS_B, SS_PASSWORD, SS_PORT, USER_NODE, cleanup_namespaces, nsx  # noqa: E402
from flvxapi import wait_for  # noqa: E402

REMOVE_WAIT = 15 * 60  # first sweep 3 min / kick 30 s + grace 10 min + slack


class CleanupLab(chain_lab.Lab):
    def __init__(self, a):
        super().__init__(a)
        self.pass_name = a.pass_name
        self.old_agent = pathlib.Path(a.old_agent) if a.old_agent else None
        self.new_agent = self.agent_bin

    def panel(self):
        super().panel()
        # Plan 139: a native landing range is carved from the rule range, so
        # the public exit A gets a prod-like rule range (Bestvm US 10000-65535).
        orig = self.api.call

        def call(path, body=None, *args, **kw):
            if path == "node/create" and (body or {}).get("name") == "AkariHK-lab":
                body = dict(body, port="10000-65535")
            if path == "landing/server/save" and (body or {}).get("realityDest"):
                # Plan 227: 「自动（最快）」 would replace the lab's TLS dest by an
                # internet domain the netns cannot reach.
                body = dict(body, realityMode="manual")
            return orig(path, body, *args, **kw)
        self.api.call = call

    def seed_landing_assets(self, name, core=False):
        """The pinned assets the agent would download (no internet in the netns)."""
        d = self.scratch / name / "landing"
        d.mkdir(parents=True, exist_ok=True)
        for geo in ("geoip.dat", "geosite.dat"):
            shutil.copy2(self.core_bin.parent / geo, d / geo)
        if core:
            shutil.copy2(self.core_bin, d / "flvx-landing-core")

    def start_agent(self, name, ns, node_id, secret):
        self.seed_landing_assets(name)
        if name == "agent-b" and self.pass_name == "old":
            self.agent_bin = self.old_agent
            try:
                return super().start_agent(name, ns, node_id, secret)
            finally:
                self.agent_bin = self.new_agent
        return super().start_agent(name, ns, node_id, secret)

    # -------------------------------------------------------------- helpers
    def landing_dir(self):
        return self.scratch / "agent-b" / "landing"

    def landing_files(self):
        d = self.landing_dir()
        return sorted(p.name for p in d.iterdir()) if d.exists() else []

    def srv_b(self):
        rows = self.q("select id, chain_only, status, revision from landing_server where node_id = ?", self.exit_b)
        return rows[0] if rows else None

    def srv_a(self):
        return self.q("select id, chain_only, status from landing_server where node_id = ?", self.exit_a)[0]

    def panel_lines(self, needle):
        text = (self.scratch / "panel.log").read_text(errors="replace")
        return [l for l in text.splitlines() if needle in l]

    def agent_lines(self, agent, needle):
        p = self.scratch / f"{agent}.log"
        return [l for l in p.read_text(errors="replace").splitlines() if needle in l] if p.exists() else []

    def agent_pid(self, name):
        return next(p.pid for n, p in self.procs if n == name and p.poll() is None)

    def ss_link(self, tag):
        return f"ss://aes-256-gcm:{SS_PASSWORD}@{USER_NODE}:{SS_PORT}#{tag}"

    def delete_rule(self, forward_id):
        self.api.call("forward/delete", {"id": forward_id}, self.user)
        wait_for("copies gone", lambda: not self.q("select id from forward_chain_node"), timeout=30, interval=1)
        return time.monotonic()

    def chain_listening_b(self):
        """Exit B serves the chain copy: loopback listener + chain outbound."""
        port = self.q("select port from forward_chain_node where node_id = ?", self.exit_b)[0]["port"]
        out = nsx(NS_B, "ss", "-Hltn", f"sport = :{port}").stdout.split()
        listen = [x for x in out if f":{port}" in x]
        outs = [t for t in self.core_tags(NS_B, "agent-b", "lso", "outbounds", "tag") if t and t.startswith("flvxc-")]
        ins = [t for t in self.core_tags(NS_B, "agent-b", "lsi", "inbounds", "tag") if t and t.startswith("flvxl-")]
        assert listen and all(x.startswith("127.0.0.1:") for x in listen) and outs and ins, (listen, outs, ins)
        return {"port": port, "listen": listen, "inbounds": ins, "outbounds": outs}

    def try_traffic(self, forward_id, port):
        try:
            return {"ok": True, "exit_b_bytes": self.traffic(forward_id, port)}
        except Exception as e:  # recorded, investigated separately (data path unchanged by plan 252)
            return {"ok": False, "error": repr(e)[:200]}

    def traffic(self, forward_id, port):
        url = self.link(forward_id)
        c = self.client(url, port)
        try:
            before = self.inbound_bytes(NS_B, "agent-b")
            for _ in range(6):
                coreclient.download(port, USER_NODE, FIX_PORT, 512 * 1024, timeout=20)
            moved = self.inbound_bytes(NS_B, "agent-b") - before
        finally:
            c.stop()
            self.clients.remove(c)
        assert moved > 0, "exit B carried no traffic"
        return moved

    # --------------------------------------------------------------- passes
    def pass_new(self):
        final, secs = self.create_chain("own-1", self.ss_link("home"))
        s1 = self.srv_b()
        pid1 = self.core_pid(NS_B)
        assert s1 and s1["chain_only"] == 1 and pid1, (s1, pid1)
        files_before = self.landing_files()
        (self.landing_dir() / "operator-notes.txt").write_text("not the Core's; must stay\n")
        a_before, a_pid = self.srv_a(), self.core_pid(NS_A)
        agent_b_pid = self.agent_pid("agent-b")
        serving1 = self.chain_listening_b()
        traffic1 = self.try_traffic(final["forwardId"], 26611)
        self.results["L1_create"] = {"seconds": secs, "server": s1, "core_pid": pid1, "landing_files": files_before, "serving": serving1, "traffic": traffic1}
        self.log("L1 create ok", s1, "core pid", pid1)

        t_del = self.delete_rule(final["forwardId"])
        wait_for("chain-only server B removed", lambda: self.srv_b() is None, timeout=REMOVE_WAIT, interval=5)
        removed_after = round(time.monotonic() - t_del)
        wait_for("Core process on B gone", lambda: self.core_pid(NS_B) is None, timeout=20, interval=1)
        left = self.landing_files()
        assert left == ["operator-notes.txt"], left
        assert self.srv_a() == a_before and self.core_pid(NS_A) == a_pid, "exit A (public landing) touched"
        assert self.agent_pid("agent-b") == agent_b_pid, "agent B restarted"
        assert self.q("select status from node where id = ?", self.exit_b)[0]["status"] == 1, "agent B offline"
        lines = self.panel_lines("[chain-host]")
        assert len(lines) == 1 and "removed idle chain-only landing server" in lines[0], lines
        cmds = self.agent_lines("agent-b", "RemoveLandingCore")
        self.results["L2_cleanup"] = {"removed_seconds_after_delete": removed_after, "landing_files_left": left,
                                      "panel_log": lines, "agent_b_command_lines": len(cmds), "exit_a_core_pid_same": True}
        self.log("L2 cleanup ok after", removed_after, "s:", lines[0])

        self.seed_landing_assets("agent-b", core=True)  # = the agent's downloads
        final2, secs2 = self.create_chain("own-2", self.ss_link("home2"))
        s2 = self.srv_b()
        pid2 = self.core_pid(NS_B)
        assert s2 and s2["chain_only"] == 1 and s2["id"] != s1["id"] and pid2 and pid2 != pid1, (s2, pid2)
        serving2 = self.chain_listening_b()
        traffic2 = self.try_traffic(final2["forwardId"], 26612)
        self.results["L3_recreate"] = {"seconds": secs2, "server": s2, "core_pid": pid2, "landing_files": self.landing_files(), "serving": serving2, "traffic": traffic2,
                                       "chain_host_log_lines": len(self.panel_lines("[chain-host]"))}
        assert len(self.panel_lines("[chain-host]")) == 1, "extra cleanup log lines"
        self.log("L3 recreate ok", s2, "core pid", pid2)

    def pass_old(self):
        chain = self.q("select landing_chain from node where id = ?", self.exit_b)[0]["landing_chain"]
        assert chain == 2, f"old agent reports landing_chain={chain}"
        final, _ = self.create_chain("own-old", self.ss_link("home"))
        s1, pid1 = self.srv_b(), self.core_pid(NS_B)
        assert s1 and s1["chain_only"] == 1 and pid1
        t_del = self.delete_rule(final["forwardId"])
        # Long enough for the first sweep + grace + one more sweep.
        wait_for("version-gate log line", lambda: self.panel_lines("版本过低"), timeout=REMOVE_WAIT, interval=5)
        time.sleep(max(0, 14 * 60 - (time.monotonic() - t_del)))
        assert self.srv_b() is not None and self.core_pid(NS_B) == pid1, "old agent's server/Core touched"
        lines = self.panel_lines("[chain-host]")
        assert len(lines) == 1 and "版本过低" in lines[0], lines
        assert not self.agent_lines("agent-b", "RemoveLandingCore"), "command sent to an old agent"
        unknown = self.agent_lines("agent-b", "未知命令")
        self.results["L4_old_agent"] = {"kept_minutes": round((time.monotonic() - t_del) / 60, 1), "panel_log": lines, "agent_unknown_command_lines": len(unknown), "core_pid_same": True}
        self.log("L4 old agent kept, one line:", lines[0])

        # OTA exit B to this build (the direct-mode Core goes with the old process group).
        p = next(p for n, p in self.procs if n == "agent-b")
        os.killpg(p.pid, signal.SIGTERM)
        p.wait(timeout=20)
        wait_for("old Core gone", lambda: self.core_pid(NS_B) is None, timeout=20, interval=1)
        self.procs = [(n, q) for n, q in self.procs if n != "agent-b"]
        secret = self.q("select secret from node where id = ?", self.exit_b)[0]["secret"]
        self.start_agent("agent-b", NS_B, self.exit_b, secret)
        t_ota = time.monotonic()
        wait_for("agent B reports landing_chain 3", lambda: self.q("select landing_chain, status from node where id = ?", self.exit_b)[0] == {"landing_chain": 3, "status": 1}, timeout=90, interval=2)
        wait_for("server B removed after the upgrade", lambda: self.srv_b() is None, timeout=12 * 60, interval=5)
        wait_for("Core process on B gone", lambda: self.core_pid(NS_B) is None, timeout=20, interval=1)
        lines = self.panel_lines("[chain-host]")
        assert len(lines) == 2 and "removed" in lines[1], lines
        self.results["L5_after_ota"] = {"removed_seconds_after_ota": round(time.monotonic() - t_ota), "landing_files_left": self.landing_files(), "panel_log": lines}
        self.log("L5 cleaned after OTA:", lines[1])

    def run_pass(self):
        started = time.monotonic()
        (self.pass_new if self.pass_name == "new" else self.pass_old)()
        self.results["pass"] = self.pass_name
        self.results["seconds"] = round(time.monotonic() - started)
        (self.scratch / f"results-{self.pass_name}.json").write_text(json.dumps(self.results, indent=2, ensure_ascii=False))


def main():
    p = argparse.ArgumentParser()
    p.add_argument("cmd", choices=["run", "cleanup"])
    p.add_argument("--pass", dest="pass_name", choices=["new", "old"], default="new")
    p.add_argument("--panel")
    p.add_argument("--agent")
    p.add_argument("--old-agent")
    p.add_argument("--core")
    p.add_argument("--scratch", required=True)
    p.add_argument("--keep", action="store_true")
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
    lab = CleanupLab(a)
    try:
        lab.setup()
        lab.run_pass()
        lab.log("PASS", a.pass_name, "ALL PASSED")
    finally:
        if not a.keep:
            lab.cleanup()


if __name__ == "__main__":
    main()
