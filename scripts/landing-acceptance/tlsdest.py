#!/usr/bin/env python3
"""Concurrent TLS 1.3 web server used as a REALITY dest in labs.

  tlsdest.py <bind-ip> <port> <cert> <key>

REALITY fetches the dest's handshake for every connection, so the dest must
serve connections concurrently (openssl s_server is iterative).
"""
import http.server
import socketserver
import ssl
import sys


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body = b"ok"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


class Server(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True
    allow_reuse_address = True


def main():
    ip, port, cert, key = sys.argv[1], int(sys.argv[2]), sys.argv[3], sys.argv[4]
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_3
    ctx.load_cert_chain(cert, key)
    ctx.set_alpn_protocols(["h2", "http/1.1"])
    srv = Server((ip, port), Handler)
    srv.socket = ctx.wrap_socket(srv.socket, server_side=True, do_handshake_on_connect=False)
    srv.serve_forever()


if __name__ == "__main__":
    main()
