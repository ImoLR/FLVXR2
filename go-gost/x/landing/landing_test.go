package landing

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testNode(t *testing.T, id int64) Node {
	t.Helper()
	key := func() string {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(b)
	}
	return Node{ID: id, InboundTag: "flvxl-" + strconv.FormatInt(id, 10), Protocol: "ss2022", Port: 30000 + int(id), Enabled: true, SS2022: &SS2022{Method: SS2022Method, ServerKey: key(), UserKey: key()}}
}
func TestLandingValidationAndImmutableRouting(t *testing.T) {
	n := testNode(t, 1)
	d := Desired{Revision: 1, Enabled: true, Nodes: []Node{n}}
	if err := d.validate(); err != nil {
		t.Fatal(err)
	}
	if d.CoreMemoryMB != 256 || d.CoreCPUPercent != 50 {
		t.Fatal("missing resource defaults")
	}
	for _, key := range []string{"bad key", base64.StdEncoding.EncodeToString(make([]byte, 32)), base64.RawStdEncoding.EncodeToString(make([]byte, 16))} {
		bad := testNode(t, 1)
		bad.SS2022.UserKey = key
		if _, err := bad.inbound(); err == nil {
			t.Fatal("invalid SS2022 key accepted")
		}
	}
	bad := d
	bad.Nodes = append(append([]Node(nil), d.Nodes...), n)
	if err := bad.validate(); err == nil {
		t.Fatal("duplicate port/id accepted")
	}
	bad = d
	bad.Nodes = []Node{n}
	bad.Nodes[0].Port = 443
	if err := bad.validate(); err == nil {
		t.Fatal("privileged port accepted")
	}
	cfg := baseConfig(32123)
	if cfg["api"].(map[string]any)["listen"] != "127.0.0.1:32123" {
		t.Fatal("API not loopback-only")
	}
	rule := cfg["routing"].(map[string]any)["rules"].([]any)[0].(map[string]any)
	if rule["outboundTag"] != "block" || len(rule["ip"].([]string)) != 14 {
		t.Fatal("private-address first block missing")
	}
	m := NewManager("/etc/flvx_agent/landing", false)
	m.desired = d
	unit, err := m.unit()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"User=flvx-landing", "MemoryMax=256M", "CPUQuota=50%", "NoNewPrivileges=yes", "ProtectSystem=strict", "LimitNOFILE=65535", "flvx-landing-core"} {
		if !strings.Contains(unit, required) {
			t.Fatalf("unit missing %s", required)
		}
	}
	if strings.Contains(unit, "xray") {
		t.Fatal("installation name collides with mmwx process detector")
	}
}

// Run with FLVX_LANDING_TEST_BINARY pointing to the SHA256-verified release asset.
func TestLandingPinnedCore(t *testing.T) {
	binary := os.Getenv("FLVX_LANDING_TEST_BINARY")
	if binary == "" {
		t.Skip("pinned Core integration binary not supplied")
	}
	hash, err := fileHash(binary)
	if err != nil || hash != CoreSHA256["amd64"] {
		t.Fatal("pinned binary checksum mismatch", err)
	}
	dir := t.TempDir()
	m := NewManager(dir, true)
	bytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.binary(), bytes, 0700); err != nil {
		t.Fatal(err)
	}
	n := testNode(t, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n.Port = listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	v := testNode(t, 2)
	v.Protocol = "vless_reality_vision"
	v.Port = n.Port + 1
	v.SS2022 = nil
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	v.VLESS = &VLESS{UUID: "d77c8b43-40ed-42cd-9150-f3f47d0f534d", Flow: "xtls-rprx-vision", Reality: Reality{Dest: "www.lovelive-anime.jp:443", ServerNames: []string{"www.lovelive-anime.jp"}, PrivateKey: base64.RawURLEncoding.EncodeToString(key.Bytes()), ShortIDs: []string{""}}}
	config := baseConfig(n.Port + 2)
	in1, _ := n.inbound()
	in2, err := v.inbound()
	if err != nil {
		t.Fatal(err)
	}
	config["inbounds"] = []any{in1, in2}
	if err := m.writeJSON("test-config.json", config); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(binary, "run", "-test", "-config", filepath.Join(dir, "test-config.json")).CombinedOutput(); err != nil {
		t.Fatalf("real SS2022 + Reality config check: %v %s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	m.desired = Desired{Revision: 1, Enabled: true, CoreMemoryMB: 256, CoreCPUPercent: 50, MaxConnections: 4, Nodes: []Node{n}}
	if err := m.setup(ctx); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, m.binary(), "run", "-config", filepath.Join(dir, "config.json"))
	cmd.Env = append(os.Environ(), "MMWXC_CORE_CONTROL_SOCKET="+m.socket())
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	for i := 0; i < 50; i++ {
		if _, err = m.snapshot(ctx); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	n.MaxConnections = 2
	n.MaxOnlineIPs = 1
	m.desired.Nodes = []Node{n}
	status, err := m.Apply(ctx, m.desired)
	if err != nil {
		t.Fatal(err)
	}
	epoch := status.Epoch

	// Inbound totals include bytes read before authentication, and removing and
	// restoring a tag must not reset them within a process epoch.
	sendMalformed := func() {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(n.Port)), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = c.Write([]byte(strings.Repeat("x", 1000)))
		_ = c.Close()
	}
	sendMalformed()
	var initial int64
	for i := 0; i < 50; i++ {
		counters := m.Counters()
		if len(counters) > 0 && counters[0].UpBytes > 0 {
			initial = counters[0].UpBytes
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if initial == 0 {
		t.Fatal("unauthenticated inbound traffic was not counted")
	}
	pause := m.desired
	pause.Revision++
	pause.Nodes = append([]Node(nil), pause.Nodes...)
	pause.Nodes[0].Enabled = false
	if _, err := m.Apply(ctx, pause); err != nil {
		t.Fatal(err)
	}
	pause.Revision++
	pause.Nodes[0].Enabled = true
	if _, err := m.Apply(ctx, pause); err != nil {
		t.Fatal(err)
	}
	sendMalformed()
	var after int64
	for i := 0; i < 50; i++ {
		counters := m.Counters()
		after = counters[0].UpBytes
		if after > initial {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if after <= initial || m.Status().Epoch != epoch {
		t.Fatalf("same-epoch counter reset after restore: %d -> %d", initial, after)
	}
	t.Logf("unauthenticated SS2022 cumulative bytes preserved after pause/restore: %d -> %d", initial, after)
	raw, err := m.api(ctx, "lsi")
	if err != nil || !strings.Contains(string(raw), n.InboundTag) {
		t.Fatalf("gRPC AddInbound failed: %s %v", raw, err)
	}
	d := m.desired
	d.Revision++
	d.Nodes = append(d.Nodes, v)
	if _, err := m.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}
	if m.Status().Epoch != epoch {
		t.Fatal("adding an inbound restarted Core")
	}
	d.Revision++
	d.Nodes[1].Enabled = false
	if _, err := m.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}
	raw, err = m.api(ctx, "lsi")
	if err != nil || strings.Contains(string(raw), v.InboundTag) || !strings.Contains(string(raw), n.InboundTag) {
		t.Fatalf("gRPC RemoveInbound diff failed: %s %v", raw, err)
	}
	recovered := NewManager(dir, true)
	if _, err := recovered.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}
	if recovered.Status().Epoch != epoch {
		t.Fatal("agent recovery restarted Core")
	}
	if _, err := recovered.Apply(ctx, Desired{Revision: 1, Enabled: true}); err == nil {
		t.Fatal("stale revision accepted")
	}
	var limits map[string]any
	if err := m.control(ctx, "GET", "/v1/snapshot", nil, &limits); err != nil {
		t.Fatal(err)
	}
	if limits["global"].(map[string]any)["max_total"] != float64(4) {
		t.Fatal("server connection limit not applied")
	}
	c := m.Counters()
	if len(c) != 2 || c[0].Epoch != epoch {
		t.Fatalf("counter reporting failed: %+v", c)
	}
	fi, err := os.Stat(filepath.Join(dir, "desired.json"))
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatal("desired credentials not protected")
	}
	var persisted Desired
	b, _ := os.ReadFile(filepath.Join(dir, "desired.json"))
	if err := json.Unmarshal(b, &persisted); err != nil || persisted.Revision != d.Revision {
		t.Fatal("desired state missing")
	}
	t.Logf("real Core config valid; epoch=%s preserved by hot add/remove and agent restart; server limit=4; node limit=2/IP=1; cumulative rows=%d", epoch, len(c))
	// A real Core restart has a newer epoch and replays persisted desired state.
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	restarted := exec.CommandContext(ctx, m.binary(), "run", "-config", filepath.Join(dir, "config.json"))
	restarted.Env = append(os.Environ(), "MMWXC_CORE_CONTROL_SOCKET="+m.socket())
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = restarted.Process.Kill(); _ = restarted.Wait() }()
	for i := 0; i < 50; i++ {
		snap, e := recovered.snapshot(ctx)
		if e == nil && strconv.FormatInt(snap.StartedAt.UnixNano(), 10) != epoch {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := recovered.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}
	epochBefore, _ := strconv.ParseInt(epoch, 10, 64)
	epochAfter, _ := strconv.ParseInt(recovered.Status().Epoch, 10, 64)
	if epochAfter <= epochBefore {
		t.Fatal("core restart did not advance monotonic epoch")
	}
	raw, err = recovered.api(ctx, "lsi")
	if err != nil || !strings.Contains(string(raw), n.InboundTag) {
		t.Fatal("core restart did not restore persisted inbound")
	}

}
