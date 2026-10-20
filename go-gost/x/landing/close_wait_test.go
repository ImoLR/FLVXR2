package landing

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Machine protection hands the CLOSE_WAIT timeout to the Core with the existing
// connection-limit PUT; 0 keeps today's explicit null (Core default: off).
func TestLandingCloseWaitTimeoutSentWithLimits(t *testing.T) {
	m := NewManager(t.TempDir(), true)
	if err := os.MkdirAll(filepath.Join(m.dir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", m.socket())
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]any, 4)
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		received <- body
		_, _ = w.Write([]byte(`{"success":true}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	m.desired = Desired{Enabled: true, Nodes: []Node{testNode(t, 1)}}
	send := func() any {
		t.Helper()
		if err := m.applyLimits(context.Background(), coreSnapshot{}); err != nil {
			t.Fatal(err)
		}
		body := <-received
		v, ok := body["default_close_wait_timeout_seconds"]
		if !ok {
			t.Fatal("field missing")
		}
		return v
	}
	if v := send(); v != nil {
		t.Fatalf("default = %v, want null", v)
	}
	m.SetCloseWaitTimeout(10)
	if v := send(); v != float64(10) {
		t.Fatalf("timeout = %v, want 10", v)
	}
	m.SetCloseWaitTimeout(0)
	if v := send(); v != nil {
		t.Fatalf("reset = %v, want null", v)
	}
	m.SetCloseWaitTimeout(-3)
	if v := send(); v != nil {
		t.Fatalf("negative = %v, want null", v)
	}
}

func TestLandingPublicPorts(t *testing.T) {
	ss := testNode(t, 1) // SS2022: TCP + UDP
	vless := testNode(t, 2)
	vless.Protocol = "vless_reality_vision"
	vless.Port = 30002
	off := testNode(t, 3)
	off.Port = 30003
	off.Enabled = false
	chain := testNode(t, 4)
	chain.Port = 30004
	chain.Listen = ChainListen
	d := Desired{Enabled: true, Nodes: []Node{ss, vless, off, chain}}
	got := d.publicPorts()
	if !reflect.DeepEqual(got.TCP, []int{ss.Port, 30002}) || !reflect.DeepEqual(got.UDP, []int{ss.Port}) {
		t.Fatalf("ports %+v", got)
	}
	d.Enabled = false
	if p := d.publicPorts(); len(p.TCP)+len(p.UDP) != 0 {
		t.Fatalf("disabled server ports %+v", p)
	}
	m := NewManager(t.TempDir(), true)
	if tcp, udp := m.PublicPorts(); tcp != nil || udp != nil {
		t.Fatal("ports before any desired state")
	}
	m.ports.Store(got)
	if tcp, _ := m.PublicPorts(); len(tcp) != 2 {
		t.Fatal("stored ports not returned")
	}
	// PublicPorts never waits for a reconcile holding the manager lock.
	m.mu.Lock()
	defer m.mu.Unlock()
	if tcp, _ := m.PublicPorts(); len(tcp) != 2 {
		t.Fatal("blocked by the manager lock")
	}
}
