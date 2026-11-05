package repo

import (
	"path/filepath"
	"testing"
	"time"

	"go-backend/internal/store/model"

	"gorm.io/gorm"
)

// Plan 212: cost of the rollup inside one flow upload. A typical upload of one
// entry node: 3 users, each on entry → relay → exit (9 per-server keys).
func benchFlowBatch() FlowBatch {
	b := FlowBatch{Users: map[int64]FlowCounterDelta{}, QuotaUsage: map[int64]int64{}, ServerQuotas: map[ServerQuotaKey]FlowCounterDelta{}}
	for u := int64(2); u <= 4; u++ {
		b.Users[u] = FlowCounterDelta{In: 1 << 20, Out: 3 << 20}
		b.QuotaUsage[u] = 4 << 20
		for _, n := range []int64{1, 2, 3} {
			b.ServerQuotas[ServerQuotaKey{u, "node", n}] = FlowCounterDelta{In: 1 << 20, Out: 3 << 20}
		}
	}
	return b
}

func benchRepo(b *testing.B) *Repository {
	r, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { r.Close() })
	for u := int64(2); u <= 4; u++ {
		if err := r.db.Create(&model.User{ID: u, User: "u", Pwd: "x", RoleID: 1, Status: 1, Flow: 99999, FlowResetTime: 1}).Error; err != nil {
			b.Fatal(err)
		}
	}
	for n := int64(1); n <= 3; n++ {
		if err := r.db.Create(&model.Node{ID: n, Name: "n", Secret: "s", ServerIP: "127.0.0.1", Port: "1-2"}).Error; err != nil {
			b.Fatal(err)
		}
	}
	return r
}

// The whole upload (counters + server quotas + quotas + rollup).
func BenchmarkApplyFlowBatchTypical(b *testing.B) {
	r := benchRepo(b)
	batch := benchFlowBatch()
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.ApplyFlowBatch(batch, now); err != nil {
			b.Fatal(err)
		}
	}
}

// The rollup part alone, inside its own transaction (an upper bound of its share).
func BenchmarkTrafficDailyRollupTypical(b *testing.B) {
	r := benchRepo(b)
	batch := benchFlowBatch()
	now := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.db.Transaction(func(tx *gorm.DB) error { return addTrafficDailyTx(tx, batch.Users, batch.ServerQuotas, now) }); err != nil {
			b.Fatal(err)
		}
	}
}
