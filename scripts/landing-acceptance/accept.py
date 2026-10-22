#!/usr/bin/env python3
"""FLVX landing acceptance runner (plan 110 matrix + plan 107 outbound/routing).

Local mode (isolated lab, root):
  accept.py local --panel BIN --agent BIN --core BIN [--scratch DIR] [--only S1,S4] [--keep]
  accept.py cleanup --scratch DIR
Production mode (temporary user, cleans up on exit, never edits existing data):
  FLVX_URL=http://127.0.0.1:63665 FLVX_JWT_SECRET=... accept.py prod --native-server 1 --shared-server 2 ...
  (see README.md)

Every scenario prints PASS/FAIL lines; the JSON report lists each check with
its evidence. Exit code 0 only if every selected scenario passed.
"""
import argparse
import json
import os
import pathlib
import signal
import sys
import time
import traceback

HERE = pathlib.Path(__file__).resolve().parent
sys.dont_write_bytecode = True  # keep the repo free of __pycache__
sys.path.insert(0, str(HERE))

import flvxapi  # noqa: E402
from flvxapi import ADMIN, Identity, Panel, wait_for  # noqa: E402


class Check(Exception):
    pass


class Report:
    def __init__(self, path):
        self.path = pathlib.Path(path)
        self.scenarios = []
        self.current = None
        self.started = time.time()

    def begin(self, sid, title):
        self.current = {"id": sid, "title": title, "checks": [], "status": "RUNNING", "started": time.time()}
        self.scenarios.append(self.current)
        print(f"\n=== {sid} {title}", flush=True)

    def ok(self, name, evidence=None):
        self.current["checks"].append({"check": name, "ok": True, "evidence": evidence})
        print(f"  PASS {name}" + (f"  {short(evidence)}" if evidence is not None else ""), flush=True)

    def fail(self, name, evidence):
        self.current["checks"].append({"check": name, "ok": False, "evidence": evidence})
        print(f"  FAIL {name}  {short(evidence)}", flush=True)

    def end(self, error=None):
        cur = self.current
        if error:
            cur["error"] = error
        cur["status"] = "PASS" if not error and all(c["ok"] for c in cur["checks"]) else "FAIL"
        cur["seconds"] = round(time.time() - cur["started"], 1)
        print(f"--- {cur['id']} {cur['status']} ({cur['seconds']} s)", flush=True)
        self.save()

    def save(self):
        self.path.write_text(json.dumps({"started": self.started, "scenarios": self.scenarios}, ensure_ascii=False, indent=2, default=str))

    def matrix(self):
        lines = ["", "Scenario matrix:"]
        for s in self.scenarios:
            n_ok = sum(c["ok"] for c in s["checks"])
            lines.append(f"  {s['id']:<4} {s['status']:<4} {n_ok}/{len(s['checks'])} checks  {s['title']}")
        return "\n".join(lines)

    @property
    def passed(self):
        return all(s["status"] == "PASS" for s in self.scenarios)


def short(v, n=300):
    t = v if isinstance(v, str) else json.dumps(v, ensure_ascii=False, default=str)
    return t if len(t) <= n else t[: n - 3] + "..."


def expect(report, name, cond, evidence=None):
    if cond:
        report.ok(name, evidence)
    else:
        report.fail(name, evidence)
        raise Check(name)


def run_scenarios(ctx, selected, scenarios):
    report = ctx.report
    for sid, title, fn in scenarios:
        if selected and sid not in selected:
            continue
        report.begin(sid, title)
        try:
            fn(ctx)
            report.end()
        except Check as e:
            report.end(f"check failed: {e}")
        except Exception as e:  # noqa: BLE001 - report and keep going
            report.end(f"{type(e).__name__}: {e}\n{traceback.format_exc(limit=6)}")
    return report


def main():
    ap = argparse.ArgumentParser()
    sub = ap.add_subparsers(dest="mode", required=True)
    lp = sub.add_parser("local")
    lp.add_argument("--panel", required=True)
    lp.add_argument("--agent", required=True)
    lp.add_argument("--core", required=True)
    lp.add_argument("--scratch", default="/root/tmp-work/landing-acceptance")
    lp.add_argument("--report", default=None)
    lp.add_argument("--only", default="")
    lp.add_argument("--keep", action="store_true", help="leave the lab running after the run")
    lp.add_argument("--geo", default="", help="dir with the pinned geoip.dat/geosite.dat (L2 seeds them instead of downloading)")
    cp = sub.add_parser("cleanup")
    cp.add_argument("--scratch", default="/root/tmp-work/landing-acceptance")
    pp = sub.add_parser("prod")
    pp.add_argument("--native-server", type=int, required=True, help="landing_server id of a native landing")
    pp.add_argument("--shared-server", type=int, default=0, help="landing_server id of a ready mmwx-shared server")
    pp.add_argument("--observe-server", type=int, action="append", default=[], help="servers only checked read-only")
    pp.add_argument("--db", default="", help="read-only SQLite path for ledger checks")
    pp.add_argument("--fixture-host", required=True, help="public IP of this host (traffic fixture)")
    pp.add_argument("--fixture-port", type=int, default=18481)
    pp.add_argument("--core", required=True)
    pp.add_argument("--scratch", default="/root/tmp-work/landing-acceptance-prod")
    pp.add_argument("--report", default=None)
    pp.add_argument("--only", default="")
    pp.add_argument("--custom-status", default="", help="optional: command printing Custom status JSON for the shared server")
    args = ap.parse_args()

    if args.mode == "cleanup":
        import lab
        cleanup_from_pids(pathlib.Path(args.scratch))
        lab.cleanup_namespace()
        print("lab cleaned up")
        return 0
    selected = {x.strip().upper() for x in args.only.split(",") if x.strip()}
    if args.mode == "local":
        import scenarios_local
        return scenarios_local.run(args, selected)
    import scenarios_prod
    return scenarios_prod.run(args, selected)


def cleanup_from_pids(scratch):
    p = scratch / "pids.json"
    if not p.exists():
        return
    for name, pid, argv0 in reversed(json.loads(p.read_text())):
        try:
            first = pathlib.Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0")[0].decode(errors="replace")
        except OSError:
            continue
        if first != argv0:  # pid reused by something else: never touch it
            continue
        try:
            os.killpg(pid, signal.SIGTERM)
        except (ProcessLookupError, PermissionError):
            pass


if __name__ == "__main__":
    sys.exit(main())
