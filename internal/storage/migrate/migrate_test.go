package migrate_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/storage/migrate"
	_ "modernc.org/sqlite"
)

func openDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestLoadReturnsOrderedMigrations(t *testing.T) {
	migrations, err := migrate.Load(migrate.DialectSQLite)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("Load() returned no migrations, want at least the initial schema")
	}

	for i, m := range migrations {
		if m.SQL == "" {
			t.Errorf("migration %s has empty SQL", m)
		}
		if i > 0 && m.Version <= migrations[i-1].Version {
			t.Errorf("migration %s is not ordered after %s", m, migrations[i-1])
		}
	}
	if migrations[0].Version != 1 {
		t.Errorf("first migration is version %d, want 1", migrations[0].Version)
	}
}

func TestLoadUnknownDialect(t *testing.T) {
	if _, err := migrate.Load("oracle"); err == nil {
		t.Error("Load(unknown dialect) returned nil error, want a failure")
	}
}

func TestApplyThenIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	applied, err := migrate.Apply(ctx, db, migrate.DialectSQLite)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("Apply() on a fresh database applied nothing")
	}

	version, err := migrate.Current(ctx, db)
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if version != applied[len(applied)-1].Version {
		t.Errorf("Current() = %d, want %d", version, applied[len(applied)-1].Version)
	}

	// Re-running must be a no-op. An install that re-applies its schema on
	// every launch would fail the second time the app opened.
	again, err := migrate.Apply(ctx, db, migrate.DialectSQLite)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second Apply() applied %d migrations, want 0", len(again))
	}

	pending, err := migrate.Pending(ctx, db, migrate.DialectSQLite)
	if err != nil {
		t.Fatalf("Pending() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Pending() = %d migrations after Apply(), want 0", len(pending))
	}
}

func TestCurrentOnFreshDatabase(t *testing.T) {
	version, err := migrate.Current(context.Background(), openDB(t))
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if version != 0 {
		t.Errorf("Current() on a fresh database = %d, want 0", version)
	}
}

// TestSchemaCreatesExpectedTables pins the initial schema. Renaming a table in
// a later migration is fine; silently losing one is not.
func TestSchemaCreatesExpectedTables(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	if _, err := migrate.Apply(ctx, db, migrate.DialectSQLite); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	for _, table := range []string{"products", "locations", "stock_movements", "stock_levels", "schema_migrations"} {
		var name string
		err := db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q is missing after migration: %v", table, err)
		}
	}

	// The default location must exist, since every ledger row references one.
	var locations int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM locations WHERE is_default = 1`).Scan(&locations); err != nil {
		t.Fatalf("counting default locations: %v", err)
	}
	if locations != 1 {
		t.Errorf("found %d default locations, want exactly 1", locations)
	}
}

// TestPricesAreIntegerColumns guards the single most consequential schema
// decision: a REAL price column silently loses fractions of a cent.
func TestPricesAreIntegerColumns(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)

	if _, err := migrate.Apply(ctx, db, migrate.DialectSQLite); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT name, type FROM pragma_table_info('products')`)
	if err != nil {
		t.Fatalf("reading table info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatalf("scanning table info: %v", err)
		}
		if name == "price_minor" {
			found = true
			if typ != "INTEGER" {
				t.Errorf("products.price_minor is %s, want INTEGER", typ)
			}
		}
		if typ == "REAL" {
			t.Errorf("products.%s is REAL; money must never be stored as a float", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating table info: %v", err)
	}
	if !found {
		t.Error("products has no price_minor column")
	}
}
