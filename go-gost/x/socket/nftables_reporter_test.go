package socket

import (
	"testing"

	"github.com/go-gost/x/nftables"
	"github.com/go-gost/x/service"
)

type fakeNftablesManager struct {
	NftablesManagerInterface
	deltas []nftables.TrafficDelta
}

func (f *fakeNftablesManager) CollectTraffic() []nftables.TrafficDelta {
	out := f.deltas
	f.deltas = nil
	return out
}

// TestPollNftablesCountersReportsDirections: nftables traffic reaches the panel like gost
// service traffic, D = upload (from the client) and U = download (to the client), and every
// collected delta is reported once.
func TestPollNftablesCountersReportsDirections(t *testing.T) {
	mgr := &fakeNftablesManager{deltas: []nftables.TrafficDelta{
		{ForwardID: 9101, UserID: 2, UserTunnelID: 3, Protocol: "tcp", Port: 31001, UploadBytes: 1000, DownloadBytes: 7000},
		{ForwardID: 9101, UserID: 2, UserTunnelID: 3, Protocol: "udp", Port: 31001, UploadBytes: 5, DownloadBytes: 0},
	}}
	w := &WebSocketReporter{nftablesMgr: mgr}

	w.pollNftablesCounters()
	w.pollNftablesCounters() // nothing new

	up, down := service.GetGlobalTrafficManager().GetServiceTraffic("9101_2_3_tcp")
	if up != 7000 || down != 1000 {
		t.Fatalf("tcp traffic U=%d D=%d, want U=7000 (download) D=1000 (upload)", up, down)
	}
	up, down = service.GetGlobalTrafficManager().GetServiceTraffic("9101_2_3_udp")
	if up != 0 || down != 5 {
		t.Fatalf("udp traffic U=%d D=%d, want U=0 D=5", up, down)
	}
}
