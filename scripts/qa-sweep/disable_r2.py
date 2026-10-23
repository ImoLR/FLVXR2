#!/usr/bin/env python3
"""Plan 133 r2: disable a user live (LAB) -> old token refused on REST + open WS closed; re-enable."""
import json, threading, time
import qa, wsprobe
adm = qa.lab_token(1, 0, "imolr")
UID = 13
tok = qa.lab_token(UID, 1, "rickzong")
st, j, _ = qa.api(qa.LAB, "user/list", adm, {})
u = [x for x in j["data"] if x["id"] == UID][0]
res = {"before": qa.api(qa.LAB, "user/package", tok, {})[1].get("code")}
s, rest = wsprobe.connect("ws://203.0.113.2:16481/system-info?type=0", tok)
ws = {"msgs": 0, "closed_at": None}
def rd():
    try:
        for _ in wsprobe.frames(s, rest):
            ws["msgs"] += 1
    except Exception as e:
        ws["err"] = str(e)[:60]
    ws["closed_at"] = time.time()
threading.Thread(target=rd, daemon=True).start()
time.sleep(3)
upd = dict(u); upd["status"] = 0
t0 = time.time()
res["disable"] = qa.api(qa.LAB, "user/update", adm, upd)[1]
res["rest_after_0s"] = qa.api(qa.LAB, "user/package", tok, {})[1].get("code")
for i in range(80):
    if ws["closed_at"]:
        break
    time.sleep(1)
res["ws_closed_after_s"] = round(ws["closed_at"] - t0, 1) if ws["closed_at"] else None
res["ws_msgs_before_close"] = ws["msgs"]
res["landing_links_after"] = qa.api(qa.LAB, "landing/node/list", tok, {})[1].get("code")
upd["status"] = 1
res["enable"] = qa.api(qa.LAB, "user/update", adm, upd)[1].get("code")
time.sleep(0.2)
res["rest_after_reenable"] = qa.api(qa.LAB, "user/package", tok, {})[1].get("code")
print(json.dumps(res, ensure_ascii=False))
json.dump(res, open("/root/flvx-workers/runs/qa-sweep-r2/evidence/disable-live-lab.json", "w"), ensure_ascii=False)
