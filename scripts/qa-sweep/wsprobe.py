#!/usr/bin/env python3
"""Passive WebSocket listener for /system-info (plan 133 QA sweep). Read-only: it only
performs the GET upgrade and reads frames; it never sends application data.

  wsprobe.py URL SECONDS OUT_JSONL [--raw] [--token JWT]

For every message it records: time, outer broadcast type, node id, inner "type" (agent
message type), size, whether it carries a requestId, and the JSON key paths of the inner
payload (values redacted). --raw additionally stores the full message (LAB ONLY).
"""
import base64
import json
import os
import socket
import ssl
import struct
import sys
import time
import urllib.parse


def keypaths(v, prefix="", out=None, depth=0):
    out = out if out is not None else set()
    if depth > 5:
        return out
    if isinstance(v, dict):
        for k, x in v.items():
            p = f"{prefix}.{k}" if prefix else k
            out.add(p)
            keypaths(x, p, out, depth + 1)
    elif isinstance(v, list) and v:
        keypaths(v[0], prefix + "[]", out, depth + 1)
    return out


def connect(url, token=None):
    u = urllib.parse.urlparse(url)
    port = u.port or (443 if u.scheme == "wss" else 80)
    s = socket.create_connection((u.hostname, port), timeout=15)
    if u.scheme == "wss":
        s = ssl.create_default_context().wrap_socket(s, server_hostname=u.hostname)
    key = base64.b64encode(os.urandom(16)).decode()
    path = u.path + ("?" + u.query if u.query else "")
    hdr = (f"GET {path} HTTP/1.1\r\nHost: {u.hostname}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
           f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\nUser-Agent: flvx-qa-sweep/133\r\n")
    if token:
        hdr += f"Cookie: flvx_session={token}\r\n"
    s.sendall((hdr + "\r\n").encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = s.recv(4096)
        if not chunk:
            raise RuntimeError("closed during handshake")
        buf += chunk
    head, rest = buf.split(b"\r\n\r\n", 1)
    status = head.split(b"\r\n", 1)[0].decode()
    if " 101 " not in status:
        raise RuntimeError("handshake failed: " + status)
    return s, rest


def frames(s, buf):
    while True:
        while len(buf) < 2:
            c = s.recv(65536)
            if not c:
                return
            buf += c
        b1, b2 = buf[0], buf[1]
        op, ln, i = b1 & 0x0F, b2 & 0x7F, 2
        if ln == 126:
            while len(buf) < 4:
                buf += s.recv(65536)
            ln, i = struct.unpack(">H", buf[2:4])[0], 4
        elif ln == 127:
            while len(buf) < 10:
                buf += s.recv(65536)
            ln, i = struct.unpack(">Q", buf[2:10])[0], 10
        while len(buf) < i + ln:
            c = s.recv(65536)
            if not c:
                return
            buf += c
        payload, buf = buf[i:i + ln], buf[i + ln:]
        if op == 0x9:  # ping -> pong (masked, empty) keeps the server happy
            s.sendall(bytes([0x8A, 0x80]) + os.urandom(4))
            continue
        if op == 0x8:
            return
        if op in (0x1, 0x2):
            yield payload


def main():
    url, secs, out = sys.argv[1], float(sys.argv[2]), sys.argv[3]
    raw = "--raw" in sys.argv
    token = sys.argv[sys.argv.index("--token") + 1] if "--token" in sys.argv else None
    s, rest = connect(url, token)
    s.settimeout(secs)
    end = time.time() + secs
    n = 0
    with open(out, "a") as f:
        try:
            for p in frames(s, rest):
                n += 1
                rec = {"t": round(time.time(), 1), "size": len(p)}
                try:
                    m = json.loads(p)
                    rec["outer"] = m.get("type")
                    rec["node"] = m.get("id")
                    data = m.get("data")
                    inner = None
                    if isinstance(data, str) and data.startswith("{"):
                        try:
                            inner = json.loads(data)
                        except Exception:
                            inner = None
                    elif isinstance(data, dict):
                        inner = data
                    if isinstance(inner, dict):
                        rec["inner"] = inner.get("type")
                        rec["requestId"] = "requestId" in inner
                        rec["keys"] = sorted(keypaths(inner))[:80]
                    if raw:
                        rec["raw"] = p.decode(errors="replace")
                except Exception:
                    rec["nonjson"] = True
                f.write(json.dumps(rec, ensure_ascii=False) + "\n")
                if time.time() > end:
                    break
        except (socket.timeout, TimeoutError):
            pass
    print("messages", n)


if __name__ == "__main__":
    main()
