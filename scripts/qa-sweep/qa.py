"""Shared helpers for the plan 133 QA sweep (stdlib only).

  token(secret, uid, role, user)  -> HS256 JWT in the panel's own format (in memory only)
  lab_token(uid, role)            -> token for the neutralised lab panel
  prod_token(uid, role)           -> token for prod (JWT_SECRET read from /opt/flvx-svc/.env); use with GET only
  api(base, path, tok, body=None, method=None) -> (http_status, json|text, seconds)
"""
import base64
import hashlib
import hmac
import json
import time
import urllib.error
import urllib.request

LAB = "http://203.0.113.2:16481"
PROD = "https://flvxx.imgamer.top"
LAB_SECRET_FILE = "/root/tmp-work/qa-sweep/lab/jwt.secret"


def _enc(b):
    return base64.urlsafe_b64encode(b).rstrip(b"=").decode()


def token(secret, uid, role, user="qa", ttl=3600):
    h = _enc(json.dumps({"alg": "HmacSHA256", "typ": "JWT"}, separators=(",", ":")).encode())
    now = int(time.time())
    p = _enc(json.dumps({"sub": str(uid), "iat": now, "exp": now + ttl, "user": user, "name": user, "role_id": role}, separators=(",", ":")).encode())
    return f"{h}.{p}." + _enc(hmac.new(secret.encode(), f"{h}.{p}".encode(), hashlib.sha256).digest())


def lab_token(uid, role, user="qa"):
    return token(open(LAB_SECRET_FILE).read().strip(), uid, role, user)


def prod_token(uid, role, user="qa"):
    sec = [l.split("=", 1)[1].strip() for l in open("/opt/flvx-svc/.env") if l.startswith("JWT_SECRET=")][0]
    return token(sec, uid, role, user)


def api(base, path, tok=None, body=None, method=None, timeout=30):
    url = base + (path if path.startswith("/") else "/api/v1/" + path)
    data = None if body is None else json.dumps(body).encode()
    method = method or ("POST" if data is not None else "GET")
    if base.startswith(PROD) and method != "GET":
        raise RuntimeError("prod is read-only: refusing " + method + " " + path)
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    req.add_header("User-Agent", "flvx-qa-sweep/133")
    if tok:
        req.add_header("Authorization", tok)
    t0 = time.monotonic()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            raw, status = r.read(), r.status
    except urllib.error.HTTPError as e:
        raw, status = e.read(), e.code
    dt = time.monotonic() - t0
    try:
        return status, json.loads(raw), dt
    except Exception:
        return status, raw.decode(errors="replace")[:500], dt
