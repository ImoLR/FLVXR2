package contract_test

import (
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"go-backend/internal/store/model"
	"go-backend/internal/store/repo"
	"go-backend/internal/sublink"
)

// Plan 256: user_subscription.routing on PostgreSQL — a database from before
// plan 256 gets the column from AutoMigrate (nullable TEXT, old rows NULL),
// and the 分流 list round-trips.
func TestPostgresSubRoutingContract(t *testing.T) {
	baseDSN := strings.TrimSpace(os.Getenv("FLVX_POSTGRES_TEST_DSN"))
	if baseDSN == "" {
		t.Skip("set FLVX_POSTGRES_TEST_DSN to run postgres contract tests")
	}
	schemaName := "contract_sub_routing_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	adminDB, err := sql.Open("pgx", baseDSN)
	if err != nil {
		t.Fatalf("open postgres admin connection: %v", err)
	}
	t.Cleanup(func() {
		_, _ = adminDB.Exec(`DROP SCHEMA IF EXISTS "` + schemaName + `" CASCADE`)
		_ = adminDB.Close()
	})
	if _, err := adminDB.Exec(`CREATE SCHEMA "` + schemaName + `"`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	dsn, err := withSearchPath(baseDSN, schemaName)
	if err != nil {
		t.Fatal(err)
	}

	// A fork.131 database: the table without the column, one subscription.
	r, err := repo.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := r.DB().Exec(`ALTER TABLE user_subscription DROP COLUMN routing`).Error; err != nil {
		t.Fatalf("drop column: %v", err)
	}
	if err := r.DB().Exec(`INSERT INTO user_subscription (user_id, name, token, items) VALUES (2, 'old', 'OldTokenAAAAAAAAAAAAAAAA', '[{"kind":"external_node","externalId":7}]')`).Error; err != nil {
		t.Fatalf("old row: %v", err)
	}
	_ = r.Close()

	r, err = repo.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	var typ, nullable string
	if err := r.DB().Raw(`SELECT data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? AND table_name = 'user_subscription' AND column_name = 'routing'`, schemaName).Row().Scan(&typ, &nullable); err != nil {
		t.Fatalf("routing column missing after AutoMigrate: %v", err)
	}
	if typ != "text" || nullable != "YES" {
		t.Fatalf("routing column %s nullable=%s", typ, nullable)
	}
	old, err := r.GetSubscriptionByToken("OldTokenAAAAAAAAAAAAAAAA")
	if err != nil || old == nil || old.Routing != nil {
		t.Fatalf("old row: %+v %v", old, err)
	}

	js := sublink.RoutingJSON(sublink.DefaultRouting())
	s := &model.UserSubscription{UserID: 2, Name: "new", Token: "NewTokenAAAAAAAAAAAAAAAA", Format: "auto", RuleMode: "cn_direct", Items: `[{"kind":"external_node","externalId":7}]`, Routing: &js}
	if err := r.CreateSubscription(s, 5); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, _ := r.GetSubscription(s.ID)
	if got == nil || got.Routing == nil || *got.Routing != js {
		t.Fatalf("routing round trip: %+v", got)
	}
	empty := "[]"
	if ok, err := r.UpdateSubscription(old.ID, 2, "old", "auto", "global", old.Items, &empty); err != nil || !ok {
		t.Fatalf("update: %v", err)
	}
	if ok, err := r.UpdateSubscription(s.ID, 2, "new2", "auto", "cn_direct", s.Items, nil); err != nil || !ok {
		t.Fatalf("update without routing: %v", err)
	}
	if got, _ := r.GetSubscription(old.ID); got.Routing == nil || *got.Routing != "[]" {
		t.Fatal("routing [] not stored")
	}
	if got, _ := r.GetSubscription(s.ID); got.Routing == nil || *got.Routing != js || got.Name != "new2" {
		t.Fatal("update without routing changed it")
	}
	if uses, err := r.ExternalSubscriptionUses(2); err != nil || uses[7] != 2 {
		t.Fatalf("subscription uses %v %v", uses, err)
	}
}
