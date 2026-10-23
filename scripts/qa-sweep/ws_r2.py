#!/usr/bin/env python3
"""Plan 133 r2: verify the fork.74 WebSocket fix on the LAB panel.

Opens several /system-info listeners at once (admin, two users with rules on
the lab-agent nodes, a user with nothing, anon type=2, wrong Origin, query-token)
for SECONDS, while triggering admin commands whose replies must NOT be broadcast
(forward diagnose -> TcpPing, forward client-ips). Writes a summary JSON.
Usage: ws_r2.py SECONDS OUT_JSON
"""
import json
import sys
import threading
import time

import qa
import wsprobe

LAB_WS = "ws://203.0.113.2:16481/system-info"


def listen(name, url, secs, res, token=None, origin=None):
    r = res[name] = {"url": url.replace(qa.LAB, ""), "msgs": 0, "bytes": 0, "outer": {}, "inner": {}, "nodes": set(),
                     "fwdUsers": set(), "fwdIds": set(), "keys": set(), "requestId": 0}
    try:
        s, rest = wsprobe.connect(url, token, origin)
    except Exception as e:
        r["handshake"] = str(e)[:120]
        return
    r["handshake"] = "101"
    s.settimeout(secs + 5)
    killer = threading.Timer(secs, lambda: (s.shutdown(2) if True else None))
    killer.daemon = True
    killer.start()
    end = time.time() + secs
    try:
        for p in wsprobe.frames(s, rest):
            r["msgs"] += 1
            r["bytes"] += len(p)
            try:
                m = json.loads(p)
            except Exception:
                continue
            r["outer"][m.get("type")] = r["outer"].get(m.get("type"), 0) + 1
            r["nodes"].add(m.get("id"))
            d = m.get("data")
            inner = json.loads(d) if isinstance(d, str) and d.startswith("{") else (d if isinstance(d, dict) else None)
            if isinstance(inner, dict):
                t = inner.get("type")
                r["inner"][t] = r["inner"].get(t, 0) + 1
                if "requestId" in inner:
                    r["requestId"] += 1
                r["keys"] |= set(inner.keys())
                for fm in inner.get("forward_metrics") or []:
                    r["fwdUsers"].add(fm.get("user_id"))
                    r["fwdIds"].add(fm.get("forward_id"))
            if time.time() > end:
                break
    except Exception:
        pass
    try:
        s.close()
    except Exception:
        pass


def main():
    secs, out = float(sys.argv[1]), sys.argv[2]
    adm = qa.lab_token(1, 0, "imolr")
    u7, u13, u26 = qa.lab_token(7, 1, "clot88"), qa.lab_token(13, 1, "rickzong"), qa.lab_token(26, 1, "leekao")
    res = {}
    specs = [
        ("admin_cookie", LAB_WS + "?type=0", adm, None),
        ("admin_good_origin", LAB_WS + "?type=0", adm, "http://203.0.113.2:16481"),
        ("admin_bad_origin", LAB_WS + "?type=0", adm, "https://evil.example"),
        ("admin_sibling_origin", LAB_WS + "?type=0", adm, "https://flvx.imgamer.top"),
        ("admin_query_secret", LAB_WS + "?type=0&secret=" + adm, None, None),
        ("user7", LAB_WS + "?type=0", u7, None),
        ("user13", LAB_WS + "?type=0", u13, None),
        ("user26_nothing", LAB_WS + "?type=0", u26, None),
        ("anon_public", LAB_WS + "?type=2", None, None),
        ("anon_type0", LAB_WS + "?type=0", None, None),
        ("forged_token", LAB_WS + "?type=0", qa.token("wrong-secret", 1, 0, "x"), None),
    ]
    ths = [threading.Thread(target=listen, args=(n, u, secs, res, t, o)) for n, u, t, o in specs]
    for t in ths:
        t.start()
    time.sleep(5)
    # admin commands whose replies must stay with the caller
    cmds = []
    for path, body in (("forward/diagnose", {"forwardId": FWD}), ("forward/client-ips", {"id": FWD}),
                       ("tunnel/diagnose", {"tunnelId": TUN})):
        st, j, dt = qa.api(qa.LAB, path, adm, body, timeout=60)
        cmds.append({"path": path, "http": st, "code": j.get("code") if isinstance(j, dict) else None,
                     "msg": (j.get("msg") if isinstance(j, dict) else str(j))[:80], "s": round(dt, 1)})
    for t in ths:
        t.join()
    for r in res.values():
        for k in ("nodes", "fwdUsers", "fwdIds", "keys"):
            r[k] = sorted(x for x in r[k] if x is not None)
        r["msgsPerMin"] = round(r["msgs"] / (secs / 60), 1)
    json.dump({"seconds": secs, "commands": cmds, "listeners": res}, open(out, "w"), ensure_ascii=False, indent=1)
    for n, r in res.items():
        print(f'{n:22s} hs={r["handshake"][:40]:40s} msgs={r["msgs"]:5d} nodes={r["nodes"]} fwdUsers={r["fwdUsers"]} inner={r["inner"]} reqId={r["requestId"]}')
    print(json.dumps(cmds, ensure_ascii=False))


FWD, TUN = int(sys.argv[3]), int(sys.argv[4])
if __name__ == "__main__":
    main()
