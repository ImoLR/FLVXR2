package ws

import (
	"strings"
	"testing"
)

func TestDNSCacheSessionView(t *testing.T) {
	s := &Server{nodes: map[int64]*nodeSession{}}
	if s.DNSCache(5) != nil {
		t.Fatal("offline node must have no view")
	}
	old := &nodeSession{nodeID: 5}
	s.nodes[5] = old
	if v := s.DNSCache(5); v == nil || v.Supported || v.Mode != "" {
		t.Fatalf("old agent view = %+v", v)
	}
	if s.DNSCacheCapable(5) {
		t.Fatal("old agent is not capable")
	}
	ns := &nodeSession{nodeID: 6, dnsCache: 1}
	s.nodes[6] = ns
	if v := s.DNSCache(6); v == nil || !v.Supported || v.Mode != "" {
		t.Fatalf("capable, not reported yet = %+v", v)
	}
	s.recordDNSCache(ns, nil) // metric without the field keeps the last state
	s.recordDNSCache(ns, &DNSCacheReport{Mode: "minimal", OK: true, Reason: strings.Repeat("长", 200), CheckedAt: 9})
	v := s.DNSCache(6)
	if v.Mode != "minimal" || !v.OK || len([]rune(v.Reason)) != 120 || v.CheckedAt != 9 {
		t.Fatalf("view = %+v", v)
	}
	s.recordDNSCache(ns, nil)
	if s.DNSCache(6).Mode != "minimal" {
		t.Fatal("nil report must not clear the state")
	}
	s.SetDNSCache(6, &DNSCacheReport{Mode: "weird", OK: true})
	if v := s.DNSCache(6); v.Mode != "none" || v.OK {
		t.Fatalf("unknown mode must read as none/not ok: %+v", v)
	}
}
