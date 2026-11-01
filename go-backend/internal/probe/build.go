package probe

import (
	"sort"
	"time"
)

// NodeInput is a shown node as the caller resolved it (hidden nodes are
// simply not passed in). Name is the probe display name.
type NodeInput struct {
	ID       int64
	Name     string
	Region   string
	Version  string
	LastSeen int64 // ms of the newest stored metric (fallback for 「最后在线」), 0 unknown
}

// Sample is the newest agent report of a node, kept in memory (1 s reports).
type Sample struct {
	At      int64 // ms
	CPU     float64
	Mem     float64
	Disk    float64
	Load1   float64
	NetIn   int64
	NetOut  int64
	RxTotal int64
	TxTotal int64
	Uptime  int64
	TCP     int64
}

type BuildInput struct {
	Title     string
	LatencyOn bool
	Now       time.Time
	Nodes     []NodeInput
	Samples   map[int64]Sample
	Online    func(id int64) bool
	Edges     map[int64][]Edge // from node id → newest edges
	Key       func(id int64) string
}

const (
	// A sample older than this is not shown as live data.
	sampleMaxAge = 30 * time.Second
	// At most this many latency rows per card.
	maxLatencyRows = 12
)

// BuildSnapshot is pure: same input, same output.
func BuildSnapshot(in BuildInput) Snapshot {
	now := in.Now.UnixMilli()
	shown := make(map[int64]int, len(in.Nodes))
	for i, n := range in.Nodes {
		shown[n.ID] = i
	}
	out := Snapshot{Title: in.Title, TS: now, LatencyOn: in.LatencyOn, Nodes: make([]Node, 0, len(in.Nodes))}
	for _, n := range in.Nodes {
		osName, arch := ParseOSArch(n.Version)
		node := Node{K: in.Key(n.ID), Name: n.Name, Region: CleanRegion(n.Region), OS: osName, Arch: arch, Seen: n.LastSeen}
		s, has := in.Samples[n.ID]
		if has && s.At > node.Seen {
			node.Seen = s.At
		}
		node.Online = in.Online != nil && in.Online(n.ID)
		if node.Online && has && now-s.At <= sampleMaxAge.Milliseconds() {
			node.CPU = Round1(s.CPU)
			node.Mem = Round1(s.Mem)
			node.Disk = Round1(s.Disk)
			node.Load1 = float64(int64(s.Load1*100+0.5)) / 100
			node.NetIn, node.NetOut = nonNeg(s.NetIn), nonNeg(s.NetOut)
			node.TrafficIn, node.TrafficOut = nonNeg(s.RxTotal), nonNeg(s.TxTotal)
			node.Uptime, node.TCP = nonNeg(s.Uptime), nonNeg(s.TCP)
		}
		if in.LatencyOn {
			type ranked struct {
				idx int
				e   Edge
			}
			rows := make([]ranked, 0, len(in.Edges[n.ID]))
			for _, e := range in.Edges[n.ID] {
				idx, ok := shown[e.To]
				if !ok || e.To == n.ID {
					continue // hidden or unknown targets never appear
				}
				rows = append(rows, ranked{idx: idx, e: e})
			}
			sort.SliceStable(rows, func(i, j int) bool { return rows[i].idx < rows[j].idx })
			if len(rows) > maxLatencyRows {
				rows = rows[:maxLatencyRows]
			}
			for _, r := range rows {
				row := Latency{To: in.Key(r.e.To), OK: r.e.OK}
				if r.e.OK {
					row.MS = Round1(r.e.MS)
				}
				node.Latency = append(node.Latency, row)
			}
		}
		out.Nodes = append(out.Nodes, node)
	}
	return out
}

func nonNeg(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}
