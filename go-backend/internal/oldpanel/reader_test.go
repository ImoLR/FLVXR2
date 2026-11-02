package oldpanel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Schema of the 救命版 (flux-panel-backend 3.0.1-alpha6) tables the reader
// touches, as found in its gost.db (plus a column the reader ignores).
const fixtureSchema = "CREATE TABLE `node` (`id` integer PRIMARY KEY AUTOINCREMENT,`name` varchar(100) NOT NULL,`remark` text,`expiry_time` integer,`renewal_cycle` varchar(20),`secret` varchar(100) NOT NULL,`server_ip` varchar(100) NOT NULL,`server_ip_v4` varchar(100),`server_ip_v6` varchar(100),`extra_ips` text,`port` text NOT NULL,`version` varchar(100),`created_time` integer NOT NULL,`status` integer NOT NULL,`inx` integer NOT NULL DEFAULT 0,`is_remote` integer DEFAULT 0);" +
	"CREATE TABLE `node_metric` (`id` integer PRIMARY KEY AUTOINCREMENT,`node_id` integer NOT NULL,`timestamp` integer NOT NULL,`cpu_usage` real,`mem_usage` real,`disk_usage` real,`net_in_bytes` integer,`net_out_bytes` integer,`net_in_speed` integer,`net_out_speed` integer,`load1` real,`load5` real,`load15` real,`tcp_conns` integer,`udp_conns` integer,`uptime` integer);" +
	"CREATE INDEX `idx_node_metric_time` ON `node_metric`(`timestamp`);" +
	"CREATE INDEX `idx_node_metric_node_time` ON `node_metric`(`node_id`,`timestamp`);"

// fixture builds a WAL-mode database and keeps the writer connection open
// (the "old panel process") for the whole test.
func fixture(t *testing.T, now time.Time) (string, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gost.db")
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = w.Close() })
	if _, err := w.Exec(fixtureSchema); err != nil {
		t.Fatal(err)
	}
	ms := now.UnixMilli()
	nodes := []struct {
		id                 int64
		name, ip, v4, v6   string
		extra, ver, cycle  string
		expiry, inx, remot int64
	}{
		{1, "PQS TW", "pqstw.example.org", "", "", "", "3.0.1-rc4 (debian/amd64)", "year", ms + 86400000*30, 0, 0},
		{2, "Vmiss KR", "45.93.28.126", "45.93.28.126", "", "", "3.0.0-rc6 (debian/amd64)", "month", ms + 86400000*3, -1, 0},
		{3, "Remote", "198.51.100.9", "", "", "", "", "", 0, -2, 1},
		{4, "Offline SG", "194.127.192.26", "", "2a12:6e40::1", `["194.127.192.27"]`, "3.0.0 (ubuntu/arm64)", "", 0, -3, 0},
	}
	for _, n := range nodes {
		if _, err := w.Exec("INSERT INTO node(id,name,expiry_time,renewal_cycle,secret,server_ip,server_ip_v4,server_ip_v6,extra_ips,port,version,created_time,status,inx,is_remote) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
			n.id, n.name, n.expiry, n.cycle, "s3cret", n.ip, n.v4, n.v6, n.extra, "1000-2000", n.ver, ms, 1, n.inx, n.remot); err != nil {
			t.Fatal(err)
		}
	}
	// 1 h of 5 s rows for nodes 1 and 2; node 4 stopped 10 min ago.
	tx, _ := w.Begin()
	for ts := ms - 3600_000; ts <= ms; ts += 5000 {
		for _, id := range []int64{1, 2, 4} {
			if id == 4 && ts > ms-600_000 {
				continue
			}
			if _, err := tx.Exec("INSERT INTO node_metric(node_id,timestamp,cpu_usage,mem_usage,disk_usage,net_in_bytes,net_out_bytes,net_in_speed,net_out_speed,load1,load5,load15,tcp_conns,udp_conns,uptime) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
				id, ts, 10.0+float64(id), 40.0, 20.0, 1000*id, 2000*id, 300*id, 400*id, 0.5, 0.4, 0.3, 50*id, 3, 86400); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return path, w
}

func TestReaderReadsFixture(t *testing.T) {
	now := time.Now()
	path, w := fixture(t, now)
	r := New(path)
	defer r.Close()
	ctx := context.Background()

	nodes, err := r.Nodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 3 || nodes[0].ID != 4 || nodes[1].ID != 2 || nodes[2].ID != 1 {
		t.Fatalf("nodes (remote excluded, inx order): %+v", nodes)
	}
	if nodes[2].Name != "PQS TW" || nodes[2].RenewalCycle != "year" || nodes[2].ExpiryTime == 0 || nodes[0].ExtraIPs == "" || nodes[0].Version != "3.0.0 (ubuntu/arm64)" {
		t.Fatalf("node fields: %+v", nodes)
	}

	boot, err := r.MetricsSince(ctx, now.Add(-60*time.Second).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(boot) != 2*12 && len(boot) != 2*13 {
		t.Fatalf("bootstrap rows %d", len(boot))
	}
	var cursor int64
	for _, m := range boot {
		if m.NodeID == 4 {
			t.Fatal("stopped node in the 60 s window")
		}
		if m.ID > cursor {
			cursor = m.ID
		}
	}
	if more, err := r.MetricsAfter(ctx, cursor); err != nil || len(more) != 0 {
		t.Fatalf("nothing new: %d %v", len(more), err)
	}
	// The old panel flushes a batch: the cursor sees exactly the new rows.
	for i := 0; i < 3; i++ {
		if _, err := w.Exec("INSERT INTO node_metric(node_id,timestamp,cpu_usage,mem_usage,disk_usage,net_in_bytes,net_out_bytes,net_in_speed,net_out_speed,load1,tcp_conns,uptime) VALUES(2,?,77,41,20,5,6,7,8,0.9,99,100)", now.UnixMilli()+int64(i)*5000); err != nil {
			t.Fatal(err)
		}
	}
	more, err := r.MetricsAfter(ctx, cursor)
	if err != nil || len(more) != 3 || more[2].CPU != 77 || more[2].TCP != 99 || more[2].NetInSpeed != 7 || more[2].NetOutBytes != 6 || more[2].Uptime != 100 {
		t.Fatalf("incremental rows %+v %v", more, err)
	}

	ts, err := r.LatestMetricTime(ctx, 4)
	if err != nil || ts == 0 || now.UnixMilli()-ts < 600_000 {
		t.Fatalf("latest of the stopped node: %d %v", ts, err)
	}
	if ts, err := r.LatestMetricTime(ctx, 99); err != nil || ts != 0 {
		t.Fatalf("unknown node: %d %v", ts, err)
	}

	b, err := r.Buckets(ctx, 1, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), 60_000, 60)
	if err != nil || len(b) < 59 || len(b) > 62 {
		t.Fatalf("buckets %d %v", len(b), err)
	}
	if b[0].CPU != 11 || b[0].TCP != 50 || b[0].NetInSpeed != 300 || b[0].At%60_000 != 0 {
		t.Fatalf("bucket values %+v", b[0])
	}
}

func fileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return [32]byte{}
		}
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

// TestReaderNeverWrites: the reader's connection refuses writes, and a run of
// reads leaves the database and WAL files byte-for-byte unchanged.
func TestReaderNeverWrites(t *testing.T) {
	now := time.Now()
	path, _ := fixture(t, now)
	dbSum, walSum := fileSum(t, path), fileSum(t, path+"-wal")

	r := New(path)
	defer r.Close()
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := r.Nodes(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.MetricsSince(ctx, now.Add(-time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Buckets(ctx, 2, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), 30_000, 120); err != nil {
			t.Fatal(err)
		}
	}
	if fileSum(t, path) != dbSum || fileSum(t, path+"-wal") != walSum {
		t.Fatal("reads changed the database or WAL file")
	}

	// The same DSN refuses every kind of write.
	db, err := sql.Open("sqlite", DSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		"INSERT INTO node_metric(node_id,timestamp) VALUES(1,1)",
		"DELETE FROM node_metric",
		"UPDATE node SET name='x'",
		"CREATE TABLE x(a)",
		"CREATE INDEX x ON node(name)",
		"PRAGMA journal_mode=DELETE",
		"VACUUM",
	} {
		if _, err := db.Exec(stmt); err == nil {
			t.Fatalf("write allowed: %s", stmt)
		}
	}
	if fileSum(t, path) != dbSum {
		t.Fatal("write attempts changed the database")
	}
}

// TestReaderHoldsNoReadTransaction: right after the reader's queries the old
// panel can run a TRUNCATE checkpoint without being blocked, i.e. no read
// transaction (WAL read mark) is left open.
func TestReaderHoldsNoReadTransaction(t *testing.T) {
	now := time.Now()
	path, w := fixture(t, now)
	r := New(path)
	defer r.Close()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := r.MetricsSince(ctx, now.Add(-time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Nodes(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Exec("INSERT INTO node_metric(node_id,timestamp) VALUES(1,?)", now.UnixMilli()+int64(i)); err != nil {
			t.Fatal(err)
		}
		var busy, logFrames, done int
		if err := w.QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &done); err != nil {
			t.Fatal(err)
		}
		if busy != 0 {
			t.Fatalf("checkpoint blocked by the reader (round %d)", i)
		}
		if st, err := os.Stat(path + "-wal"); err != nil || st.Size() != 0 {
			t.Fatalf("WAL not truncated: %v %v", st, err)
		}
	}
}

func TestReaderMissingAndSchema(t *testing.T) {
	ctx := context.Background()
	missing := New(filepath.Join(t.TempDir(), "nope", "gost.db"))
	if _, err := missing.Nodes(ctx); Classify(err) != "missing" {
		t.Fatalf("missing file: %v (%s)", err, Classify(err))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(missing.Path()))); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("reader created something for a missing file")
	}
	if _, err := New("").Nodes(ctx); Classify(err) != "missing" {
		t.Fatal("empty path")
	}
	dir := t.TempDir()
	if _, err := New(dir).Nodes(ctx); Classify(err) != "missing" {
		t.Fatal("directory path")
	}

	// Another program's database: no node table / different columns.
	path := filepath.Join(t.TempDir(), "other.db")
	w, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Exec("CREATE TABLE node(id integer primary key, title text)"); err != nil {
		t.Fatal(err)
	}
	r := New(path)
	defer r.Close()
	if _, err := r.Nodes(ctx); !errors.Is(err, ErrSchema) || Classify(err) != "schema" {
		t.Fatalf("schema mismatch: %v (%s)", err, Classify(err))
	}
	if _, err := r.MetricsAfter(ctx, 0); Classify(err) != "schema" {
		t.Fatalf("no metric table: %v", err)
	}

	// Not a database at all.
	junk := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(junk, []byte("this is not sqlite, just some bytes padding padding padding padding padding padding"), 0o644); err != nil {
		t.Fatal(err)
	}
	jr := New(junk)
	defer jr.Close()
	if _, err := jr.Nodes(ctx); err == nil || Classify(err) == "" {
		t.Fatalf("junk file read: %v", err)
	}
	raw, _ := os.ReadFile(junk)
	if string(raw[:10]) != "this is no" {
		t.Fatal("junk file changed")
	}
}

func TestDSNEscapes(t *testing.T) {
	if got := DSN("/a b/c?d#e%f.db"); got != "file:/a b/c%3fd%23e%25f.db?mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)&_pragma=cache_size(-2048)" {
		t.Fatalf("dsn %q", got)
	}
}
