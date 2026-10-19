#!/usr/bin/env python3
"""Traffic fixtures for the acceptance runner (run as a separate process).

  fixtures.py serve <bind-ip> <http-port> <echo-port>

GET /bytes/<n>[?delay=<s>] streams n bytes of the runner's fixed pattern
(optionally slowly, 64 KiB per delay), so the client can verify every byte.
The echo port reflects whatever it receives. No logging, no state on disk.
"""
import http.server
import socket
import socketserver
import sys
import threading
import time
import urllib.parse

PATTERN = bytes(range(256)) * 256


class Bytes(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        u = urllib.parse.urlsplit(self.path)
        if not u.path.startswith("/bytes/"):
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        n = int(u.path.split("/")[-1])
        delay = float(urllib.parse.parse_qs(u.query).get("delay", ["0"])[0] or 0)
        self.send_response(200)
        self.send_header("Content-Length", str(n))
        self.send_header("Connection", "close")
        self.end_headers()
        left = n
        try:
            while left > 0:
                chunk = PATTERN[: min(left, len(PATTERN))]
                self.wfile.write(chunk)
                left -= len(chunk)
                if delay:
                    time.sleep(delay)
        except (BrokenPipeError, ConnectionResetError):
            pass
        self.close_connection = True

    def log_message(self, *args):
        pass


class Echo(socketserver.BaseRequestHandler):
    def handle(self):
        while True:
            try:
                data = self.request.recv(65536)
            except OSError:
                return
            if not data:
                return
            self.request.sendall(data)


class TCP(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


class HTTP(http.server.ThreadingHTTPServer):
    allow_reuse_address = True
    daemon_threads = True


class TCP6(TCP):
    address_family = socket.AF_INET6


class HTTP6(HTTP):
    address_family = socket.AF_INET6


def serve(bind, http_port, echo_port):
    tcp, web = (TCP6, HTTP6) if ":" in bind else (TCP, HTTP)
    echo = tcp((bind, echo_port), Echo)
    threading.Thread(target=echo.serve_forever, daemon=True).start()
    web((bind, http_port), Bytes).serve_forever()


if __name__ == "__main__":
    if len(sys.argv) != 5 or sys.argv[1] != "serve":
        raise SystemExit(__doc__)
    serve(sys.argv[2], int(sys.argv[3]), int(sys.argv[4]))
