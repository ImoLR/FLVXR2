#!/usr/bin/env bash
# Plan 150 lab: a node with a second, non-default address (node 48's CU IPv6
# next to the CT one) must answer UDP from the address the client used.
#
#   client ns  c0 10.232.1.2 / fd32:1::2  <->  n0 10.232.1.1 + 10.232.1.5 (secondary)
#                                              fd32:1::1 + fd32:1::5 (deprecated, never the default source)
#
# Runs the wildcard UDP echo twice: plain socket (the bug: secondary addresses
# get no answer) and with StickySource (all four answer). Exit 1 otherwise.
# Usage (root): ./netns_sticky.sh
set -euo pipefail
cd "$(dirname "$0")"

id=$$
C="flvsc$id" N="flvsn$id"
work="$(mktemp -d)"
bin="$work/udp.test"
port=40150

cleanup() {
	for ns in "$C" "$N"; do ip netns del "$ns" 2>/dev/null || true; done
	rm -rf "$work"
}
trap cleanup EXIT

go test -c -o "$bin" .

for ns in "$C" "$N"; do
	ip netns add "$ns"
	ip -n "$ns" link set lo up
done
ip link add name c0 netns "$C" type veth peer name n0 netns "$N"
ip -n "$C" addr add 10.232.1.2/24 dev c0
ip -n "$C" addr add fd32:1::2/64 dev c0 nodad
ip -n "$C" link set c0 up
ip -n "$N" addr add 10.232.1.1/24 dev n0
ip -n "$N" addr add 10.232.1.5/24 dev n0
ip -n "$N" addr add fd32:1::1/64 dev n0 nodad
ip -n "$N" addr add fd32:1::5/64 dev n0 nodad preferred_lft 0 valid_lft forever
ip -n "$N" link set n0 up

targets="10.232.1.1 10.232.1.5 fd32:1::1 fd32:1::5"
declare -A result
for mode in plain sticky; do
	plain=0
	[ "$mode" = plain ] && plain=1
	FLVX_STICKY_NETNS_SERVE=$port FLVX_STICKY_NETNS_PLAIN=$plain FLVX_STICKY_NETNS_SECONDS=20 \
		ip netns exec "$N" "$bin" -test.run '^TestStickyNetnsServe$' -test.count=1 >"$work/server-$mode.log" 2>&1 &
	server=$!
	sleep 1
	for t in $targets; do
		hostport="$t:$port"
		case "$t" in *:*) hostport="[$t]:$port" ;; esac
		if FLVX_STICKY_NETNS_TARGET="$hostport" ip netns exec "$C" "$bin" -test.run '^TestStickySourceNetnsClient$' -test.count=1 >"$work/client.log" 2>&1; then
			result[$mode,$t]=OK
		else
			result[$mode,$t]=FAIL
		fi
	done
	kill "$server" 2>/dev/null || true
	wait "$server" 2>/dev/null || true
done

printf '%-14s %-6s %-6s\n' target plain sticky
rc=0
for t in $targets; do
	printf '%-14s %-6s %-6s\n' "$t" "${result[plain,$t]}" "${result[sticky,$t]}"
	[ "${result[sticky,$t]}" = OK ] || rc=1
done
# The plain socket must reproduce the bug on both secondary addresses.
[ "${result[plain,10.232.1.5]}" = FAIL ] && [ "${result[plain,fd32:1::5]}" = FAIL ] || rc=1
[ "${result[plain,10.232.1.1]}" = OK ] && [ "${result[plain,fd32:1::1]}" = OK ] || rc=1
echo "lab rc=$rc"
exit $rc
