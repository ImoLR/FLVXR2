#!/usr/bin/env bash
# Machine protection (plan 118) E2E in throwaway network namespaces; the caller's
# firewall, conntrack table, sysctls and interfaces are never touched:
#
#   client ns  c0 10.231.1.2  <->  n0 10.231.1.1   node ns (tests run here)
#   target ns  t0 10.231.2.2  <->  n1 10.231.2.1
#
# Usage (root): ./machine_protect_netns.sh [go test -run regexp] [evidence file]
set -euo pipefail
cd "$(dirname "$0")"

id=$$
C="mpc$id" N="mpn$id" T="mpt$id"
work="$(mktemp -d "${TMPDIR:-/root}/mp-e2e.XXXXXX")"
bin="$work/socket.test"

cleanup() {
	for ns in "$C" "$N" "$T"; do ip netns del "$ns" 2>/dev/null || true; done
	rm -rf "$work"
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
ip -n "$C" link set c0 up
ip -n "$C" route add default via 10.231.1.1

ip -n "$N" addr add 10.231.1.1/24 dev n0
ip -n "$N" addr add 10.231.2.1/24 dev n1
ip -n "$N" link set n0 up
ip -n "$N" link set n1 up

ip -n "$T" addr add 10.231.2.2/24 dev t0
ip -n "$T" link set t0 up
ip -n "$T" route add default via 10.231.2.1

FLVX_MP_NETNS_E2E=1 FLVX_MP_CLIENT_NS="$C" FLVX_MP_TARGET_NS="$T" FLVX_MP_EVIDENCE="${2:-}" \
	ip netns exec "$N" "$bin" -test.v -test.count=1 -test.run "${1:-TestMachineProtectionNetns}"
