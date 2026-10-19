"""Client side of the acceptance runner: the pinned fork Core (xray) as a local
SOCKS client for FLVX SS2022 / VLESS Reality Vision links, plus SOCKS5,
HTTP-download and echo helpers that verify every byte."""
import hashlib
import http.client
import json
import os
import pathlib
import signal
import socket
import struct
import subprocess
import time
import urllib.parse

PATTERN = bytes(range(256)) * 256  # 64 KiB


def expected_digest(n):
    h = hashlib.sha256()
    left = n
    while left > 0:
        chunk = PATTERN[: min(left, len(PATTERN))]
        h.update(chunk)
        left -= len(chunk)
    return h.hexdigest()


def outbound_for(link, override=None, send_through=None):
    u = urllib.parse.urlsplit(link)
    host, port = (override or (u.hostname, u.port))
    if u.scheme == "ss":
        userinfo = urllib.parse.unquote(u.netloc.rsplit("@", 1)[0])
        method, password = userinfo.split(":", 1)
        out = {"protocol": "shadowsocks", "settings": {"servers": [{"address": host, "port": port, "method": method, "password": password}]}}
    elif u.scheme == "vless":
        q = urllib.parse.parse_qs(u.query, keep_blank_values=True)
        out = {
            "protocol": "vless",
            "settings": {"vnext": [{"address": host, "port": port, "users": [{"id": u.username, "encryption": "none", "flow": q.get("flow", [""])[0]}]}]},
            "streamSettings": {
                "network": "tcp",
                "security": "reality",
                "realitySettings": {
                    "serverName": q["sni"][0],
                    "fingerprint": q.get("fp", ["chrome"])[0],
                    "publicKey": q["pbk"][0],
                    "shortId": q.get("sid", [""])[0],
                },
            },
        }
    else:
        raise ValueError("unsupported link scheme " + u.scheme)
    if send_through:
        out["sendThrough"] = send_through
    out["tag"] = "proxy"
    return out


class CoreClient:
    def __init__(self, core, workdir, link, local_port, override=None, send_through=None):
        self.core = str(core)
        self.workdir = pathlib.Path(workdir)
        self.link = link
        self.port = local_port
        self.override = override
        self.send_through = send_through
        self.proc = None

    def start(self, timeout=10):
        self.workdir.mkdir(parents=True, exist_ok=True)
        cfg = {
            "log": {"loglevel": "warning", "access": "none"},
            "inbounds": [{"listen": "127.0.0.1", "port": self.port, "protocol": "socks", "settings": {"auth": "noauth", "udp": False}}],
            "outbounds": [outbound_for(self.link, self.override, self.send_through)],
        }
        path = self.workdir / f"client-{self.port}.json"
        path.write_text(json.dumps(cfg))
        path.chmod(0o600)
        log = open(self.workdir / f"client-{self.port}.log", "ab")
        self.proc = subprocess.Popen([self.core, "run", "-config", str(path)], stdout=log, stderr=subprocess.STDOUT, start_new_session=True)
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if self.proc.poll() is not None:
                raise RuntimeError(f"client core exited rc={self.proc.returncode}")
            try:
                socket.create_connection(("127.0.0.1", self.port), 0.3).close()
                return self
            except OSError:
                time.sleep(0.1)
        raise RuntimeError("client core did not listen")

    def stop(self):
        if self.proc and self.proc.poll() is None:
            try:
                os.killpg(self.proc.pid, signal.SIGTERM)
                self.proc.wait(timeout=5)
            except (ProcessLookupError, subprocess.TimeoutExpired):
                try:
                    os.killpg(self.proc.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
        self.proc = None

    def __enter__(self):
        return self.start()

    def __exit__(self, *exc):
        self.stop()


def _exact(s, n):
    buf = b""
    while len(buf) < n:
        part = s.recv(n - len(buf))
        if not part:
            raise EOFError("SOCKS closed")
        buf += part
    return buf


def socks_connect(local_port, host, port, timeout=10):
    s = socket.create_connection(("127.0.0.1", local_port), timeout)
    s.settimeout(timeout)
    try:
        s.sendall(b"\x05\x01\x00")
        if _exact(s, 2) != b"\x05\x00":
            raise ConnectionError("SOCKS auth refused")
        try:
            addr = b"\x01" + socket.inet_aton(host)
        except OSError:
            try:
                addr = b"\x04" + socket.inet_pton(socket.AF_INET6, host)
            except OSError:
                h = host.encode()
                addr = b"\x03" + bytes([len(h)]) + h
        s.sendall(b"\x05\x01\x00" + addr + struct.pack("!H", port))
        r = _exact(s, 4)
        if r[1] != 0:
            raise ConnectionError(f"SOCKS reply {r[1]}")
        _exact(s, 4 if r[3] == 1 else (16 if r[3] == 4 else _exact(s, 1)[0]))
        _exact(s, 2)
        return s
    except Exception:
        s.close()
        raise


def download(local_port, host, port, n, delay=0.0, timeout=60, progress=None):
    """GET /bytes/n through the SOCKS client; verifies length + SHA256."""
    started = time.monotonic()
    s = socks_connect(local_port, host, port, timeout)
    try:
        s.sendall(f"GET /bytes/{n}?delay={delay} HTTP/1.1\r\nHost: acceptance\r\nConnection: close\r\n\r\n".encode())
        resp = http.client.HTTPResponse(s)
        resp.begin()
        if resp.status != 200:
            raise ConnectionError(f"HTTP {resp.status}")
        h = hashlib.sha256()
        got = 0
        while True:
            chunk = resp.read(65536)
            if not chunk:
                break
            h.update(chunk)
            got += len(chunk)
            if progress is not None:
                progress["bytes"] = got
    finally:
        s.close()
    if got != n or h.hexdigest() != expected_digest(n):
        raise ConnectionError(f"payload mismatch: got {got} of {n}")
    return {"bytes": got, "seconds": round(time.monotonic() - started, 3)}


def try_download(local_port, host, port, n=4096, timeout=6):
    try:
        download(local_port, host, port, n, timeout=timeout)
        return True
    except Exception:
        return False


def echo_open(local_port, host, port, timeout=8):
    s = socks_connect(local_port, host, port, timeout)
    try:
        s.sendall(b"acceptance-echo")
        if _exact(s, 15) != b"acceptance-echo":
            raise ConnectionError("echo mismatch")
        return s
    except Exception:
        s.close()
        raise


def echo_alive(s, token=b"ping-again"):
    try:
        s.sendall(token)
        return _exact(s, len(token)) == token
    except (OSError, EOFError):
        return False


def try_echo(local_port, host, port, timeout=5):
    try:
        s = echo_open(local_port, host, port, timeout)
    except Exception:
        return False
    s.close()
    return True
