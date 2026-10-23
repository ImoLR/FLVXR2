#!/usr/bin/env python3
"""Plan 133 r2: snapshot billing counters of a lab user (read-only on the lab DB)."""
import json, sqlite3, sys
uid = int(sys.argv[1]); fid = int(sys.argv[2]) if len(sys.argv) > 2 else 0
c = sqlite3.connect("file:/root/tmp-work/qa-r2/lab/gost.db?mode=ro", uri=True)
c.row_factory = sqlite3.Row
out = {"user": dict(c.execute("select in_flow,out_flow,flow from user where id=?", (uid,)).fetchone())}
if fid:
    out["forward"] = dict(c.execute("select in_flow,out_flow from forward where id=?", (fid,)).fetchone())
cols = [r[1] for r in c.execute("pragma table_info(landing_node)")]
pick = [x for x in ("id", "used_bytes", "upload_bytes", "download_bytes", "raw_up", "raw_down", "traffic_used", "used_up", "used_down") if x in cols]
out["landing_cols"] = [x for x in cols if "byte" in x or "flow" in x or "used" in x or "traffic" in x]
out["landing_nodes"] = [dict(r) for r in c.execute(f"select id,{','.join(out['landing_cols']) or 'id'} from landing_node where user_id=?", (uid,))]
out["server_quota"] = [dict(r) for r in c.execute("select * from user_server_quota where user_id=?", (uid,))]
print(json.dumps(out, ensure_ascii=False))
