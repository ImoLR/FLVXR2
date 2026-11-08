package dnscache

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeHost struct {
	t   *testing.T
	dir string
	c   checker
}

func newFakeHost(t *testing.T, resolv string) *fakeHost {
	t.Helper()
	d := t.TempDir()
	h := &fakeHost{t: t, dir: d, c: checker{
		resolvConf: filepath.Join(d, "resolv.conf"),
		state:      filepath.Join(d, "state"),
		conf:       "/etc/flvx-dnscache/dnsmasq.conf",
		proc:       filepath.Join(d, "proc"),
	}}
	if resolv != "" {
		h.write(h.c.resolvConf, resolv)
	}
	if err := os.MkdirAll(h.c.proc, 0o755); err != nil {
		t.Fatal(err)
	}
	h.proc("1", "systemd", "/sbin/init")
	return h
}

func (h *fakeHost) write(p, s string) {
	h.t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHost) proc(pid, comm, cmdline string) {
	h.t.Helper()
	d := filepath.Join(h.c.proc, pid)
	if err := os.MkdirAll(d, 0o755); err != nil {
		h.t.Fatal(err)
	}
	h.write(filepath.Join(d, "comm"), comm+"\n")
	h.write(filepath.Join(d, "cmdline"), strings.ReplaceAll(cmdline, " ", "\x00")+"\x00")
}

func TestCheckRules(t *testing.T) {
	const ours = "/usr/sbin/dnsmasq --keep-in-foreground --conf-file=/etc/flvx-dnscache/dnsmasq.conf"
	cases := []struct {
		name   string
		resolv string
		state  string
		procs  [][3]string
		want   Status
	}{
		{"plan239 dnsmasq host", "# FLVX\nnameserver 127.0.0.1\nnameserver 1.1.1.1\nnameserver 8.8.8.8\noptions timeout:1 attempts:2\n", "",
			[][3]string{{"812", "dnsmasq", ours}}, Status{Mode: "dnsmasq", OK: true}},
		{"cache down, fallback still there", "nameserver 127.0.0.1\nnameserver 1.1.1.1\noptions timeout:1 attempts:2\n", "",
			nil, Status{Mode: "dnsmasq", Reason: "本机缓存 flvx-dnscache 未运行"}},
		{"someone else's dnsmasq does not count", "nameserver 127.0.0.1\nnameserver 1.1.1.1\n", "",
			[][3]string{{"900", "dnsmasq", "/usr/sbin/dnsmasq -x /run/dnsmasq/dnsmasq.pid"}}, Status{Mode: "dnsmasq", Reason: "本机缓存 flvx-dnscache 未运行"}},
		{"other local resolver, ensure chose minimal", "nameserver 127.0.0.1\nnameserver 8.8.8.8\noptions timeout:1\n", "mode=minimal\nreason=53 端口已被 unbound 占用\n",
			[][3]string{{"77", "unbound", "/usr/sbin/unbound -d"}}, Status{Mode: "minimal", OK: true, Reason: "53 端口已被 unbound 占用"}},
		{"resolved stub", "nameserver 127.0.0.53\noptions edns0 trust-ad\nsearch .\n", "",
			[][3]string{{"300", "systemd-resolve", "/lib/systemd/systemd-resolved"}}, Status{Mode: "resolved", OK: true}},
		{"resolved stub, resolved dead", "nameserver 127.0.0.53\nnameserver 1.1.1.1\noptions timeout:1 attempts:2 edns0 trust-ad\n", "",
			nil, Status{Mode: "resolved", Reason: "systemd-resolved 未运行"}},
		{"plan239 phase-A only (Lightlayer TW)", "nameserver 8.8.8.8\nnameserver 1.1.1.1\noptions timeout:1 attempts:2\n", "",
			nil, Status{Mode: "minimal", OK: true, Reason: "未装本机缓存（只有备用解析器 + 1 秒超时）"}},
		{"minimal with reason from ensure", "nameserver 8.8.8.8\nnameserver 1.1.1.1\noptions timeout:1 attempts:2 rotate\n", "version=1\nmode=minimal\nreason=可用内存 40 MB（低于 60 MB）\n",
			nil, Status{Mode: "minimal", OK: true, Reason: "可用内存 40 MB（低于 60 MB）"}},
		{"fresh host, single resolver", "nameserver 10.0.0.2\n", "", nil, Status{Mode: "none", Reason: "未启用本机 DNS 缓存（只有 1 个解析器）"}},
		{"fresh host, no timeout", "nameserver 1.1.1.1\nnameserver 8.8.8.8\n", "", nil, Status{Mode: "none", Reason: "未启用本机 DNS 缓存（解析超时仍是 5 秒）"}},
		{"commented nameservers ignored", "#nameserver 127.0.0.1\n; nameserver 1.1.1.1\n", "", nil, Status{Mode: "none", Reason: "resolv.conf 里没有 nameserver"}},
		{"no resolv.conf", "", "", nil, Status{Mode: "none", Reason: "没有 /etc/resolv.conf"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHost(t, tc.resolv)
			if tc.state != "" {
				h.write(h.c.state, tc.state)
			}
			for _, p := range tc.procs {
				h.proc(p[0], p[1], p[2])
			}
			got := h.c.check()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("check() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCheckSetsTime(t *testing.T) {
	if s := Check(); s.CheckedAt == 0 || s.Mode == "" {
		t.Fatalf("Check() = %+v", s)
	}
}

func TestValidZones(t *testing.T) {
	got := ValidZones([]string{"imgamer.top", "IMGAMER.TOP.", "flvxxht.imgamer.top", "1.2.3.4", "bad zone", "a;rm -rf /", "-x.com", "localhost", "", "example.co.uk"})
	want := []string{"imgamer.top", "flvxxht.imgamer.top", "example.co.uk"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ValidZones = %v, want %v", got, want)
	}
	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, "z"+string(rune('a'+i))+".com")
	}
	if n := len(ValidZones(many)); n != 12 {
		t.Fatalf("ValidZones cap = %d", n)
	}
}

func TestEmbeddedScript(t *testing.T) {
	s := string(Script)
	for _, must := range []string{"#!/bin/bash", "listen-address=$listen", "bind-interfaces", "all-servers", "cache-size=1000",
		"max-cache-ttl=300", "neg-ttl=30", "MemoryMax=32M", "CPUQuota=20%", "Restart=always", "timeout:1 attempts:2",
		"do_check()", "rollback.sh", "/root/dns-backup-"} {
		if !strings.Contains(s, must) {
			t.Errorf("embedded script lacks %q", must)
		}
	}
	if strings.Contains(s, "log-queries") {
		t.Error("script must not enable the query log")
	}
}

func TestInstallScriptAt(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sbin", "flvx-dns-cache")
	if err := installScriptAt(p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("stat %v %v", st, err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != string(Script) {
		t.Fatal("content differs")
	}
	if err := installScriptAt(p); err != nil { // unchanged: no rewrite
		t.Fatal(err)
	}
}

func TestSummaryAndTail(t *testing.T) {
	out := "[flvx-dns-cache] x\nDNS 缓存：本机 dnsmasq（127.0.0.1，上游 1.1.1.1 8.8.4.4）无需改动\nRESULT mode=dnsmasq changed=0 mode=dnsmasq ok=1 reason=\n"
	if s := Summary(out); !strings.HasPrefix(s, "DNS 缓存：本机 dnsmasq") {
		t.Fatalf("Summary = %q", s)
	}
	long := strings.Repeat("line\n", 100)
	if n := len(strings.Split(tail(long), "\n")); n != 30 {
		t.Fatalf("tail lines = %d", n)
	}
}
