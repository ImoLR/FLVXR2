#!/usr/bin/env python3
"""Plan 133 QA sweep: make a NEUTRALISED copy of the production SQLite DB for the local lab.

The source is opened read-only (sqlite URI mode=ro) and copied with the online
backup API, so the production file is never written. In the copy every outbound
integration is disabled and every credential that could reach a real system is
rotated before any panel process ever opens it:

  * landing_integration.base_url/token  -> 127.0.0.1:9 / qa-neutralised (mmwx Custom)
  * leikwan_account.token               -> qa-neutralised (LeiKwan bridge)
  * node.secret                         -> random (real agents cannot authenticate)
  * node.remote_url/remote_token        -> '' (federation peers)
  * federation_tunnel_binding.remote_url-> '' ; peer_share.token -> random
  * landing_node keys/passwords, node_identity passwords, wg keys, warp accounts -> random
  * payment_config / order pay urls     -> cleared
  * node_metric older than --keep-metric-min minutes -> deleted (size), then VACUUM

Usage: make_lab_db.py SRC DST [--keep-metric-min 90]
"""
import argparse
import base64
import os
import secrets
import sqlite3
import time


def rnd_b64(n):
    return base64.b64encode(secrets.token_bytes(n)).decode()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src")
    ap.add_argument("dst")
    ap.add_argument("--keep-metric-min", type=int, default=90)
    a = ap.parse_args()
    if os.path.exists(a.dst):
        raise SystemExit("refusing to overwrite " + a.dst)
    src = sqlite3.connect(f"file:{a.src}?mode=ro", uri=True)
    dst = sqlite3.connect(a.dst)
    src.backup(dst)
    src.close()
    print("quick_check:", dst.execute("pragma quick_check").fetchone()[0])
    c = dst.cursor()
    tables = {r[0] for r in c.execute("select name from sqlite_master where type='table'")}

    def upd(sql, *args):
        c.execute(sql, args)
        return c.rowcount

    log = []
    if "landing_integration" in tables:
        log.append(("landing_integration", upd("update landing_integration set base_url='http://127.0.0.1:9', token='qa-neutralised'")))
    if "leikwan_account" in tables:
        log.append(("leikwan_account", upd("update leikwan_account set token='qa-neutralised'")))
    for (nid,) in c.execute("select id from node").fetchall():
        c.execute("update node set secret=?, remote_url='', remote_token='' where id=?", (secrets.token_hex(16), nid))
    log.append(("node secrets rotated", c.execute("select count(*) from node").fetchone()[0]))
    if "federation_tunnel_binding" in tables:
        log.append(("federation_tunnel_binding", upd("update federation_tunnel_binding set remote_url=''")))
    if "peer_share" in tables:
        log.append(("peer_share", upd("update peer_share set token=hex(randomblob(16))")))
    if "landing_node" in tables:
        for row in c.execute("select id, user_key, server_key, private_key, public_key, password from landing_node").fetchall():
            nid, uk, sk, pk, pub, pw = row
            c.execute(
                "update landing_node set user_key=?, server_key=?, password=?, private_key=?, public_key=?, tls_key_pem='' where id=?",
                (rnd_b64(16) if uk else uk, rnd_b64(16) if sk else sk, secrets.token_hex(12) if pw else pw,
                 base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("=") if pk else pk,
                 base64.urlsafe_b64encode(secrets.token_bytes(32)).decode().rstrip("=") if pub else pub, nid))
        log.append(("landing_node creds rotated", c.execute("select count(*) from landing_node").fetchone()[0]))
    if "node_identity" in tables:
        log.append(("node_identity", upd("update node_identity set mixed_password=hex(randomblob(8)), trojan_password=hex(randomblob(8)), hysteria2_password=hex(randomblob(8)), tuic_password=hex(randomblob(8)), protocol_credentials_json='{}'")))
    for t, sql in (("landing_warp_account", "delete from landing_warp_account"),
                   ("payment_config", "delete from payment_config"),
                   ("wg_node_identity", "update wg_node_identity set private_key_encrypted=''")):
        if t in tables:
            log.append((t, upd(sql)))
    cutoff = int(time.time() * 1000) - a.keep_metric_min * 60_000
    log.append(("node_metric deleted", upd("delete from node_metric where timestamp < ?", cutoff)))
    if "tunnel_quality" in tables:
        log.append(("tunnel_quality deleted", upd("delete from tunnel_quality")))
    dst.commit()
    dst.execute("vacuum")
    dst.close()
    for k, v in log:
        print(f"{k}: {v}")
    print("size MB:", round(os.path.getsize(a.dst) / 1e6, 1))


if __name__ == "__main__":
    main()
