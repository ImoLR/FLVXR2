#!/usr/bin/env python3
"""Plan 133 r2: use a REAL client (mihomo) to parse a share link exactly as given to
the user and move verified bytes through it (LAB ONLY; runs mihomo inside netns qacli).

  client_r2.py LINK [--down BYTES] [--up BYTES] [--target 203.0.113.200] [--keep]
  client_r2.py --clash-yaml FILE ...   (a Clash proxy snippet instead of a link)

Prints JSON: parse ok?, mihomo-reported proxy type, bytes down/up verified, seconds.
Download = GET http://TARGET:8080/bytes/N (fixed pattern, every byte checked).
Upload   = N random bytes to the echo port TARGET:9000, echo compared byte for byte.
"""
import argparse
import hashlib
import json
import os
import socket
import struct
import subprocess
import sys
import tempfile
import time

MIHOMO = "/root/mmwx-workers/tools/node-check/bin/mihomo"
PATTERN = bytes(range(256)) * 256
NS = ["ip", "netns", "exec", "qacli"]


def socks_connect(port, host, dport, timeout=20):
    s = socket.create_connection(("127.0.0.1", port), timeout=timeout)
    s.sendall(b"\x05\x01\x00")
    if s.recv(2) != b"\x05\x00":
        raise RuntimeError("socks auth")
    hb = host.encode()
    s.sendall(b"\x05\x01\x00\x03" + bytes([len(hb)]) + hb + struct.pack(">H", dport))
    r = s.recv(10)
    if len(r) < 2 or r[1] != 0:
        raise RuntimeError("socks connect failed %r" % r)
    return s


def run_in_ns(code):
    return subprocess.run(NS + [sys.executable, "-c", code], capture_output=True, text=True, timeout=180)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("link", nargs="?")
    ap.add_argument("--clash-yaml")
    ap.add_argument("--down", type=int, default=2 * 1024 * 1024)
    ap.add_argument("--up", type=int, default=1024 * 1024)
    ap.add_argument("--target", default="203.0.113.200")
    ap.add_argument("--port", type=int, default=17890)
    ap.add_argument("--keep", action="store_true")
    a = ap.parse_args()
    d = tempfile.mkdtemp(prefix="mihomo-", dir="/root/tmp-work/qa-r2/tmp")
    if a.clash_yaml:
        proxies = open(a.clash_yaml).read()
        cfg = f"mixed-port: {a.port}\nallow-lan: false\nmode: global\nlog-level: warning\ngeodata-mode: false\ngeo-auto-update: false\nprofile: {{store-selected: false}}\n{proxies}\nproxy-groups:\n  - {{name: G, type: select, proxies: [{proxies.split('name:')[1].split(',')[0].split(chr(10))[0].strip().strip(chr(39)).strip(chr(34))}]}}\nrules:\n  - MATCH,G\n"
    else:
        open(os.path.join(d, "links.txt"), "w").write(a.link + "\n")
        cfg = (f"mixed-port: {a.port}\nallow-lan: false\nmode: rule\nlog-level: warning\ngeodata-mode: false\ngeo-auto-update: false\n"
               "profile: {store-selected: false}\nproxy-providers:\n  p:\n    type: file\n    path: ./links.txt\n    health-check: {enable: false}\n"
               "proxy-groups:\n  - {name: G, type: select, use: [p]}\nrules:\n  - MATCH,G\n")
    open(os.path.join(d, "config.yaml"), "w").write(cfg)
    res = {"link": (a.link or a.clash_yaml)[:120]}
    t = subprocess.run([MIHOMO, "-t", "-d", d], capture_output=True, text=True, timeout=30)
    res["mihomo_t"] = "ok" if t.returncode == 0 else (t.stdout + t.stderr)[-300:]
    log = open(os.path.join(d, "mihomo.log"), "w")
    p = subprocess.Popen(NS + [MIHOMO, "-d", d], stdout=log, stderr=subprocess.STDOUT)
    try:
        time.sleep(1.5)
        # what did mihomo parse?  (external controller is off; read the provider via the log-less way: parse ok if proxy count > 0)
        code = f"""
import socket,struct,os,time,hashlib,json,sys
sys.path.insert(0,{os.path.dirname(os.path.abspath(__file__))!r})
from client_r2 import socks_connect, PATTERN
out={{}}
t0=time.time()
try:
    s=socks_connect({a.port},{a.target!r},8080)
    s.sendall(b'GET /bytes/{a.down} HTTP/1.1\\r\\nHost: t\\r\\nConnection: close\\r\\n\\r\\n')
    buf=b''
    while True:
        c=s.recv(65536)
        if not c: break
        buf+=c
    body=buf.split(b'\\r\\n\\r\\n',1)[1] if b'\\r\\n\\r\\n' in buf else b''
    exp=(PATTERN*({a.down}//len(PATTERN)+1))[:{a.down}]
    out['down_bytes']=len(body); out['down_ok']=body==exp
except Exception as e:
    out['down_err']=str(e)[:160]
out['down_s']=round(time.time()-t0,2)
t0=time.time()
try:
    data=os.urandom({a.up})
    s=socks_connect({a.port},{a.target!r},9000)
    s.sendall(data); got=b''
    s.settimeout(20)
    while len(got)<len(data):
        c=s.recv(65536)
        if not c: break
        got+=c
    out['up_bytes']=len(data); out['echo_ok']=got==data
except Exception as e:
    out['up_err']=str(e)[:160]
out['up_s']=round(time.time()-t0,2)
print(json.dumps(out))
"""
        r = run_in_ns(code)
        try:
            res.update(json.loads(r.stdout.strip().splitlines()[-1]))
        except Exception:
            res["client_err"] = (r.stdout + r.stderr)[-400:]
    finally:
        p.terminate()
        p.wait(5)
        log.close()
        res["mihomo_log_tail"] = open(os.path.join(d, "mihomo.log")).read()[-300:]
        if not a.keep:
            subprocess.run(["rm", "-rf", d])
    print(json.dumps(res, ensure_ascii=False))


if __name__ == "__main__":
    main()
