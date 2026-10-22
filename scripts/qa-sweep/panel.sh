#!/usr/bin/env bash
# start|stop the QA lab panel inside netns flvxqa (203.0.113.2:16481)
RUN=${QA_RUN:-/root/tmp-work/qa-sweep}; L=$RUN/lab
case "$1" in
start)
  cd $L && SERVER_ADDR=203.0.113.2:16481 DB_TYPE=sqlite DB_PATH=$L/gost.db JWT_SECRET=$(cat $L/jwt.secret) LOG_DIR=$L/logs FLUX_VERSION=3.0.27-qa GOMAXPROCS=2 \
    nohup ip netns exec flvxqa ${PANEL_BIN:-$RUN/bin/paneld} > $L/paneld.out 2>&1 &
  echo $! > $L/paneld.pid
  for i in $(seq 1 60); do curl -s -m 2 http://203.0.113.2:16481/flow/test >/dev/null && { echo healthy; exit 0; }; sleep 1; done; echo "not healthy"; tail -20 $L/paneld.out; exit 1;;
stop) pkill -f "$RUN/bin/paneld" ; sleep 1; echo stopped;;
esac
