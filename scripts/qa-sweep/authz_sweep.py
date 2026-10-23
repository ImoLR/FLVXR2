#!/usr/bin/env python3
"""Plan 133: enumerate every route registered in handler.Register and call it on the
LAB panel (neutralised prod copy) as: no token, a non-admin user, an expired/disabled
user, and admin. Combines the result with a static read of the handler body
(admin guard helpers / role checks / caller-id use) so every non-403 non-admin
answer can be reviewed by hand.

Usage: authz_sweep.py ROUTES_TXT OUT_JSON [--user UID] [--disabled UID]
Writes only to the lab panel. Never point it at prod.
"""
import json
import pathlib
import re
import sys

sys.path.insert(0, str(pathlib.Path(__file__).parent))
import qa  # noqa: E402

HANDLER_DIR = pathlib.Path(__file__).resolve().parents[2] / "go-backend/internal/http/handler"


def handler_bodies():
    bodies = {}
    for f in HANDLER_DIR.glob("*.go"):
        if f.name.endswith("_test.go"):
            continue
        src = f.read_text()
        for m in re.finditer(r"^func \(h \*Handler\) (\w+)\(", src, re.M):
            start = m.start()
            nxt = re.search(r"^func ", src[m.end():], re.M)
            body = src[start: m.end() + nxt.start()] if nxt else src[start:]
            bodies[m.group(1)] = (f.name, src[:start].count("\n") + 1, body)
    return bodies


def static_flags(name, bodies, depth=0, seen=None):
    seen = seen or set()
    if name in seen or name not in bodies:
        return set()
    seen.add(name)
    _, _, body = bodies[name]
    flags = set()
    if "ensureAdminAccess(" in body or "ensureServiceMonitorAdminAccess(" in body:
        flags.add("ensureAdmin")
    if "ensureSelfOrAdmin(" in body:
        flags.add("selfOrAdmin")
    if re.search(r"role[iI][dD]\s*[!=]=\s*0|RoleID\s*[!=]=\s*0|isAdmin", body):
        flags.add("roleCheck")
    if re.search(r"userIDFromRequest|userRoleFromRequest|claimsFromRequest|currentUserID", body):
        flags.add("callerId")
    if depth < 1:  # one level of h.xxx( delegation
        for callee in set(re.findall(r"h\.(\w+)\(w, r", body)):
            flags |= {f + "*" for f in static_flags(callee, bodies, depth + 1, seen)}
    return flags


def requires_admin_mw(path):
    for p in ("/api/v1/monitor/permission/", "/api/v1/group/", "/api/v1/federation/share/", "/api/v1/node/",
              "/api/v1/speed-limit/", "/api/v1/backup/", "/api/v1/api/v1/backup/"):
        if path.startswith(p):
            return True
    if path.startswith("/api/v1/tunnel/"):
        return not (path == "/api/v1/tunnel/user/latency" or path.startswith("/api/v1/tunnel/user/tunnel"))
    return path in ("/api/v1/user/create", "/api/v1/user/list", "/api/v1/user/update", "/api/v1/user/delete",
                    "/api/v1/user/reset", "/api/v1/user/update-order", "/api/v1/config/update",
                    "/api/v1/config/update-single", "/api/v1/announcement/update")


def main():
    routes_txt, out = sys.argv[1], sys.argv[2]
    uid = int(sys.argv[sys.argv.index("--user") + 1]) if "--user" in sys.argv else 26
    dis = int(sys.argv[sys.argv.index("--disabled") + 1]) if "--disabled" in sys.argv else 10
    bodies = handler_bodies()
    tok_user = qa.lab_token(uid, 1, "qa-user")
    tok_dis = qa.lab_token(dis, 1, "qa-disabled")
    rows = []
    skip_calls = ("/api/v1/user/logout",)
    for line in open(routes_txt):
        line = line.strip()
        if not line.startswith("/"):
            continue
        path, h = line.split()[:2]
        name = h.split(".", 1)[1]
        fname, lineno, _ = bodies.get(name, ("?", 0, ""))
        row = {"path": path, "handler": name, "file": f"{fname}:{lineno}", "mwAdmin": requires_admin_mw(path),
               "static": sorted(static_flags(name, bodies))}
        if path.startswith("/api/") and path not in skip_calls:
            for label, tok in (("anon", None), ("user", tok_user), ("disabled", tok_dis)):
                st, j, dt = qa.api(qa.LAB, path, tok, {}, "POST", timeout=20)
                if isinstance(j, dict):
                    row[label] = {"http": st, "code": j.get("code"), "msg": (j.get("msg") or "")[:60],
                                  "dataType": type(j.get("data")).__name__,
                                  "dataLen": len(json.dumps(j.get("data"), ensure_ascii=False)) if j.get("data") is not None else 0}
                else:
                    row[label] = {"http": st, "raw": str(j)[:80]}
                row[label]["ms"] = round(dt * 1000)
        rows.append(row)
        print(path, row.get("user", {}).get("code"), row.get("user", {}).get("msg", ""), row["static"], flush=True)
    json.dump(rows, open(out, "w"), ensure_ascii=False, indent=1)


if __name__ == "__main__":
    main()
