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
