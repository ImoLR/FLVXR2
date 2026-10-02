package handler

import "testing"

func TestSumForwardConnectionsDistinctEntryNodes(t *testing.T) {
	calls := make(map[int64]int)
	got := sumForwardConnections([]int64{2, 23, 2, 32, 23}, func(nodeID int64) int {
		calls[nodeID]++
		return map[int64]int{2: 3, 23: 5, 32: 7}[nodeID]
	})
	if got != 15 || calls[2] != 1 || calls[23] != 1 || calls[32] != 1 {
		t.Fatalf("got %d connections and calls %v, want 15 and one call per node", got, calls)
	}
}
