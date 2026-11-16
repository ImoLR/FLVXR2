package handler

import (
	"strings"
	"testing"
	"time"
)

// Plan 265: the node's LeiKwan client ports (also stopped ones) are never
// handed out; typed ports there are refused; an existing row keeps saving.
const lkTestForwards = `[{"forwardId":264,"listenPort":10015,"running":true},{"forwardId":281,"listenPort":10016,"running":false},{"forwardId":282,"listenPort":10017,"running":true}]`

func TestLeiKwanPortValidationMessages(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			r := landingMonitorRepo(t, dialect)
			e := &flowTestEnv{t: t, r: r, h: &Handler{repo: r}, now: time.Now().UnixMilli()}
			e.exec(`INSERT INTO node(id, name, secret, server_ip, port, created_time, status, leikwan_status, leikwan_forwards) VALUES(1, '阿里广州', 's1', '198.51.100.60', '10014-10020', ?, 1, 'running', ?)`, e.now, lkTestForwards)
			e.exec(`INSERT INTO node(id, name, secret, server_ip, port, created_time, status) VALUES(2, 'Bestvm TW', 's2', '198.51.100.12', '10014-10020', ?, 1)`, e.now)
			e.exec(`INSERT INTO forward_port(forward_id, node_id, port) VALUES(77, 1, 10015)`)
			lk, _ := e.h.getNodeRecord(1)
			plain, _ := e.h.getNodeRecord(2)
			want := "节点 阿里广州 端口 10016 已被本机利群转发占用，请换一个"
			for _, err := range []error{e.h.validateForwardPortAvailability(lk, 10016, 0), e.h.validateForwardPortAvailabilityTx(r.DB(), lk, 10016, 0)} {
				if err == nil || err.Error() != want {
					t.Fatalf("stopped LeiKwan port: %v", err)
				}
			}
			// The forward that already sits on 10015 keeps saving; another one is refused.
			if err := e.h.validateForwardPortAvailability(lk, 10015, 77); err != nil {
				t.Fatalf("grandfathered row refused: %v", err)
			}
			if err := e.h.validateForwardPortAvailabilityTx(r.DB(), lk, 10015, 77); err != nil {
				t.Fatalf("grandfathered row refused (tx): %v", err)
			}
			if err := e.h.validateForwardPortAvailability(lk, 10015, 78); err == nil || !strings.Contains(err.Error(), "已被本机利群转发占用") {
				t.Fatalf("other forward on LeiKwan port: %v", err)
			}
			if err := e.h.validateForwardPortAvailability(lk, 10018, 0); err != nil {
				t.Fatalf("free port refused: %v", err)
			}
			// A node without a LeiKwan client is unchanged.
			if err := e.h.validateForwardPortAvailability(plain, 10016, 0); err != nil {
				t.Fatalf("plain node: %v", err)
			}
			// Auto picks: 10014..10020 minus 10015-10017 (LeiKwan) = 10014, 10018-10020.
			for i := 0; i < 40; i++ {
				if p := e.h.pickRandomPortForNode(1); p >= 10015 && p <= 10017 || p == 0 {
					t.Fatalf("random pick %d", p)
				}
			}
			if p := e.h.resolvePortForNewEntryNode(1, 10016, 99); p >= 10015 && p <= 10017 {
				t.Fatalf("new entry kept LeiKwan reference port %d", p)
			}
			if p := e.h.resolvePortForNewEntryNode(2, 10016, 99); p != 10016 {
				t.Fatalf("plain node lost reference port: %d", p)
			}
		})
	}
}

func TestRandomPortAvoiding(t *testing.T) {
	avoid := map[int]bool{}
	for p := 10000; p < 60000; p++ {
		if p != 33333 {
			avoid[p] = true
		}
	}
	if p := randomPortAvoiding(avoid); p != 33333 {
		t.Fatalf("got %d", p)
	}
	if p := randomPortAvoiding(nil); p < 10000 || p >= 60000 {
		t.Fatalf("got %d", p)
	}
}

// The LeiKwan pipe (plan 094) dials 127.0.0.1:<listen port> on the entries —
// the FLVX side never listens there, so the reserved set must not break it.
func TestLeiKwanPortSkipKeepsPipeTunnel(t *testing.T) {
	e, _, pipe, commands := newLeiKwanTunnelTest(t)
	e.exec(`UPDATE node SET port='10014-10016', leikwan_status='running', leikwan_forwards=? WHERE id IN (1,2)`,
		`[{"forwardId":263,"listenPort":10015,"running":true},{"forwardId":264,"listenPort":10016,"running":false}]`)
	req := leiKwanTunnelRequest(pipe.ID)
	id := leiKwanCreateTunnel(t, e, pipe, req)
	if err := e.h.redeployTunnelAndForwards(id); err != nil {
		t.Fatalf("redeploy: %v", err)
	}
	dials := 0
	for _, command := range commands.snapshot() {
		if command.method == "AddChains" && command.nodeID <= 2 {
			dials++
			if got := chainAddress(command.data); got != "127.0.0.1:10015" {
				t.Fatalf("entry %d dial=%s", command.nodeID, got)
			}
		}
	}
	if dials == 0 {
		t.Fatal("no entry chain deployed")
	}
	req["id"] = id
	delete(req, "leikwanPipeId")
	if code, msg, _ := leiKwanHandlerCall(t, e.h.tunnelUpdate, req); code != 0 {
		t.Fatalf("unchanged edit: %d %s", code, msg)
	}
	// Rules on the pipe tunnel never get the entries' LeiKwan ports.
	for i := 0; i < 20; i++ {
		if p := e.h.pickTunnelPort(id); p != 10014 {
			t.Fatalf("pipe tunnel rule port %d", p)
		}
	}
}
