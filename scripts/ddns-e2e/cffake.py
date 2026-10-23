"""In-memory fake of the Cloudflare v4 DNS API for the node DDNS E2E (plan 142).

Usage: python3 cffake.py <port> <token> <calls.jsonl>
Every request is appended to calls.jsonl ({"t","method","path"}). DELETE is
answered 405 and logged: FLVX must never delete records.
"""
import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

PORT, TOKEN, LOG = int(sys.argv[1]), sys.argv[2], sys.argv[3]
ZONE = {"id": "zone-e2e", "name": "imgamer.top", "status": "active", "permissions": ["#dns_records:edit", "#zone:read"]}
records, lock, seq = {}, threading.Lock(), [0]


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def reply(self, status, result=None, err=None):
        body = {"success": status < 300, "errors": [] if not err else [{"code": 7000 + status, "message": err}], "result": result}
        if isinstance(result, list):
            body["result_info"] = {"page": 1, "per_page": 100, "total_pages": 1, "count": len(result)}
        raw = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def handle_any(self):
        u = urlparse(self.path)
        if not self.headers.get("X-E2E"):  # the runner's own reads are not counted
            with lock, open(LOG, "a") as f:
                f.write(json.dumps({"t": time.time(), "method": self.command, "path": u.path}) + "\n")
        if self.headers.get("Authorization") != "Bearer " + TOKEN:
            return self.reply(403, err="Invalid API Token")
        length = int(self.headers.get("Content-Length") or 0)
        body = json.loads(self.rfile.read(length) or b"{}") if length else {}
        if self.command == "DELETE":
            return self.reply(405, err="delete not allowed in the E2E")
        if u.path == "/user/tokens/verify":
            return self.reply(200, {"id": "e2e", "status": "active"})
        if u.path == "/zones":
            return self.reply(200, [ZONE])
        prefix = "/zones/zone-e2e/dns_records"
        if not u.path.startswith(prefix):
            return self.reply(404, err="not found")
        rid = u.path[len(prefix):].strip("/")
        with lock:
            if self.command == "GET" and not rid:
                q = parse_qs(u.query)
                name, typ = q.get("name", [""])[0], q.get("type", [""])[0]
                return self.reply(200, [r for r in records.values() if r["name"] == name and (not typ or r["type"] == typ)])
            if self.command == "GET":
                return self.reply(200, records[rid]) if rid in records else self.reply(404, err="Record does not exist.")
            if self.command == "POST":
                seq[0] += 1
                rec = dict(body, id=f"rec{seq[0]}")
                records[rec["id"]] = rec
                return self.reply(200, rec)
            if self.command == "PUT":
                if rid not in records:
                    return self.reply(404, err="Record does not exist.")
                records[rid] = dict(body, id=rid)
                return self.reply(200, records[rid])
        return self.reply(405, err="method")

    do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = handle_any


class S(ThreadingHTTPServer):
    daemon_threads = True


if __name__ == "__main__":
    S(("127.0.0.1", PORT), H).serve_forever()
