#!/usr/bin/env bash
# send one JSON command (or array) to pwdrive.cjs
curl -s -m ${PW_TIMEOUT:-120} -X POST 127.0.0.1:16598/cmd -d "$1"; echo
