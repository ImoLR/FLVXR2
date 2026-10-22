package repo

import (
	"os"
	"path/filepath"
	"testing"

	"go-backend/internal/store/model"
)

func TestCompactNodeMetricsAveragesAndIsIdempotent(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	testCompactNodeMetrics(t, r)
}

// Same SQL on PostgreSQL (empty scratch database in FLVX_COMPACT_PG_DSN).
func TestCompactNodeMetricsPostgres(t *testing.T) {
	dsn := os.Getenv("FLVX_COMPACT_PG_DSN")
	if dsn == "" {
		t.Skip("set FLVX_COMPACT_PG_DSN to an empty scratch database")
	}
	r, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	testCompactNodeMetrics(t, r)
}

func testCompactNodeMetrics(t *testing.T, r *Repository) {
	t.Helper()
	var err error
	// Two nodes, 1 Hz for 2 minutes starting at a bucket boundary.
	const base = int64(1_800_000_000_000) // multiple of 60 000
	var rows []*model.NodeMetric
	for i := int64(0); i < 120; i++ {
		for node := int64(1); node <= 2; node++ {
			rows = append(rows, &model.NodeMetric{
				NodeID: node, Timestamp: base + i*1000,
				CPUUsage: float64(i % 30), NetInSpeed: 100 * node, NetInBytes: 1000 + i,
				TCPConns: 10, Uptime: 500 + i, PeriodRx: 7000 + i,
			})
		}
	}
	if err := r.InsertNodeMetricBatch(rows); err != nil {
		t.Fatal(err)
	}
	deleted, err := r.CompactNodeMetrics(base, base+60_000, 30_000)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 120 { // first minute, both nodes
		t.Fatalf("deleted %d", deleted)
	}
	var got []model.NodeMetric
	r.DB().Where("timestamp < ?", base+60_000).Order("node_id, timestamp").Find(&got)
	if len(got) != 4 {
		t.Fatalf("want 2 buckets x 2 nodes, got %d", len(got))
	}
	first := got[0]
	if first.Timestamp != base+15_000 || first.CPUUsage != 14.5 || first.NetInSpeed != 100 || first.Uptime != 529 || first.PeriodRx != 7029 || first.TCPConns != 10 {
		t.Fatalf("bucket values %+v", first)
	}
	// Second minute untouched (raw).
	var raw int64
	r.DB().Model(&model.NodeMetric{}).Where("timestamp >= ?", base+60_000).Count(&raw)
	if raw != 120 {
		t.Fatalf("raw rows outside the range changed: %d", raw)
	}
	// Re-running on a compact range writes nothing.
	if deleted, err := r.CompactNodeMetrics(base, base+60_000, 30_000); err != nil || deleted != 0 {
		t.Fatalf("second pass deleted=%d err=%v", deleted, err)
	}
	// Batched prune.
	n, err := r.PruneNodeMetricsBatch(base+90_000, 50)
	if err != nil || n != 50 {
		t.Fatalf("prune batch n=%d err=%v", n, err)
	}
	oldest, err := r.OldestNodeMetricTimestamp()
	if err != nil || oldest <= base {
		t.Fatalf("oldest %d err %v", oldest, err)
	}
}
