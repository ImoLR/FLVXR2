#!/usr/bin/env python3
"""Plan 133 r2: login hardening on the LAB panel (rate limits, known-good address,
MD5->bcrypt upgrade, disabled user, username timing). Source addresses are bound
per request (lab host has 203.0.113.1/.101/.102)."""
import http.client
import json
import socket
import sqlite3
import statistics
import time

DB = "/root/tmp-work/qa-r2/lab/gost.db"


def login(user, pw, src="203.0.113.1"):
    c = http.client.HTTPConnection("203.0.113.2", 16481, timeout=10, source_address=(src, 0))
    t0 = time.monotonic()
    c.request("POST", "/api/v1/user/login", json.dumps({"username": user, "password": pw}), {"Content-Type": "application/json"})
    r = c.getresponse()
    body = json.loads(r.read())
    dt = time.monotonic() - t0
    c.close()
    return body.get("code"), body.get("msg"), round(dt * 1000, 1)


def pwd(uid):
    c = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    return c.execute("select pwd from user where id=?", (uid,)).fetchone()[0]


out = {}
# 1. MD5 -> bcrypt upgrade on first good login (user 4 'test')
out["before_hash_prefix"] = pwd(4)[:4]
out["login1"] = login("test", "qa-pass-4", "203.0.113.101")
out["after_hash_prefix"] = pwd(4)[:4]
out["login2_after_upgrade"] = login("test", "qa-pass-4", "203.0.113.101")
out["wrong_after_upgrade"] = login("test", "nope", "203.0.113.101")
# 2. disabled user (10) with right password
out["disabled_right_pw"] = login("@Cap_Grey", "qa-pass-10", "203.0.113.1")
out["disabled_wrong_pw"] = login("@Cap_Grey", "wrong", "203.0.113.1")
# 3. per-account budget: hammer 'test' from .102 (attacker)
att = [login("test", f"guess{i}", "203.0.113.102") for i in range(13)]
out["attacker_codes"] = [a[0] for a in att]
# owner from known-good .101 still gets in; unknown .1 is refused even with right pw
out["owner_known_good_addr"] = login("test", "qa-pass-4", "203.0.113.101")
out["owner_new_addr_right_pw"] = login("test", "qa-pass-4", "203.0.113.1")
# 4. per-IP budget: .102 tries other accounts
ipb = [login(f"nouser{i}", "x", "203.0.113.102") for i in range(12)]
out["per_ip_codes_after_13"] = [a[0] for a in ipb]
# 5. username timing (existing bcrypt user vs missing user) from .1
t_exist = [login("alex", f"bad{i}", "203.0.113.1")[2] for i in range(5)]
t_missing = [login(f"ghost{i}", "bad", "203.0.113.1")[2] for i in range(5)]
out["timing_ms_existing_user_wrong_pw"] = t_exist
out["timing_ms_missing_user"] = t_missing
out["timing_median"] = [statistics.median(t_exist), statistics.median(t_missing)]
print(json.dumps(out, ensure_ascii=False, indent=1))
json.dump(out, open("/root/flvx-workers/runs/qa-sweep-r2/evidence/login-lab.json", "w"), ensure_ascii=False, indent=1)
