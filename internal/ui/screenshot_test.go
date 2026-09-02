package ui

import (
	"context"
	"image/png"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage/sqlite"
)

// TestRenderScreens writes a PNG of each screen when INVENTORY_SHOT_DIR is
// set. It is a development aid rather than an assertion: reviewing a desktop
// layout means looking at it, and Fyne's headless driver can render one
// without a display attached.
func TestRenderScreens(t *testing.T) {
	outDir := os.Getenv("INVENTORY_SHOT_DIR")
	if outDir == "" {
		t.Skip("set INVENTORY_SHOT_DIR to render screenshots")
	}

	dbPath := os.Getenv("INVENTORY_SHOT_DB")
	if dbPath == "" {
		t.Skip("set INVENTORY_SHOT_DB to the database to render")
	}

	store, err := sqlite.Open(context.Background(), sqlite.Options{Path: dbPath, SkipMigrate: true})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	cfg := config.Default()
	svc := service.NewInventory(store, service.WithDefaultCurrency(core.DefaultCurrency))

	a := newApp(test.NewApp(), svc, cfg, slog.Default())
	defer a.cancel()
	a.run = func(fn func(ctx context.Context)) {
		defer a.endWork()
		fn(context.Background())
	}
	a.onMain = func(fn func()) { fn() }
	a.status.onMain = a.onMain

	window := test.NewWindow(a.buildShell())
	defer window.Close()
	window.Resize(fyne.NewSize(1440, 900))

	shots := []struct {
		name string
		show func()
	}{
		{"dashboard", func() { a.show(viewDashboard) }},
		{"orders", func() { a.show(viewOrders) }},
		{"orders-detail", func() {
			a.show(viewOrders)
			a.views[viewOrders].(*ordersView).table.SelectIndex(0)
		}},
		{"customers", func() {
			a.show(viewCustomers)
			a.views[viewCustomers].(*customersView).table.SelectIndex(0)
		}},
		{"reports-coverage", func() {
			a.show(viewReports)
			a.views[viewReports].(*reportsView).showTab(reportCoverage)
		}},
		{"products", func() { a.show(viewProducts) }},
		{"products-detail", func() {
			a.show(viewProducts)
			// Select through the table so the real selection path runs,
			// including opening the inspector.
			a.views[viewProducts].(*productsView).table.SelectIndex(0)
		}},
		{"products-low-stock", func() {
			a.show(viewProducts)
			products := a.views[viewProducts].(*productsView)
			products.stockSel.SetSelectedIndex(1)
		}},
		{"movements", func() { a.show(viewMovements) }},
		{"reports-valuation", func() {
			a.show(viewReports)
			a.views[viewReports].(*reportsView).showTab(reportValuation)
		}},
		{"reports-reorder", func() {
			a.show(viewReports)
			a.views[viewReports].(*reportsView).showTab(reportReorder)
		}},
		{"reports-abc", func() {
			a.show(viewReports)
			a.views[viewReports].(*reportsView).showTab(reportABC)
		}},
		{"reports-aging", func() {
			a.show(viewReports)
			a.views[viewReports].(*reportsView).showTab(reportAging)
		}},
		{"reports-expiry", func() {
			a.show(viewReports)
			a.views[viewReports].(*reportsView).showTab(reportExpiry)
		}},
		{"settings", func() { a.show(viewSettings) }},
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", outDir, err)
	}

	for _, shot := range shots {
		shot.show()
		window.Content().Refresh()

		file, err := os.Create(filepath.Join(outDir, shot.name+".png"))
		if err != nil {
			t.Fatalf("creating %s: %v", shot.name, err)
		}
		if err := png.Encode(file, window.Canvas().Capture()); err != nil {
			t.Fatalf("encoding %s: %v", shot.name, err)
		}
		if err := file.Close(); err != nil {
			t.Fatalf("closing %s: %v", shot.name, err)
		}
		t.Logf("wrote %s.png", shot.name)
	}
}
