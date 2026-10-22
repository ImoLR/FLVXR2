#!/usr/bin/env bash
# Plan 133 QA lab network: namespace "flvxqa" for the panel that runs on a neutralised
# copy of the prod DB. Nothing in the namespace can reach the internet: every
# non-lab packet is counted into a dynamic set and dropped, and DNS goes to a
# logging sink (dnslog.py) that answers REFUSED.
#   labns.sh up | down | proof
set -euo pipefail
NS=flvxqa; H=qa0; N=qa1; HIP=203.0.113.1; NIP=203.0.113.2
HERE=$(cd "$(dirname "$0")" && pwd)
RUN=${QA_RUN:-/root/tmp-work/qa-sweep}
case "${1:-}" in
up)
  ip netns add $NS
  ip link add $H type veth peer name $N
  ip link set $N netns $NS
  ip addr add $HIP/24 dev $H; ip link set $H up
  ip -n $NS addr add $NIP/24 dev $N; ip -n $NS link set $N up; ip -n $NS link set lo up
  ip -n $NS route add default via $HIP
  mkdir -p /etc/netns/$NS
  echo "nameserver $HIP" > /etc/netns/$NS/resolv.conf
  printf '127.0.0.1 localhost\n' > /etc/netns/$NS/hosts
  ip netns exec $NS nft -f - <<EOF
table inet qa {
  set seen4 { type ipv4_addr . inet_service; flags dynamic; size 4096; }
  set seen6 { type ipv6_addr . inet_service; flags dynamic; size 4096; }
  chain out {
    type filter hook output priority 0; policy accept;
    oif "lo" accept
    ip daddr 203.0.113.0/24 accept
    meta l4proto { tcp, udp } meta nfproto ipv4 add @seen4 { ip daddr . th dport } counter drop
    meta l4proto { tcp, udp } meta nfproto ipv6 add @seen6 { ip6 daddr . th dport } counter drop
    ip6 daddr fe80::/10 accept
    counter drop
  }
}
EOF
  nohup python3 "$HERE/dnslog.py" $HIP "$RUN/dns-queries.log" > "$RUN/dnslog.out" 2>&1 &
  echo $! > "$RUN/dnslog.pid"
  echo up;;
proof)
  echo "== routes in $NS"; ip -n $NS route
  echo "== dropped non-lab destinations (dst . port)"; ip netns exec $NS nft list set inet qa seen4 | sed -n '/elements/,/}/p'
  ip netns exec $NS nft list set inet qa seen6 | sed -n '/elements/,/}/p'
  echo "== counters"; ip netns exec $NS nft list chain inet qa out | grep counter
  echo "== DNS names asked"; awk '{print $3}' "$RUN/dns-queries.log" 2>/dev/null | sort | uniq -c | sort -rn | head -40;;
down)
  [ -f "$RUN/dnslog.pid" ] && kill "$(cat "$RUN/dnslog.pid")" 2>/dev/null || true
  ip netns pids $NS 2>/dev/null | xargs -r kill 2>/dev/null || true
  sleep 1
  ip netns del $NS 2>/dev/null || true
  ip link del $H 2>/dev/null || true
  rm -rf /etc/netns/$NS
  echo down;;
*) echo "usage: $0 up|down|proof"; exit 2;;
esac
