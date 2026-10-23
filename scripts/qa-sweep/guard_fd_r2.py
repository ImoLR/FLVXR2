#!/usr/bin/env python3
"""Plan 133 r2: per-connection socket cost of a guarded VLESS Reality landing node.
Holds N authenticated Reality streams through mihomo (netns qacli) and samples the
landing Core's fd count and its TCP connections to the Reality dest over time.
  guard_fd_r2.py LINK CORE_NS DEST_IP:PORT [N]"""
import json, os, subprocess, sys, tempfile, time
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from client_r2 import MIHOMO, NS
link, ns, dest = sys.argv[1], sys.argv[2], sys.argv[3]
N = int(sys.argv[4]) if len(sys.argv) > 4 else 20
PORT = 17892
def core_pid():
    for pid in subprocess.run(["ip", "netns", "pids", ns], capture_output=True, text=True).stdout.split():
        try:
            if os.readlink(f"/proc/{pid}/exe").endswith("flvx-landing-core"):
                return int(pid)
        except OSError:
            pass
def sample(tag):
    pid = core_pid()
    fds = len(os.listdir(f"/proc/{pid}/fd"))
    est = subprocess.run(["ip", "netns", "exec", ns, "ss", "-Htn", "state", "established"], capture_output=True, text=True).stdout.splitlines()
    to_dest = sum(1 for l in est if l.split()[-1] == dest)
    loop = sum(1 for l in est if "127.0.0.1:" in l)
    other = subprocess.run(["ip", "netns", "exec", ns, "ss", "-Htan"], capture_output=True, text=True).stdout.splitlines()
    tw = sum(1 for l in other if l.startswith("TIME-WAIT"))
    return {"t": tag, "core_fds": fds, "estab_to_dest": to_dest, "estab_loopback": loop, "time_wait": tw}
d = tempfile.mkdtemp(prefix="mihomo-g-", dir="/root/tmp-work/qa-r2/tmp")
open(os.path.join(d, "links.txt"), "w").write(link + "\n")
open(os.path.join(d, "config.yaml"), "w").write(f"mixed-port: {PORT}\nallow-lan: false\nmode: rule\nlog-level: warning\ngeodata-mode: false\ngeo-auto-update: false\nprofile: {{store-selected: false}}\nproxy-providers:\n  p:\n    type: file\n    path: ./links.txt\n    health-check: {{enable: false}}\nproxy-groups:\n  - {{name: G, type: select, use: [p]}}\nrules:\n  - MATCH,G\n")
mp = subprocess.Popen(NS + [MIHOMO, "-d", d], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
time.sleep(1.5)
res = [sample("idle")]
holder = subprocess.Popen(NS + [sys.executable, "-c", f"""
import sys,time,socket
sys.path.insert(0,{os.path.dirname(os.path.abspath(__file__))!r})
from client_r2 import socks_connect
ss=[]
for i in range({N}):
    s=socks_connect({PORT},'203.0.113.200',8080); s.sendall(b'GET /bytes/16?delay=200 HTTP/1.1\\r\\nHost: t\\r\\n\\r\\n'); ss.append(s)
print('held',len(ss),flush=True)
time.sleep(75)
"""], stdout=subprocess.PIPE, text=True)
print(holder.stdout.readline().strip())
t0 = time.time()
for wait in (3, 9, 30, 60):
    time.sleep(max(0, wait - (time.time() - t0)))
    res.append(sample(f"held {N} +{wait}s"))
holder.wait(30)
time.sleep(2)
res.append(sample("released +2s"))
mp.terminate(); mp.wait(5)
subprocess.run(["rm", "-rf", d])
for r in res: print(json.dumps(r))
json.dump(res, open("/root/flvx-workers/runs/qa-sweep-r2/evidence/" + os.environ.get("OUTNAME","reality-guard-fd.json") + "", "w"), indent=1)
