#!/usr/bin/env bash
# Runs the nftables integration tests in throwaway network namespaces, so the caller's
# firewall, conntrack table and interfaces are never touched:
#
#   client ns  c0 10.231.1.2 / fd31:1::2  <->  n0 10.231.1.1 / fd31:1::1   node ns (tests run here)
#   target ns  t0 10.231.2.2 / fd31:2::2  <->  n1 10.231.2.1 / fd31:2::1
#
# Usage (root): ./netns_integration.sh [go test -run regexp]
set -euo pipefail
cd "$(dirname "$0")"

id=$$
C="flvxc$id" N="flvxn$id" T="flvxt$id"
bin="$(mktemp -d)/nftables.test"

cleanup() {
	for ns in "$C" "$N" "$T"; do ip netns del "$ns" 2>/dev/null || true; done
	rm -rf "$(dirname "$bin")"
}
trap cleanup EXIT

go test -c -o "$bin" .

for ns in "$C" "$N" "$T"; do
	ip netns add "$ns"
	ip -n "$ns" link set lo up
done
ip link add name c0 netns "$C" type veth peer name n0 netns "$N"
ip link add name t0 netns "$T" type veth peer name n1 netns "$N"

ip -n "$C" addr add 10.231.1.2/24 dev c0
ip -n "$C" addr add 10.231.1.3/24 dev c0
ip -n "$C" addr add fd31:1::2/64 dev c0 nodad
ip -n "$C" addr add fd31:1::3/64 dev c0 nodad
ip -n "$C" link set c0 up
ip -n "$C" route add default via 10.231.1.1
ip -n "$C" -6 route add default via fd31:1::1

ip -n "$N" addr add 10.231.1.1/24 dev n0
ip -n "$N" addr add fd31:1::1/64 dev n0 nodad
ip -n "$N" addr add 10.231.2.1/24 dev n1
ip -n "$N" addr add fd31:2::1/64 dev n1 nodad
ip -n "$N" link set n0 up
ip -n "$N" link set n1 up

ip -n "$T" addr add 10.231.2.2/24 dev t0
ip -n "$T" addr add fd31:2::2/64 dev t0 nodad
ip -n "$T" link set t0 up
ip -n "$T" route add default via 10.231.2.1
ip -n "$T" -6 route add default via fd31:2::1

# Shorten only this throwaway node namespace's conntrack cleanup for quota slot tests.
ip netns exec "$N" sysctl -q -w net.netfilter.nf_conntrack_tcp_timeout_close=1
ip netns exec "$N" sysctl -q -w net.netfilter.nf_conntrack_tcp_timeout_time_wait=2
ip netns exec "$N" sysctl -q -w net.netfilter.nf_conntrack_tcp_timeout_syn_sent=2

FLVXR2_NFT_INTEGRATION=1 FLVXR2_NFT_CLIENT_NS="$C" FLVXR2_NFT_TARGET_NS="$T" \
	ip netns exec "$N" "$bin" -test.v -test.count=1 -test.run "${1:-.}"
