#!/usr/bin/env python3
"""Plan 133 r2: feed every generated AnyTLS / Snell / Mieru share link and Clash
proxy entry to the real mihomo client and record what it accepts.

  parse_links_r2.py LINKS_JSON OUT_JSON
LINKS_JSON = [{Proto, Name, Host, URL, Clash, Surge}, ...] (from the FLVX generators).
Clash entries: `mihomo -t` on a config whose only proxy is that entry.
URLs: mihomo started with a file provider holding the URL; the controller API
tells whether a proxy of the expected type came out (runs in netns qacli, no egress).
"""
import json
import os
import subprocess
import sys
import tempfile
import time
import urllib.request

MIHOMO = "/root/mmwx-workers/tools/node-check/bin/mihomo"
BASE = "mixed-port: 0\nallow-lan: false\nlog-level: warning\ngeodata-mode: false\ngeo-auto-update: false\nprofile: {store-selected: false}\n"


def test_clash(entry):
    d = tempfile.mkdtemp(dir="/root/tmp-work/qa-r2/tmp")
    cfg = BASE + "proxies:\n" + "\n".join("  " + l for l in entry.splitlines()) + "\nrules:\n  - MATCH,DIRECT\n"
    open(os.path.join(d, "config.yaml"), "w").write(cfg)
    r = subprocess.run([MIHOMO, "-t", "-d", d], capture_output=True, text=True, timeout=30)
    subprocess.run(["rm", "-rf", d])
    return "ok" if r.returncode == 0 else (r.stdout + r.stderr).strip().splitlines()[-1][-200:]


def test_url(url, port=19190):
    d = tempfile.mkdtemp(dir="/root/tmp-work/qa-r2/tmp")
    open(os.path.join(d, "links.txt"), "w").write(url + "\n")
    cfg = BASE + f"external-controller: 127.0.0.1:{port}\nproxy-providers:\n  p:\n    type: file\n    path: ./links.txt\n    health-check: {{enable: false}}\nproxy-groups:\n  - {{name: G, type: select, use: [p]}}\nrules:\n  - MATCH,G\n"
    open(os.path.join(d, "config.yaml"), "w").write(cfg)
    p = subprocess.Popen(["ip", "netns", "exec", "qacli", MIHOMO, "-d", d], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    out = None
    try:
        for _ in range(20):
            time.sleep(0.3)
            r = subprocess.run(["ip", "netns", "exec", "qacli", "curl", "-s", "-m", "2", f"http://127.0.0.1:{port}/providers/proxies/p"], capture_output=True, text=True)
            if r.stdout.startswith("{"):
                j = json.loads(r.stdout)
                out = [{"name": x.get("name"), "type": x.get("type"), "udp": x.get("udp")} for x in j.get("proxies", [])]
                break
    finally:
        p.terminate()
        try:
            log = p.communicate(timeout=5)[0]
        except Exception:
            log = ""
        subprocess.run(["rm", "-rf", d])
    if out is None:
        return {"error": (log or "")[-300:]}
    return {"parsed": out, "log": [l[-160:] for l in (log or "").splitlines() if "level=error" in l or "level=warn" in l][:3]}


def main():
    items = json.load(open(sys.argv[1]))
    res = []
    for it in items:
        r = {"proto": it["Proto"], "name": it["Name"], "host": it["Host"], "clash": test_clash(it["Clash"])}
        if it["URL"]:
            r["url"] = it["URL"]
            r["url_parse"] = test_url(it["URL"])
        if it["Surge"]:
            r["surge"] = it["Surge"]
        res.append(r)
        print(json.dumps(r, ensure_ascii=False)[:400], flush=True)
    json.dump(res, open(sys.argv[2], "w"), ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
