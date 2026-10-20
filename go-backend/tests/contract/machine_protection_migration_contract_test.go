package contract_test

import (
	"database/sql"
	"os"
	"path/filepath"
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
	if !n.MachineProtectEnabled || n.MachineMaxTotal != 1500 || n.MachineTWBuckets != 500 || n.MachineCloseWaitSeconds != 10 {
		t.Fatalf("%s: machine columns %v %d %d %d, want true 1500 500 10", name, n.MachineProtectEnabled, n.MachineMaxTotal, n.MachineTWBuckets, n.MachineCloseWaitSeconds)
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
