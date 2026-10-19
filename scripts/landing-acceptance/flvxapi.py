"""Small FLVX admin/user API client for the landing acceptance runner.

Tokens are minted in memory from the panel's JWT secret (same format the panel
issues), so the runner never needs or stores a password and never writes a
login record.
"""
import base64
import hashlib
import hmac
import json
import time
import urllib.error
import urllib.request


class APIError(RuntimeError):
    def __init__(self, path, envelope):
        super().__init__(f"{path}: code={envelope.get('code')} msg={envelope.get('msg')}")
        self.path = path
        self.envelope = envelope
        self.code = envelope.get("code")
        self.msg = envelope.get("msg") or ""


def _b64(raw):
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def mint_token(secret, uid, user, role, ttl=3600):
    header = _b64(json.dumps({"alg": "HmacSHA256", "typ": "JWT"}, separators=(",", ":")).encode())
    now = int(time.time())
    claims = {"sub": str(uid), "iat": now, "exp": now + ttl, "user": user, "name": user, "role_id": role}
    payload = _b64(json.dumps(claims, separators=(",", ":")).encode())
    sig = _b64(hmac.new(secret.encode(), f"{header}.{payload}".encode(), hashlib.sha256).digest())
    return f"{header}.{payload}.{sig}"


class Identity:
    def __init__(self, uid, user, role):
        self.uid, self.user, self.role = uid, user, role

    def __repr__(self):
        return f"Identity({self.uid},{self.user},{self.role})"


ADMIN = Identity(1, "admin_user", 0)


class Panel:
    def __init__(self, base, jwt_secret, timeout=90):
        self.base = base.rstrip("/")
        self.secret = jwt_secret
        self.timeout = timeout
        self._tokens = {}

    def token(self, who):
        key = (who.uid, who.user, who.role)
        cached = self._tokens.get(key)
        if cached and cached[1] > time.time() + 300:
            return cached[0]
        tok = mint_token(self.secret, who.uid, who.user, who.role)
        self._tokens[key] = (tok, time.time() + 3600)
        return tok

    def raw(self, path, data=None, who=ADMIN, timeout=None):
        body = json.dumps(data if data is not None else {}).encode()
        req = urllib.request.Request(
            self.base + "/api/v1/" + path.lstrip("/"),
            data=body,
            headers={"Authorization": self.token(who), "Content-Type": "application/json"},
        )
        with urllib.request.urlopen(req, timeout=timeout or self.timeout) as resp:
            return json.load(resp)

    def call(self, path, data=None, who=ADMIN, timeout=None):
        env = self.raw(path, data, who, timeout)
        if env.get("code") != 0:
            raise APIError(path, env)
        return env.get("data")

    def expect_error(self, path, data=None, who=ADMIN, contains=None):
        env = self.raw(path, data, who)
        if env.get("code") == 0:
            raise AssertionError(f"{path} unexpectedly succeeded: {env.get('data')!r}"[:400])
        if contains and contains not in (env.get("msg") or ""):
            raise AssertionError(f"{path} error {env.get('msg')!r} lacks {contains!r}")
        return env.get("msg") or ""

    def get(self, path, timeout=10):
        with urllib.request.urlopen(self.base + path, timeout=timeout) as resp:
            return resp.status, resp.read()

    # --- helpers -----------------------------------------------------------------
    def servers(self, who=ADMIN):
        return self.call("landing/server/list", {}, who)

    def server(self, server_id, who=ADMIN):
        for s in self.servers(who):
            if s["id"] == server_id:
                return s
        return None

    def nodes(self, who=ADMIN):
        return self.call("landing/node/list", {}, who)

    def node(self, node_id, who=ADMIN):
        for n in self.nodes(who):
            if n["id"] == node_id:
                return n
        return None

    def links(self, node_id, who=ADMIN):
        return self.call("landing/node/links", {"id": node_id}, who)

    def direct_link(self, node_id, who=ADMIN):
        return next(x["url"] for x in self.links(node_id, who) if x["kind"] == "direct")


SERVER_SAVE_KEYS = (
    "id nodeId name publicHost portMin portMax flowType trafficRatio maxConnections defaultMaxConnections "
    "defaultMaxOnlineIps defaultTrafficLimitBytes defaultUserQuotaBytes defaultExpiryMode defaultExpiryDays "
    "defaultExpiresAt maxNodesPerUser protocols realityDest realityServerNames availableUntil coreMemoryMb "
    "coreCpuPercent status userGroupIds"
).split()


def server_payload(view, **changes):
    out = {k: view[k] for k in SERVER_SAVE_KEYS if k in view}
    out.update(changes)
    return out


def wait_for(what, fn, timeout=60, interval=1.0):
    """Poll fn() until it returns a truthy value; return that value."""
    deadline = time.monotonic() + timeout
    last = None
    while True:
        try:
            last = fn()
        except Exception as e:  # keep polling through transient errors
            last = e
        if last and not isinstance(last, Exception):
            return last
        if time.monotonic() >= deadline:
            raise TimeoutError(f"timed out waiting for {what}; last={last!r}"[:600])
        time.sleep(interval)
