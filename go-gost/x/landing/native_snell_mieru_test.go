package landing

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Plan 317: Snell / Mieru as public landing nodes (landing_snell 1 /
// landing_mieru 1), rendered like mmwx Custom's shared nodes.
func publicSnellNode(t *testing.T, id int64, version int, obfs, host string) Node {
	t.Helper()
	n := testNode(t, id)
	n.Protocol, n.SS2022 = ProtocolSnell, nil
	n.Snell = &Snell{PSK: "Abcdefghijklmnop1234567890ABCDEF", Version: version, Obfs: obfs, ObfsHost: host}
	return n
}

func publicMieruNode(t *testing.T, id int64) Node {
	t.Helper()
	n := testNode(t, id)
	n.Protocol, n.SS2022 = ProtocolMieru, nil
	n.Mieru = &Mieru{Username: "u7k2m9q0aa", Password: "Abcdefghijklmnop12345678"}
	return n
}

func TestNativeSnellMieruInbound(t *testing.T) {
	if SnellVersion < 1 || MieruVersion < 1 {
		t.Fatal("public Snell / Mieru nodes need landing_snell / landing_mieru >= 1")
	}
	// settings = byte for byte what mmwx Custom renders for a shared node
	// (internal/flvx/render.go), so a node moved over keeps every client.
	for _, tc := range []struct {
		node     Node
		settings string
	}{
		{publicSnellNode(t, 7, 4, "", ""), `{"users":[{"email":"flvxl-7","psk":"Abcdefghijklmnop1234567890ABCDEF","version":4}]}`},
		{publicSnellNode(t, 7, 4, "none", ""), `{"users":[{"email":"flvxl-7","psk":"Abcdefghijklmnop1234567890ABCDEF","version":4}]}`},
		{publicSnellNode(t, 7, 5, "http", "www.bing.com"), `{"users":[{"email":"flvxl-7","obfsHost":"www.bing.com","obfsMode":"http","psk":"Abcdefghijklmnop1234567890ABCDEF","version":5}]}`},
		{publicSnellNode(t, 7, 4, "tls", "www.bing.com"), `{"users":[{"email":"flvxl-7","obfsHost":"www.bing.com","obfsMode":"tls","psk":"Abcdefghijklmnop1234567890ABCDEF","version":4}]}`},
		{publicMieruNode(t, 7), `{"users":[{"email":"flvxl-7","password":"Abcdefghijklmnop12345678","username":"u7k2m9q0aa"}]}`},
	} {
		d := Desired{Revision: 1, Enabled: true, Nodes: []Node{testNode(t, 1), tc.node}}
		if err := d.validate(); err != nil {
			t.Fatalf("%s rejected: %v", tc.node.Protocol, err)
		}
		in, err := tc.node.inbound()
		if err != nil {
			t.Fatal(err)
		}
		if in["protocol"] != tc.node.Protocol || in["listen"] != "0.0.0.0" || in["port"] != tc.node.Port || in["tag"] != "flvxl-7" {
			t.Fatalf("inbound = %v", in)
		}
		if got := canonical(in["settings"]); got != tc.settings {
			t.Fatalf("%s settings\n got %s\nwant %s", tc.node.Protocol, got, tc.settings)
		}
		// Sniffing exactly as every other landing inbound (private-target
		// protection; plan 316 vendor override works the same way).
		if s := canonical(in["sniffing"]); s != `{"destOverride":["http","tls"],"domainsExcluded":["regexp:.*"],"enabled":true,"routeOnly":true}` {
			t.Fatalf("sniffing = %s", s)
		}
		// Deferred accept on the TCP socket only (warm pool hold, plan 274).
		stream := canonical(in["streamSettings"])
		want := `{"network":"tcp","sockopt":{"customSockopt":[{"level":"6","network":"tcp","opt":"9","system":"linux","type":"int","value":"` + guardDeferAccept + `"}]}}`
		if stream != want {
			t.Fatalf("%s stream = %s", tc.node.Protocol, stream)
		}
	}
}

func TestNativeSnellMieruValidation(t *testing.T) {
	for name, fn := range map[string]func(n *Node){
		"snell missing":       func(n *Node) { n.Snell = nil },
		"snell short psk":     func(n *Node) { n.Snell.PSK = "short" },
		"snell psk symbol":    func(n *Node) { n.Snell.PSK = "Abcdefghijklmnop1234567!" },
		"snell v3":            func(n *Node) { n.Snell.Version = 3 },
		"snell v6":            func(n *Node) { n.Snell.Version = 6 },
		"snell obfs unknown":  func(n *Node) { n.Snell.Obfs = "ws" },
		"snell obfs no host":  func(n *Node) { n.Snell.Obfs = "http" },
		"snell obfs ip host":  func(n *Node) { n.Snell.Obfs, n.Snell.ObfsHost = "tls", "1.2.3.4" },
		"snell none w/ host":  func(n *Node) { n.Snell.ObfsHost = "www.bing.com" },
		"snell host injected": func(n *Node) { n.Snell.Obfs, n.Snell.ObfsHost = "http", "a.com\r\nx" },
		"snell loopback":      func(n *Node) { n.Listen = ChainListen },
	} {
		m := publicSnellNode(t, 7, 4, "", "")
		s := *m.Snell
		m.Snell = &s
		fn(&m)
		if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{m}}).validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, fn := range map[string]func(n *Node){
		"mieru missing":         func(n *Node) { n.Mieru = nil },
		"mieru short user":      func(n *Node) { n.Mieru.Username = "abc" },
		"mieru long user":       func(n *Node) { n.Mieru.Username = strings.Repeat("a", 33) },
		"mieru user symbol":     func(n *Node) { n.Mieru.Username = "user_name" },
		"mieru short password":  func(n *Node) { n.Mieru.Password = "short" },
		"mieru password symbol": func(n *Node) { n.Mieru.Password = "Abcdefghijklmnop1234567:" },
		"mieru loopback":        func(n *Node) { n.Listen = ChainListen },
	} {
		m := publicMieruNode(t, 7)
		c := *m.Mieru
		m.Mieru = &c
		fn(&m)
		if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{m}}).validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Moved-over share credentials: 4–32 alphanumeric usernames (mixed case),
	// 16–64 alphanumeric secrets.
	m := publicMieruNode(t, 7)
	m.Mieru = &Mieru{Username: "AbC4", Password: strings.Repeat("Z9", 32)}
	if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{m}}).validate(); err != nil {
		t.Fatalf("share-style Mieru credentials rejected: %v", err)
	}
	s := publicSnellNode(t, 8, 5, "http", "Www.Bing.Com")
	if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{s}}).validate(); err != nil {
		t.Fatalf("mixed-case obfs host rejected: %v", err)
	}
	// A Snell / Mieru node is never a chain node nor in a Reality guard group.
	c := publicMieruNode(t, 9)
	up := goldenChainUpstreams()[0]
	up.Tag = chainOutboundTag(c.ID)
	c.Listen, c.Chain = ChainListen, &up
	if err := (&Desired{Revision: 1, Enabled: true, Nodes: []Node{c}}).validate(); err == nil {
		t.Fatal("Mieru chain node accepted")
	}
	g := publicSnellNode(t, 7, 4, "", "")
	g.GuardPort = 45000
	if groups, byTag, warnings := (Desired{Revision: 1, Enabled: true, Nodes: []Node{g}}).guardGroups(); len(groups) != 0 || len(byTag) != 0 || len(warnings) != 1 {
		t.Fatalf("Snell node joined a Reality guard: %v %v %v", groups, byTag, warnings)
	}
}

// Mieru serves UDP on its port: public ports (machine protection), per-user
// rate (UDP shaped too) and the inbound failure probe count it; Snell is TCP.
func TestNativeSnellMieruPorts(t *testing.T) {
	snell, mieru := publicSnellNode(t, 7, 4, "", ""), publicMieruNode(t, 8)
	snell.RateGroup, mieru.RateGroup = "landing-user-7", "landing-user-7"
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{snell, mieru}, UserRates: &UserRates{Version: 1, Groups: []RateGroup{{Group: "landing-user-7", RateMbps: 10}}}}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	p := d.publicPorts()
	if len(p.TCP) != 2 || len(p.UDP) != 1 || p.UDP[0] != mieru.Port {
		t.Fatalf("public ports = %+v", p)
	}
	w := d.rateWant()
	if len(w.groups) != 1 || len(w.groups[0].tcp) != 2 || len(w.groups[0].udp) != 1 || int(w.groups[0].udp[0]) != mieru.Port {
		t.Fatalf("rate want = %+v", w.groups)
	}
	udp, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	busy := publicMieruNode(t, 9)
	busy.Port = udp.LocalAddr().(*net.UDPAddr).Port
	if code, reason := classifyInboundFailure(busy, errors.New("x")); code != NodeErrorPortInUse || reason != "UDP: address already in use" {
		t.Fatalf("mieru udp busy: %s %q", code, reason)
	}
	sn := publicSnellNode(t, 10, 4, "", "")
	sn.Port = busy.Port
	if code, _ := classifyInboundFailure(sn, errors.New("x")); code == NodeErrorPortInUse {
		t.Fatal("Snell node blamed a UDP holder (Snell listens on TCP only)")
	}
}

// The real pinned Core: hot add of Snell (v4 plain, v5 http, v4 tls) and Mieru
// next to SS2022 without a Core restart; Mieru holds UDP on its port; the
// Core lists the Snell UDP fix the protocol needs; removal keeps the Core.
func TestNativeSnellMieruRealCore(t *testing.T) {
	dir := t.TempDir()
	_ = realCore(t, dir)
	m := NewManager(dir, true)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	defer func() {
		if m.proc != nil {
			_ = m.proc.Kill()
		}
	}()
	public := testNode(t, 1)
	public.Port = freePort(t)
	d := Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, Nodes: []Node{public}}
	status, err := m.Apply(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	epoch := status.Epoch
	var features struct {
		Features []string `json:"features"`
	}
	if err := m.control(ctx, http.MethodGet, "/v1/features", nil, &features); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(features.Features, ","), "snell.udp_per_packet_target") {
		t.Fatalf("pinned Core lacks the Snell UDP fix: %v", features.Features)
	}
	extra := []Node{publicSnellNode(t, 7, 4, "", ""), publicSnellNode(t, 8, 5, "http", "www.bing.com"), publicSnellNode(t, 9, 4, "tls", "www.bing.com"), publicMieruNode(t, 10)}
	for i := range extra {
		extra[i].Port = freePort(t)
	}
	d.Revision++
	d.Nodes = append(d.Nodes, extra...)
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch || len(status.NodeErrors) != 0 {
		t.Fatalf("Snell / Mieru add restarted the Core or failed: %v %s/%s %+v", err, status.Epoch, epoch, status.NodeErrors)
	}
	listed := string(mustAPI(t, m, "lsi"))
	for _, n := range extra {
		if !strings.Contains(listed, n.InboundTag) {
			t.Fatalf("%s not listed", n.InboundTag)
		}
	}
	mi := extra[3]
	if pc, err := net.ListenPacket("udp", joinHostPort("0.0.0.0", mi.Port)); err == nil {
		_ = pc.Close()
		t.Fatal("Mieru inbound does not hold UDP on its port")
	}
	sn := extra[0]
	if pc, err := net.ListenPacket("udp", joinHostPort("0.0.0.0", sn.Port)); err != nil {
		t.Fatalf("Snell inbound holds UDP on its port: %v", err)
	} else {
		_ = pc.Close()
	}
	if b, _ := json.Marshal(status.Nodes); !strings.Contains(string(b), mi.InboundTag) {
		t.Fatalf("no counter for %s: %s", mi.InboundTag, b)
	}
	d.Revision++
	d.Nodes = d.Nodes[:1]
	if status, err = m.Apply(ctx, d); err != nil || status.Epoch != epoch {
		t.Fatalf("removal: %v", err)
	}
	if listed := string(mustAPI(t, m, "lsi")); strings.Contains(listed, mi.InboundTag) || strings.Contains(listed, sn.InboundTag) {
		t.Fatal("inbound left after removal")
	}
}
