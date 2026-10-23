#!/usr/bin/env bash
# Plan 133 r2: run REAL lab agents for nodes of the neutralised prod copy.
# Each agent gets its own netns (qan<ID>, 203.0.113.<LAST>/32) wired by a veth
# pair DIRECTLY into the panel netns flvxqa (no host bridge, so the host's
# docker FORWARD policy is never involved). flvxqa routes between agents.
# Every agent netns drops all non-lab traffic (same nft table as labns.sh) and
# resolves via the DNS sink, so agents reach only the lab panel and each other.
#   agentns.sh add NODE_ID IP_LAST     start agent for node (secret read from lab DB)
#   agentns.sh down                    stop all agents, delete their netns
set -euo pipefail
RUN=${QA_RUN:-/root/tmp-work/qa-r2}
AGENT=${AGENT_BIN:-$RUN/bin/flvx-agent}
CORE=${CORE_BIN:-$RUN/bin/mmwxc-core-linux-amd64}
DB=$RUN/lab/gost.db
nft_drop() {
  ip netns exec "$1" nft -f - <<EOF
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
}
case "${1:-}" in
add)
  ID=$2; LAST=$3; NS=qan$ID
  SECRET=$(python3 -c "import sqlite3,sys;c=sqlite3.connect('file:$DB?mode=ro',uri=True);print(c.execute('select secret from node where id=?',($ID,)).fetchone()[0])")
  ip netns add $NS
  ip link add qh$ID type veth peer name qn$ID
  ip link set qn$ID netns $NS
  ip link set qh$ID netns flvxqa
  ip netns exec flvxqa sysctl -qw net.ipv4.ip_forward=1
  ip -n flvxqa link set qh$ID up
  ip -n flvxqa route add 203.0.113.$LAST/32 dev qh$ID src 203.0.113.2
  ip -n $NS addr add 203.0.113.$LAST/32 dev qn$ID; ip -n $NS link set qn$ID up; ip -n $NS link set lo up
  ip -n $NS route add 203.0.113.2 dev qn$ID scope link
  ip -n $NS route add default via 203.0.113.2 dev qn$ID onlink
  ip route replace 203.0.113.$LAST/32 via 203.0.113.2 dev qa0   # host -> agent (DNS sink replies)
  mkdir -p /etc/netns/$NS
  echo "nameserver 203.0.113.1" > /etc/netns/$NS/resolv.conf
  printf '127.0.0.1 localhost\n' > /etc/netns/$NS/hosts
  nft_drop $NS
  for X in ${EXTRA_IPS:-}; do ip -n $NS addr add $X/32 dev qn$ID; ip -n flvxqa route add $X/32 dev qh$ID src 203.0.113.2; ip route replace $X/32 via 203.0.113.2 dev qa0; done
  D=$RUN/agents/n$ID; mkdir -p $D/landing
  [ -e $D/landing/flvx-landing-core ] || cp "$CORE" $D/landing/flvx-landing-core
  printf '{"addr":"203.0.113.2:16481","secret":"%s","node_id":%s,"http":0,"tls":0,"socks":0,"block_other":0}' "$SECRET" "$ID" > $D/config.json
  [ -e $D/gost.json ] || echo '{}' > $D/gost.json
  cd $D && FLVX_LANDING_TEST_DIRECT=1 FLVX_LANDING_TEST_DIR=$D/landing GOMAXPROCS=1 TMPDIR=$D \
    nohup ip netns exec $NS "$AGENT" > $D/agent.log 2>&1 &
  echo $! > $D/agent.pid
  echo "agent node $ID in $NS 203.0.113.$LAST pid $(cat $D/agent.pid)";;
down)
  for NS in $(ip netns list | awk '{print $1}' | grep '^qan' || true); do
    ip netns pids $NS | xargs -r kill 2>/dev/null || true
  done
  sleep 1
  for NS in $(ip netns list | awk '{print $1}' | grep '^qan' || true); do
    ID=${NS#qan}; LAST=$(ip -n $NS -4 addr show dev qn$ID | awk '/inet /{split($2,a,"[./]");print a[4]}'); ip route del 203.0.113.$LAST/32 2>/dev/null || true; ip netns del $NS; ip -n flvxqa link del qh$ID 2>/dev/null || true; rm -rf /etc/netns/$NS
  done
  echo agents-down;;
proof)
  for NS in $(ip netns list | awk '{print $1}' | grep '^qan' || true); do
    echo "== $NS"; ip netns exec $NS nft list set inet qa seen4 | sed -n '/elements/,/}/p'; ip netns exec $NS nft list chain inet qa out | grep counter
  done;;
*) echo "usage: $0 add ID LAST|down|proof"; exit 2;;
esac
