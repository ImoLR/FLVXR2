package repo

import (
	"path/filepath"
	"testing"

	"go-backend/internal/store/model"
)

// Plan 321: entry tags from DDNS records (carrier from the source or the
// admin's choice, IPv6 only when no A record), users' names inherit the tag
// of the record they point at, IPv4 / vendor names get none.
func TestEntryLabels(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	db := r.DB()
	for _, rec := range []model.NodeDDNSRecord{
		{NodeID: 48, Domain: "leikwan-hutai-ct.imgamer.top", RecordType: "AAAA", Role: "main", Source: "ct", CurrentIP: "240e:b8f:b07:4004::2813"},
		{NodeID: 48, Domain: "leikwan-hutai-cu.imgamer.top", RecordType: "AAAA", Role: "extra", Source: "cu", CurrentIP: "2408:820c:7508:34d4::2813", Inx: 1},
		{NodeID: 39, Domain: "bestvm-sg.imgamer.top", RecordType: "A", Role: "main", Source: "static", CurrentIP: "45.32.10.39"},
		{NodeID: 39, Domain: "bestvm-sg-v6.imgamer.top", RecordType: "AAAA", Role: "main", Source: "static", CurrentIP: "2a0f:1::39"},
		{NodeID: 20, Domain: "vmsilo-tw.imgamer.top", RecordType: "AAAA", Role: "main", Source: "cu", Carrier: "none", CurrentIP: "2408::20"},
		{NodeID: 58, Domain: "baidu-gz.imgamer.top", RecordType: "A", Role: "main", Source: "static", Carrier: "ct", CurrentIP: "45.32.10.58"},
		{NodeID: 12, Domain: "dual.imgamer.top", RecordType: "A", Role: "main", Source: "static", CurrentIP: "45.32.10.12"},
		{NodeID: 12, Domain: "dual.imgamer.top", RecordType: "AAAA", Role: "main", Source: "ct", CurrentIP: "240e::12"},
	} {
		rec := rec
		if err := db.Create(&rec).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []model.UserSubdomain{
		{UserID: 90, NodeID: 48, OriginNodeID: 48, Label: "atsks7i", Domain: "atsks7i.imgamer.top", State: "active"},
		{UserID: 90, NodeID: 48, OriginNodeID: 48, Label: "fh82sge", Domain: "fh82sge.imgamer.top", State: "active", RecordDomain: "leikwan-hutai-cu.imgamer.top"},
		{UserID: 91, NodeID: 48, OriginNodeID: 48, Label: "oldname", Domain: "oldname.imgamer.top", State: "retired"},
	} {
		n := n
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	x, err := r.LoadEntryLabels()
	if err != nil {
		t.Fatal(err)
	}
	for host, want := range map[string]EntryLabel{
		"leikwan-hutai-ct.imgamer.top":  {Carrier: "电信", IPv6: true},
		"LEIKWAN-HUTAI-CU.imgamer.top.": {Carrier: "联通", IPv6: true},
		"[240e:b8f:b07:4004::2813]:443": {Carrier: "电信", IPv6: true},
		"atsks7i.imgamer.top:24189":     {Carrier: "电信", IPv6: true}, // main record
		"fh82sge.imgamer.top":           {Carrier: "联通", IPv6: true},
		"oldname.imgamer.top":           {},
		"paid-v4.bgp1.leikwanhost.com":  {},
		"bestvm-sg.imgamer.top":         {},
		"bestvm-sg-v6.imgamer.top":      {IPv6: true},
		"vmsilo-tw.imgamer.top":         {IPv6: true},  // admin: 不标
		"baidu-gz.imgamer.top":          {Carrier: "电信"}, // admin choice on an A record
		"dual.imgamer.top":              {Carrier: "电信"}, // dual stack: no IPv6 tag
		"45.32.10.39:30001":             {},
		"2001:db8::1":                   {IPv6: true}, // any IPv6 literal
	} {
		got, _ := x.Of(host)
		if got != want {
			t.Errorf("%s: got %+v want %+v", host, got, want)
		}
	}
	list := x.ForList("atsks7i.imgamer.top:24189,fh82sge.imgamer.top:24189,paid-v4.bgp1.leikwanhost.com:24189")
	if len(list) != 2 || list["atsks7i.imgamer.top"].Carrier != "电信" || list["fh82sge.imgamer.top"].Carrier != "联通" {
		t.Fatalf("ForList: %+v", list)
	}
	if x.ForList("45.32.10.39:1,paid-v4.bgp1.leikwanhost.com:2") != nil {
		t.Fatal("IPv4-only list must have no tags")
	}
	if l, ok := x.Node(58); !ok || l.Carrier != "电信" {
		t.Fatalf("node 58: %+v %v", l, ok)
	}
	if _, ok := x.Node(48); ok {
		t.Fatal("a node with two DDNS domains has no node-level tag")
	}
	for _, c := range []string{"", "ct", "cu", "cm", "none"} {
		if !ValidEntryCarrier(c) {
			t.Fatalf("%q refused", c)
		}
	}
	if ValidEntryCarrier("cmcc") {
		t.Fatal("bad carrier accepted")
	}
}
