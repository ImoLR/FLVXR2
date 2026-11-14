package sublink

import (
	"strconv"
	"testing"
)

// nodes100: 100 mixed-protocol nodes (the plan-193 subscription cap).
func nodes100(t *testing.T) []Node {
	keys, m := matrixNodes(t)
	nodes := make([]Node, 0, 100)
	for i := 0; len(nodes) < 100; i++ {
		n := m[keys[i%len(keys)]]
		n.Name = keys[i%len(keys)] + " " + strconv.Itoa(i)
		nodes = append(nodes, n)
	}
	FinalizeNames(nodes)
	return nodes
}

// BenchmarkRender100: generator time / allocations for 100 nodes per format
// (plan 193 before/after numbers).
func BenchmarkRender100(b *testing.B) { benchRender(b, nodes100(&testing.T{})) }

// BenchmarkRender1: the same for a one-node subscription (plan 226).
func BenchmarkRender1(b *testing.B) { benchRender(b, nodes100(&testing.T{})[:1]) }

func benchRender(b *testing.B, nodes []Node) {
	for _, c := range []struct {
		name string
		fn   func() ([]byte, int)
	}{
		{"clash", func() ([]byte, int) { return Clash(nodes, ModeCNDirect, false) }},
		{"stash", func() ([]byte, int) { return Clash(nodes, ModeCNDirect, true) }},
		{"singbox", func() ([]byte, int) { return SingBox(nodes, ModeCNDirect, testOrigin) }},
		{"base64", func() ([]byte, int) { return Base64(nodes) }},
		{"surge", func() ([]byte, int) { return Surge(nodes, ModeCNDirect, false) }},
		{"surfboard", func() ([]byte, int) { return Surge(nodes, ModeCNDirect, true) }},
		{"loon", func() ([]byte, int) { return Loon(nodes) }},
		{"qx", func() ([]byte, int) { return QX(nodes) }},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				c.fn()
			}
		})
	}
}

// routing30: the plan-256 caps — every catalog entry plus custom entries up
// to 30, 300 custom lines in total, a node exit.
func routing30(nodes []Node) []RouteEntry {
	out := []RouteEntry{}
	for i := 0; len(out) < MaxRouteEntries-len(Catalog); i++ {
		lines := make([]string, 0, 19)
		for j := 0; j < 19 && i*19+j < MaxRouteLines; j++ {
			n := i*19 + j
			switch n % 4 {
			case 0:
				lines = append(lines, "site"+strconv.Itoa(n)+".example.com")
			case 1:
				lines = append(lines, "full:host"+strconv.Itoa(n)+".example.net")
			case 2:
				lines = append(lines, "word"+strconv.Itoa(n))
			default:
				lines = append(lines, "10."+strconv.Itoa(n/256)+"."+strconv.Itoa(n%256)+".0/24")
			}
		}
		out = append(out, RouteEntry{Type: RouteCustom, Name: "c" + strconv.Itoa(i), Lines: lines, Exit: ExitDirect})
	}
	for _, c := range Catalog {
		out = append(out, RouteEntry{Type: RouteBuiltin, Key: c.Key, Exit: c.Exit})
	}
	out[0].Exit = ExitNodePrefix + nodes[3].Key
	return out
}

// BenchmarkRender100Routed (plan 256): 100 nodes + 30 分流 entries + 300
// custom lines.
func BenchmarkRender100Routed(b *testing.B) {
	nodes := nodes100(&testing.T{})
	for i := range nodes {
		nodes[i].Key = "e:" + strconv.Itoa(i)
	}
	routing := routing30(nodes)
	lines := 0
	for _, e := range routing {
		lines += len(e.Lines)
	}
	if len(routing) != MaxRouteEntries || lines != MaxRouteLines {
		b.Fatalf("entries %d lines %d", len(routing), lines)
	}
	for _, c := range []struct {
		name string
		fn   func() ([]byte, int)
	}{
		{"clash", func() ([]byte, int) { return ClashRouted(nodes, ModeCNDirect, false, routing, testOrigin) }},
		{"stash", func() ([]byte, int) { return ClashRouted(nodes, ModeCNDirect, true, routing, testOrigin) }},
		{"singbox", func() ([]byte, int) { return SingBoxRouted(nodes, ModeCNDirect, testOrigin, routing) }},
		{"surge", func() ([]byte, int) { return SurgeRouted(nodes, ModeCNDirect, false, routing, testOrigin) }},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			var size int
			for i := 0; i < b.N; i++ {
				out, _ := c.fn()
				size = len(out)
			}
			b.ReportMetric(float64(size), "bytes/doc")
		})
	}
}
