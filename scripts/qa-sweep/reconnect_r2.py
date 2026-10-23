#!/usr/bin/env python3
"""Plan 133 r2: does an agent WebSocket reconnect (=> panel redeploy of every service on
that node) cut user connections that are already running?  LAB ONLY.

A slow, byte-verified download (~24 s) runs through a share link via mihomo in netns
qacli; after ~6 s the agent of NODE gets its panel socket killed (ss -K), it reconnects,
the panel redeploys the node. The stream must still finish with every byte correct.
  reconnect_r2.py LINK NODE_ID [NODE_ID...]   (one run per node)
"""
import json
import os
import subprocess
import sys
import tempfile
import threading
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client_r2 import MIHOMO, NS  # noqa: E402

LINK = sys.argv[1]
NODES = [int(x) for x in sys.argv[2:]]
PORT = 17891
N = 3 * 1024 * 1024  # 48 chunks of 64 KiB, 0.5 s apart => ~24 s


def panel_lines(node):
    out = subprocess.run(["grep", "-c", f"redeploy: node {node} done", "/root/tmp-work/qa-r2/lab/paneld.out"], capture_output=True, text=True).stdout.strip()
    return int(out or 0)


def main():
    d = tempfile.mkdtemp(prefix="mihomo-rc-", dir="/root/tmp-work/qa-r2/tmp")
    open(os.path.join(d, "links.txt"), "w").write(LINK + "\n")
    open(os.path.join(d, "config.yaml"), "w").write(
        f"mixed-port: {PORT}\nallow-lan: false\nmode: rule\nlog-level: warning\ngeodata-mode: false\ngeo-auto-update: false\n"
        "profile: {store-selected: false}\nproxy-providers:\n  p:\n    type: file\n    path: ./links.txt\n    health-check: {enable: false}\n"
        "proxy-groups:\n  - {name: G, type: select, use: [p]}\nrules:\n  - MATCH,G\n")
    mp = subprocess.Popen(NS + [MIHOMO, "-d", d], stdout=open(os.path.join(d, "log"), "w"), stderr=subprocess.STDOUT)
    time.sleep(1.5)
    results = []
    try:
        for node in NODES:
            code = f"""
import sys,time,json
sys.path.insert(0,{os.path.dirname(os.path.abspath(__file__))!r})
from client_r2 import socks_connect, PATTERN
t0=time.time(); out={{}}
try:
    s=socks_connect({PORT},'203.0.113.200',8080,timeout=60)
    s.sendall(b'GET /bytes/{N}?delay=0.5 HTTP/1.1\\r\\nHost: t\\r\\nConnection: close\\r\\n\\r\\n')
    buf=b''
    while True:
        c=s.recv(65536)
        if not c: break
        buf+=c
    body=buf.split(b'\\r\\n\\r\\n',1)[1] if b'\\r\\n\\r\\n' in buf else b''
    exp=(PATTERN*({N}//len(PATTERN)+1))[:{N}]
    out['bytes']=len(body); out['ok']=body==exp
except Exception as e:
    out['err']=str(e)[:160]
out['s']=round(time.time()-t0,1)
print(json.dumps(out))
"""
            before = panel_lines(node)
            proc = subprocess.Popen(NS + [sys.executable, "-c", code], stdout=subprocess.PIPE, text=True)
            time.sleep(6)
            kill = subprocess.run(["ip", "netns", "exec", f"qan{node}", "ss", "-K", "dst", "203.0.113.2", "dport", "=", "16481"], capture_output=True, text=True)
            t_kill = time.time()
            out, _ = proc.communicate(timeout=120)
            time.sleep(1)
            r = json.loads(out.strip().splitlines()[-1])
            r.update({"node": node, "killed_sockets": max(0, len(kill.stdout.strip().splitlines()) - 1),
                      "redeploys_during": panel_lines(node) - before})
            results.append(r)
            print(json.dumps(r), flush=True)
            time.sleep(3)
    finally:
        mp.terminate()
        mp.wait(5)
        subprocess.run(["rm", "-rf", d])
    json.dump(results, open("/root/flvx-workers/runs/qa-sweep-r2/evidence/reconnect-cut-lab.json", "w"), indent=1)


if __name__ == "__main__":
    main()
