//go:build leikwanlab

package handler

import (
	"os"
	"strconv"
	"strings"

	"go-backend/internal/probe"
)

// Plan 268 lab only (never in release builds): fake node→node latency edges
// for the 备线 picker screens, e.g. FLVX_LEIKWAN_LAB_EDGES=58-2:12,60-2:41.
func init() {
	spec := os.Getenv("FLVX_LEIKWAN_LAB_EDGES")
	if spec == "" {
		return
	}
	edges := []probe.Edge{}
	for _, part := range strings.Split(spec, ",") {
		pair, ms, ok := strings.Cut(part, ":")
		from, to, ok2 := strings.Cut(pair, "-")
		if !ok || !ok2 {
			continue
		}
		f, _ := strconv.ParseInt(from, 10, 64)
		t, _ := strconv.ParseInt(to, 10, 64)
		v, _ := strconv.ParseFloat(ms, 64)
		edges = append(edges, probe.Edge{From: f, To: t, MS: v, OK: true})
	}
	leiKwanLabEdges = func() []probe.Edge { return edges }
}
