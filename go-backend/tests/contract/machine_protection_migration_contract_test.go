package contract_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
)

var machineColumns = []string{"machine_protect_enabled", "machine_max_total", "machine_tw_buckets", "machine_close_wait_seconds"}

func assertMachineDefaults(t *testing.T, r *repo.Repository, name string) {
	t.Helper()
	var n model.Node
	if err := r.DB().Where("name = ?", name).Take(&n).Error; err != nil {
		t.Fatal(err)
	}
	if !n.MachineProtectEnabled || n.MachineMaxTotal != 1000 || n.MachineTWBuckets != 200 || n.MachineCloseWaitSeconds != 10 {
		t.Fatalf("%s: machine columns %v %d %d %d, want true 1000 200 10", name, n.MachineProtectEnabled, n.MachineMaxTotal, n.MachineTWBuckets, n.MachineCloseWaitSeconds)
	}
	m, err := r.GetNodeMachineProtection(n.ID)
	if err != nil || m != repo.DefaultMachineProtection() {
		t.Fatalf("GetNodeMachineProtection %+v %v", m, err)
	}
}

// An existing fork.62 database (node rows without the new columns) gets the
// columns with defaults on startup; rows inserted without them get defaults too.
func TestMachineProtectionMigrationDefaultsSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fork62.db")
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('old', 's-old', '192.0.2.1', '1000-2000', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, col := range machineColumns {
		if err := r.DB().Exec(`ALTER TABLE node DROP COLUMN ` + col).Error; err != nil {
			t.Fatalf("simulate pre-118 schema: %v", err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = repo.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	assertMachineDefaults(t, r, "old")
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('raw', 's-raw', '192.0.2.2', '1000-2000', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	assertMachineDefaults(t, r, "raw")
	// Explicit false/0 survive (map update, not GORM zero-value skipping).
	var raw model.Node
	_ = r.DB().Where("name = ?", "raw").Take(&raw).Error
	if err := r.SetNodeMachineProtection(raw.ID, repo.MachineProtection{}); err != nil {
		t.Fatal(err)
	}
	if m, _ := r.GetNodeMachineProtection(raw.ID); m != (repo.MachineProtection{}) {
		t.Fatalf("explicit off not stored: %+v", m)
	}
	if err := r.SetNodeMachineProtection(raw.ID, repo.MachineProtection{Enabled: true, MaxTotal: 10}); err == nil {
		t.Fatal("invalid setting stored")
	}
}

func TestMachineProtectionMigrationDefaultsPostgres(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("FLVX_POSTGRES_TEST_DSN"))
	if baseDSN == "" {
		t.Skip("set FLVX_POSTGRES_TEST_DSN to run postgres contract tests")
	}
	schema := "contract_machine_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	adminDB, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		_ = adminDB.Close()
	})
	if _, err := adminDB.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	dsn, err := withSearchPath(baseDSN, schema)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('old', 's-old', '192.0.2.1', '1000-2000', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, col := range machineColumns {
		if err := r.DB().Exec(`ALTER TABLE node DROP COLUMN ` + col).Error; err != nil {
			t.Fatal(err)
		}
	}
	_ = r.Close()
	r, err = repo.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	assertMachineDefaults(t, r, "old")
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('raw', 's-raw', '192.0.2.2', '1000-2000', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	assertMachineDefaults(t, r, "raw")
	var raw model.Node
	_ = r.DB().Where("name = ?", "raw").Take(&raw).Error
	if err := r.SetNodeMachineProtection(raw.ID, repo.MachineProtection{}); err != nil {
		t.Fatal(err)
	}
	if m, _ := r.GetNodeMachineProtection(raw.ID); m != (repo.MachineProtection{}) {
		t.Fatalf("explicit off not stored: %+v", m)
	}
}

// Plan 148: the TIME_WAIT default for NEW nodes went 500 → 200. A database
// created by fork.91 (column default 500) gets the new column default on
// startup; every existing row keeps its stored value (500, 200, a custom one,
// 0 = off), the rest of the node table is unchanged and its indexes survive.
func TestMachineTWDefaultChangeKeepsRowsSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fork91.db")
	r, err := repo.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"stock", "two", "custom", "off"} {
		if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES(?, ?, '192.0.2.1', '1000-2000', 1, 1)`, name, "s-"+strconv.Itoa(i)).Error; err != nil {
			t.Fatal(err)
		}
	}
	// fork.91 schema: the column with DEFAULT 500, rows as stored on prod.
	for _, q := range []string{
		`ALTER TABLE node DROP COLUMN machine_tw_buckets`,
		`ALTER TABLE node ADD COLUMN machine_tw_buckets integer NOT NULL DEFAULT 500`,
		`UPDATE node SET machine_tw_buckets = 200 WHERE name = 'two'`,
		`UPDATE node SET machine_tw_buckets = 1234 WHERE name = 'custom'`,
		`UPDATE node SET machine_tw_buckets = 0, machine_protect_enabled = false WHERE name = 'off'`,
	} {
		if err := r.DB().Exec(q).Error; err != nil {
			t.Fatalf("simulate fork.91 schema: %s: %v", q, err)
		}
	}
	ddl := func(r *repo.Repository) string {
		var sql string
		_ = r.DB().Raw(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'node'`).Scan(&sql).Error
		return sql
	}
	if !strings.Contains(ddl(r), "DEFAULT 500") {
		t.Fatalf("simulated schema lacks DEFAULT 500: %s", ddl(r))
	}
	before := nodeRowsAndIndexes(t, r, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'node' ORDER BY name`)
	_ = r.Close()

	for round := 1; round <= 2; round++ { // second start: nothing left to migrate
		if r, err = repo.Open(path); err != nil {
			t.Fatalf("reopen %d: %v", round, err)
		}
		after := nodeRowsAndIndexes(t, r, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'node' ORDER BY name`)
		if after != before {
			t.Fatalf("round %d: node rows or indexes changed:\nbefore %s\nafter  %s", round, before, after)
		}
		if d := ddl(r); strings.Contains(d, "DEFAULT 500") {
			t.Fatalf("round %d: column default not migrated: %s", round, d)
		}
		_ = r.Close()
	}
	r, _ = repo.Open(path)
	defer r.Close()
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('new', 's-new', '192.0.2.2', '1000-2000', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	assertMachineDefaults(t, r, "new")
}

func TestMachineTWDefaultChangeKeepsRowsPostgres(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("FLVX_POSTGRES_TEST_DSN"))
	if baseDSN == "" {
		t.Skip("set FLVX_POSTGRES_TEST_DSN to run postgres contract tests")
	}
	schema := "contract_twdef_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	adminDB, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`)
		_ = adminDB.Close()
	})
	if _, err := adminDB.Exec(`CREATE SCHEMA "` + schema + `"`); err != nil {
		t.Fatal(err)
	}
	dsn, err := withSearchPath(baseDSN, schema)
	if err != nil {
		t.Fatal(err)
	}
	r, err := repo.OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"stock", "two", "custom", "off"} {
		if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES(?, ?, '192.0.2.1', '1000-2000', 1, 1)`, name, "s-"+strconv.Itoa(i)).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`ALTER TABLE node ALTER COLUMN machine_tw_buckets SET DEFAULT 500`,
		`UPDATE node SET machine_tw_buckets = 500 WHERE name = 'stock'`,
		`UPDATE node SET machine_tw_buckets = 1234 WHERE name = 'custom'`,
		`UPDATE node SET machine_tw_buckets = 0, machine_protect_enabled = false WHERE name = 'off'`,
	} {
		if err := r.DB().Exec(q).Error; err != nil {
			t.Fatalf("simulate fork.91 schema: %s: %v", q, err)
		}
	}
	colDefault := func(r *repo.Repository) string {
		var d string
		_ = r.DB().Raw(`SELECT column_default FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'node' AND column_name = 'machine_tw_buckets'`).Scan(&d).Error
		return d
	}
	if colDefault(r) != "500" {
		t.Fatalf("simulated default %q", colDefault(r))
	}
	idx := `SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND tablename = 'node' ORDER BY indexname`
	before := nodeRowsAndIndexes(t, r, idx)
	_ = r.Close()
	if r, err = repo.OpenPostgres(dsn); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	if after := nodeRowsAndIndexes(t, r, idx); after != before {
		t.Fatalf("node rows or indexes changed:\nbefore %s\nafter  %s", before, after)
	}
	if d := colDefault(r); d != "200" {
		t.Fatalf("column default %q, want 200", d)
	}
	if err := r.DB().Exec(`INSERT INTO node(name, secret, server_ip, port, created_time, status) VALUES('new', 's-new', '192.0.2.2', '1000-2000', 1, 1)`).Error; err != nil {
		t.Fatal(err)
	}
	assertMachineDefaults(t, r, "new")
}

// nodeRowsAndIndexes renders every node row (all columns, by name) and the
// node table's index names, for before/after comparisons.
func nodeRowsAndIndexes(t *testing.T, r *repo.Repository, indexQuery string) string {
	t.Helper()
	var rows []map[string]interface{}
	if err := r.DB().Raw(`SELECT * FROM node ORDER BY id`).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	var indexes []string
	if err := r.DB().Raw(indexQuery).Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, row := range rows {
		keys := make([]string, 0, len(row))
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "%s=%v;", k, row[k])
		}
		b.WriteString("\n")
	}
	return fmt.Sprintf("%d rows\n%sindexes %v", len(rows), b.String(), indexes)
}
