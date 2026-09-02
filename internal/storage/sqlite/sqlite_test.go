package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
	"github.com/rohankewalramani/inventory-sys/internal/storage/sqlite"
	"github.com/rohankewalramani/inventory-sys/internal/storage/storetest"
)

// newStore opens a migrated database on a fresh temp file. A real file rather
// than :memory: keeps the test honest about WAL, pragmas and pooling.
func newStore(t *testing.T) storage.Store {
	t.Helper()

	store, err := sqlite.Open(context.Background(), sqlite.Options{
		Path: filepath.Join(t.TempDir(), "test.db"),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return store
}

func TestConformance(t *testing.T) {
	storetest.Run(t, newStore)
}

func TestOpenRequiresAPath(t *testing.T) {
	if _, err := sqlite.Open(context.Background(), sqlite.Options{}); err == nil {
		t.Error("Open() with no path returned nil error, want a failure")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")

	first, err := sqlite.Open(context.Background(), sqlite.Options{Path: path})
	if err != nil {
		t.Fatalf("first Open() error = %v", err)
	}
	p := core.Product{SKU: "KEEP-1", Name: "Persisted", Price: core.MustParseMoney("1.00", "USD"), Active: true}
	if err := first.Products().Create(context.Background(), &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Reopening must re-run migrations as a no-op and find the existing data.
	second, err := sqlite.Open(context.Background(), sqlite.Options{Path: path})
	if err != nil {
		t.Fatalf("second Open() error = %v", err)
	}
	defer func() { _ = second.Close() }()

	if _, err := second.Products().GetBySKU(context.Background(), "KEEP-1"); err != nil {
		t.Errorf("data did not survive reopen: %v", err)
	}
}

// TestLedgerIsAppendOnly proves the database itself refuses to rewrite history,
// so an application-layer bug cannot quietly alter an audit trail.
func TestLedgerIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ledger.db")

	store, err := sqlite.Open(ctx, sqlite.Options{Path: path})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	p := core.Product{SKU: "LOCK-1", Name: "Locked", Price: core.MustParseMoney("1.00", "USD"), Active: true}
	if err := store.Products().Create(ctx, &p); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	m := core.StockMovement{
		ProductID: p.ID, LocationID: core.DefaultLocationID,
		QtyDelta: 10, Reason: core.ReasonOpeningBalance,
	}
	if err := store.Movements().Append(ctx, &m); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	raw, err := rawDB(t, path)
	if err != nil {
		t.Fatalf("opening raw connection: %v", err)
	}
	defer func() { _ = raw.Close() }()

	if _, err := raw.ExecContext(ctx, `UPDATE stock_movements SET qty_delta = 999`); err == nil {
		t.Error("UPDATE on stock_movements succeeded, want it rejected")
	}
	if _, err := raw.ExecContext(ctx, `DELETE FROM stock_movements`); err == nil {
		t.Error("DELETE on stock_movements succeeded, want it rejected")
	}

	onHand, err := store.Movements().OnHand(ctx, p.ID, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("OnHand() error = %v", err)
	}
	if onHand != 10 {
		t.Errorf("OnHand() = %d, want the ledger unchanged at 10", onHand)
	}
}

func TestErrorsMapToCoreSentinels(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	_, err := store.Products().Get(ctx, core.NewID())
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get(missing) error = %v, want core.ErrNotFound", err)
	}

	// The message must name the operation, because this string is what lands
	// in a support log.
	if err != nil && !strings.Contains(err.Error(), "get product") {
		t.Errorf("error %q does not name the failing operation", err)
	}
}
