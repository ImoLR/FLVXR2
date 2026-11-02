// Package oldpanel reads the 救命版 (the old upstream Flux panel that runs next
// to FLVX) strictly read-only, so its machines can appear on the public probe
// without installing the FLVX agent (plan 195).
//
// Read-only by construction: the database is opened with mode=ro plus
// PRAGMA query_only, the package only has constant SELECT statements, and
// every statement is a short autocommit read (rows are read to the end and
// closed at once), so no read transaction is ever held open and the old
// panel's WAL checkpoints are never held back. Nothing here changes the old
// panel's schema, journal mode, indexes or data.
package oldpanel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite" // registers the "sqlite" driver (CGO-free)
)

// Error classes (for the throttled summary log line; never shown publicly).
var (
	ErrMissing = errors.New("old panel db file not found")
	ErrSchema  = errors.New("old panel db schema not as expected")
)

// Node is one 救命版 node as the probe needs it. Addresses are only used to
// match it with an FLVX node; they are never published.
type Node struct {
	ID           int64
	Name         string
	ServerIP     string
	ServerIPV4   string
	ServerIPV6   string
	ExtraIPs     string
	Version      string
	ExpiryTime   int64 // ms, 0 = none
	RenewalCycle string
	Inx          int64
}

// Metric is one node_metric row (the old agent reports every 5 s; the old
// panel writes them in batches).
type Metric struct {
	ID          int64
	NodeID      int64
	At          int64 // ms
	CPU         float64
	Mem         float64
	Disk        float64
	Load1       float64
	NetInBytes  int64
	NetOutBytes int64
	NetInSpeed  int64
	NetOutSpeed int64
	TCP         int64
	Uptime      int64
}

// Bucket is one averaged chart point (same shape as the FLVX probe series).
type Bucket struct {
	At          int64
	CPU         float64
	Mem         float64
	NetInSpeed  float64
	NetOutSpeed float64
	TCP         float64
}

// Reader is safe for concurrent use; the pool has one connection, so reads
// queue behind each other (each is a few ms at most).
type Reader struct {
	path string

	mu sync.Mutex
	db *sql.DB
}

// IdleClose: an idle panel keeps no handle, lock or page cache on the file.
const IdleClose = 60 * time.Second

func New(path string) *Reader {
	return &Reader{path: strings.TrimSpace(path)}
}

// Path is the configured file (admin diagnostics / tests).
func (r *Reader) Path() string { return r.path }

// DSN builds the read-only URI for path (exported for the safety test).
func DSN(path string) string {
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + esc + "?mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)&_pragma=cache_size(-2048)"
}

func (r *Reader) handle() (*sql.DB, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db != nil {
		return r.db, nil
	}
	if r.path == "" {
		return nil, ErrMissing
	}
	// mode=ro never creates a file, but stat first so a missing mount gives a
	// clear error class instead of a driver message.
	if st, err := os.Stat(r.path); err != nil || st.IsDir() {
		return nil, ErrMissing
	}
	db, err := sql.Open("sqlite", DSN(r.path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(IdleClose)
	r.db = db
	return db, nil
}

// Close releases the pool (tests, shutdown).
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.db == nil {
		return nil
	}
	err := r.db.Close()
	r.db = nil
	return err
}

// Classify maps a read error to a short, stable class for the summary log.
func Classify(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrMissing):
		return "missing"
	case errors.Is(err, ErrSchema):
		return "schema"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "no such column"), strings.Contains(msg, "no such table"):
		return "schema"
	case strings.Contains(msg, "busy"), strings.Contains(msg, "locked"):
		return "locked"
	case strings.Contains(msg, "unable to open"), strings.Contains(msg, "cantopen"), strings.Contains(msg, "not a database"):
		return "open"
	}
	return "read"
}

func wrapSchema(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "no such column") || strings.Contains(msg, "no such table") {
		return fmt.Errorf("%w: %v", ErrSchema, err)
	}
	return err
}

const nodesSQL = `SELECT id, name, server_ip, COALESCE(server_ip_v4, ''), COALESCE(server_ip_v6, ''),
	COALESCE(extra_ips, ''), COALESCE(version, ''), COALESCE(expiry_time, 0), COALESCE(renewal_cycle, ''), inx
	FROM node WHERE COALESCE(is_remote, 0) = 0 ORDER BY inx ASC, id ASC LIMIT 4096`

// Nodes lists the local (non-remote) nodes in the old panel's order.
func (r *Reader) Nodes(ctx context.Context) ([]Node, error) {
	db, err := r.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, nodesSQL)
	if err != nil {
		return nil, wrapSchema(err)
	}
	defer rows.Close()
	out := make([]Node, 0, 64)
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Name, &n.ServerIP, &n.ServerIPV4, &n.ServerIPV6, &n.ExtraIPs, &n.Version, &n.ExpiryTime, &n.RenewalCycle, &n.Inx); err != nil {
			return nil, wrapSchema(err)
		}
		out = append(out, n)
	}
	return out, wrapSchema(rows.Err())
}

const metricCols = `id, node_id, timestamp, COALESCE(cpu_usage, 0), COALESCE(mem_usage, 0), COALESCE(disk_usage, 0),
	COALESCE(load1, 0), COALESCE(net_in_bytes, 0), COALESCE(net_out_bytes, 0), COALESCE(net_in_speed, 0),
	COALESCE(net_out_speed, 0), COALESCE(tcp_conns, 0), COALESCE(uptime, 0)`

// MaxMetricRows bounds one poll (≈ 100 machines × 50 rows).
const MaxMetricRows = 5000

// MetricsAfter returns the rows written after the row id cursor (rowid range
// search: only what is new since the last poll).
func (r *Reader) MetricsAfter(ctx context.Context, cursor int64) ([]Metric, error) {
	return r.metrics(ctx, `SELECT `+metricCols+` FROM node_metric WHERE id > ? ORDER BY id ASC LIMIT ?`, cursor, MaxMetricRows)
}

// MetricsSince returns the rows with timestamp > sinceMs (time index): the
// bootstrap after a start or a pause.
func (r *Reader) MetricsSince(ctx context.Context, sinceMs int64) ([]Metric, error) {
	return r.metrics(ctx, `SELECT `+metricCols+` FROM node_metric WHERE timestamp > ? LIMIT ?`, sinceMs, MaxMetricRows)
}

func (r *Reader) metrics(ctx context.Context, q string, arg int64, limit int) ([]Metric, error) {
	db, err := r.handle()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, q, arg, limit)
	if err != nil {
		return nil, wrapSchema(err)
	}
	defer rows.Close()
	out := make([]Metric, 0, 64)
	for rows.Next() {
		var m Metric
		if err := rows.Scan(&m.ID, &m.NodeID, &m.At, &m.CPU, &m.Mem, &m.Disk, &m.Load1, &m.NetInBytes, &m.NetOutBytes, &m.NetInSpeed, &m.NetOutSpeed, &m.TCP, &m.Uptime); err != nil {
			return nil, wrapSchema(err)
		}
		out = append(out, m)
	}
	return out, wrapSchema(rows.Err())
}

// LatestMetricTime is the newest row of one node (ms, 0 = none): a single
// seek on idx_node_metric_node_time. Never the GROUP BY form (full scan).
func (r *Reader) LatestMetricTime(ctx context.Context, nodeID int64) (int64, error) {
	db, err := r.handle()
	if err != nil {
		return 0, err
	}
	var ts sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(timestamp) FROM node_metric WHERE node_id = ?`, nodeID).Scan(&ts); err != nil {
		return 0, wrapSchema(err)
	}
	if !ts.Valid {
		return 0, nil
	}
	return ts.Int64, nil
}

// Buckets averages one node's rows into step-ms buckets (≤ limit points), in
// SQL, using the (node_id, timestamp) index for the range.
func (r *Reader) Buckets(ctx context.Context, nodeID, startMs, endMs, stepMs int64, limit int) ([]Bucket, error) {
	db, err := r.handle()
	if err != nil {
		return nil, err
	}
	if stepMs < 1000 {
		stepMs = 1000
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	q := fmt.Sprintf(`SELECT CAST(timestamp / %d * %d AS INTEGER) AS ts, AVG(COALESCE(cpu_usage, 0)), AVG(COALESCE(mem_usage, 0)),
		AVG(COALESCE(net_in_speed, 0)), AVG(COALESCE(net_out_speed, 0)), AVG(COALESCE(tcp_conns, 0))
		FROM node_metric WHERE node_id = ? AND timestamp >= ? AND timestamp <= ?
		GROUP BY timestamp / %d ORDER BY ts ASC LIMIT ?`, stepMs, stepMs, stepMs)
	rows, err := db.QueryContext(ctx, q, nodeID, startMs, endMs, limit+2)
	if err != nil {
		return nil, wrapSchema(err)
	}
	defer rows.Close()
	out := make([]Bucket, 0, limit)
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.At, &b.CPU, &b.Mem, &b.NetInSpeed, &b.NetOutSpeed, &b.TCP); err != nil {
			return nil, wrapSchema(err)
		}
		out = append(out, b)
	}
	return out, wrapSchema(rows.Err())
}
