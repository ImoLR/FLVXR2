package socket

import (
	"net"
	"strconv"
	"testing"

	"github.com/go-gost/x/registry"
)

type fakeClientIPService struct{ counts map[string]int }

func (*fakeClientIPService) Serve() error                     { return nil }
func (*fakeClientIPService) Addr() net.Addr                   { return nil }
func (*fakeClientIPService) Close() error                     { return nil }
func (s *fakeClientIPService) ClientIPCounts() map[string]int { return s.counts }

type fakeClientIPNft struct {
	NftablesManagerInterface
	counts map[int64]map[string]int
}

func (f *fakeClientIPNft) GetForwardClientIPs(ids []int64) (map[int64]map[string]int, error) {
	out := make(map[int64]map[string]int)
	for _, id := range ids {
		if counts, ok := f.counts[id]; ok {
			out[id] = counts
		}
	}
	return out, nil
}

func TestGetServiceClientIPsCommandGostAndNft(t *testing.T) {
	name := "909201_7_8_tcp"
	if err := registry.ServiceRegistry().Register(name, &fakeClientIPService{counts: map[string]int{"192.0.2.1": 2}}); err != nil {
		t.Fatal(err)
	}
	defer registry.ServiceRegistry().Unregister(name)
	w := &WebSocketReporter{nftablesMgr: &fakeClientIPNft{counts: map[int64]map[string]int{909202: {"2001:db8::1": 3}}}}
	data, err := w.handleGetServiceClientIPs(map[string]interface{}{"forwardIds": []int64{909201, 909202}})
	if err != nil {
		t.Fatal(err)
	}
	services := data["services"].(map[string]serviceClientIPSnapshot)
	if services[name].ConnectionCount != 2 || services["909202_nft"].ConnectionCount != 3 || services["909202_nft"].IPCount != 1 {
		t.Fatalf("unexpected service and nft snapshots: %+v", services)
	}
}

func TestSnapshotClientIPsTruncationKeepsTrueTotals(t *testing.T) {
	counts := make(map[string]int)
	for i := 0; i < 503; i++ {
		counts["192.0.2."+strconv.Itoa(i)] = 2
	}
	got := snapshotClientIPs(counts)
	if !got.Truncated || got.IPCount != 503 || got.ConnectionCount != 1006 || len(got.IPs) != 500 {
		t.Fatalf("unexpected truncated snapshot: count=%d conns=%d shown=%d truncated=%v", got.IPCount, got.ConnectionCount, len(got.IPs), got.Truncated)
	}
}
