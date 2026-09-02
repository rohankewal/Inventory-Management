package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
	"github.com/rohankewalramani/inventory-sys/internal/storage/sqlite"
)

// newTestService gives the UI tests a real database, so what they assert about
// the catalogue is what the application would actually show.
func newTestService(t *testing.T) *service.Inventory {
	t.Helper()

	store, err := sqlite.Open(context.Background(), sqlite.Options{
		Path: filepath.Join(t.TempDir(), "ui.db"),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return service.NewInventory(store)
}

// TestProductColumnsRender exercises every column's renderer against products
// in each interesting state. The columns are where a nil dereference or a
// division by zero would take down the whole grid rather than one cell.
func TestProductColumnsRender(t *testing.T) {
	columns := productColumns(core.DefaultCurrency)

	// The zero value stands in for a row that arrived incomplete. It must not
	// panic, but it is exempt from the "renders something" rule: validation
	// guarantees a real product has a SKU and a name.
	incomplete := map[string]bool{"empty product": true}

	rows := map[string]core.ProductWithStock{
		"healthy": {
			Product: core.Product{
				SKU: "OK-1", Name: "Healthy", Unit: core.UnitEach,
				Price:        core.MustParseMoney("10.00", "USD"),
				Cost:         core.MustParseMoney("4.00", "USD"),
				ReorderPoint: 5,
			},
			OnHand:         50,
			LastMovementAt: time.Now().Add(-2 * time.Hour),
		},
		"empty product": {},
		"negative stock": {
			Product: core.Product{SKU: "NEG-1", Name: "Negative", Unit: core.UnitEach},
			OnHand:  -4,
		},
		"non-stock": {
			Product: core.Product{SKU: "SVC-1", Name: "Service", NonStock: true},
		},
	}

	for name, row := range rows {
		t.Run(name, func(t *testing.T) {
			for _, col := range columns {
				if col.Value == nil {
					t.Fatalf("column %q has no value function", col.Title)
				}
				if got := col.Value(row); got == "" && !incomplete[name] {
					t.Errorf("column %q rendered an empty string; use a dash for no value", col.Title)
				}
				if col.Importance != nil {
					col.Importance(row)
				}
				if col.Bold != nil {
					col.Bold(row)
				}
			}
		})
	}
}

// TestProductColumnSortKeysAreValid guards the wiring between a clickable
// header and the query it triggers. A typo here would silently fall back to
// the default sort rather than failing.
func TestProductColumnSortKeysAreValid(t *testing.T) {
	for _, col := range productColumns(core.DefaultCurrency) {
		if col.SortKey == "" {
			continue
		}
		if !storage.ProductSort(col.SortKey).Valid() {
			t.Errorf("column %q has sort key %q, which the storage layer does not support",
				col.Title, col.SortKey)
		}
	}
}

func TestStockFilterOptionsCoverEveryState(t *testing.T) {
	seen := map[storage.StockState]bool{}
	for _, option := range stockFilterOptions {
		if option.label == "" {
			t.Error("a stock filter option has no label")
		}
		if seen[option.state] {
			t.Errorf("stock state %q is offered twice", option.state)
		}
		seen[option.state] = true
	}

	for _, state := range []storage.StockState{
		storage.StockAny, storage.StockNeedsReorder,
		storage.StockOut, storage.StockInStock, storage.StockNegative,
	} {
		if !seen[state] {
			t.Errorf("stock state %q has no filter option", state)
		}
	}
}

// TestCatalogueQueryEndToEnd runs the catalogue's own filters against a real
// database, which is the part that would break if a filter or sort key were
// wired up wrongly.
func TestCatalogueQueryEndToEnd(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	seed := []struct {
		sku, name, category string
		reorderPoint, qty   int64
	}{
		{"CBL-100", "Cable Tie 100pk", "Consumables", 50, 340},
		{"ANV-001", "Cast Iron Anvil", "Tools", 5, 3},
		{"HMR-002", "Claw Hammer", "Tools", 0, 0},
	}
	for _, s := range seed {
		if _, err := svc.CreateProduct(ctx, core.Product{
			SKU: s.sku, Name: s.name, Category: s.category,
			Price:        core.MustParseMoney("10.00", "USD"),
			Cost:         core.MustParseMoney("4.00", "USD"),
			ReorderPoint: s.reorderPoint,
		}, service.OpeningStock{Quantity: s.qty}); err != nil {
			t.Fatalf("CreateProduct(%s) error = %v", s.sku, err)
		}
	}

	t.Run("default order is by SKU", func(t *testing.T) {
		page, err := svc.ListProducts(ctx, storage.ProductFilter{
			Sort: storage.SortProductSKU, Limit: pageSize,
		})
		if err != nil {
			t.Fatalf("ListProducts() error = %v", err)
		}
		want := []string{"ANV-001", "CBL-100", "HMR-002"}
		for i, sku := range want {
			if page.Items[i].SKU != sku {
				t.Errorf("row %d = %q, want %q", i, page.Items[i].SKU, sku)
			}
		}
	})

	t.Run("last movement date reaches the list", func(t *testing.T) {
		page, err := svc.ListProducts(ctx, storage.ProductFilter{Sort: storage.SortProductSKU})
		if err != nil {
			t.Fatalf("ListProducts() error = %v", err)
		}
		for _, item := range page.Items {
			if item.OnHand > 0 && item.LastMovementAt.IsZero() {
				t.Errorf("%s has stock but no last-movement date; the aging column would read wrong", item.SKU)
			}
		}
	})

	t.Run("each stock filter returns what its label promises", func(t *testing.T) {
		cases := []struct {
			state storage.StockState
			want  []string
		}{
			{storage.StockNeedsReorder, []string{"ANV-001", "HMR-002"}},
			{storage.StockOut, []string{"HMR-002"}},
			{storage.StockInStock, []string{"ANV-001", "CBL-100"}},
			{storage.StockNegative, nil},
		}
		for _, tc := range cases {
			page, err := svc.ListProducts(ctx, storage.ProductFilter{
				Stock: tc.state, Sort: storage.SortProductSKU,
			})
			if err != nil {
				t.Fatalf("ListProducts(%s) error = %v", tc.state, err)
			}
			got := make([]string, len(page.Items))
			for i, item := range page.Items {
				got[i] = item.SKU
			}
			if len(got) != len(tc.want) {
				t.Errorf("filter %q returned %v, want %v", tc.state, got, tc.want)
				continue
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("filter %q returned %v, want %v", tc.state, got, tc.want)
					break
				}
			}
		}
	})

	t.Run("archived products leave the catalogue", func(t *testing.T) {
		archived, err := svc.GetProductBySKU(ctx, "CBL-100", core.NilID)
		if err != nil {
			t.Fatalf("GetProductBySKU() error = %v", err)
		}
		if _, err := svc.DeleteProduct(ctx, archived.ID, core.NilID); err != nil {
			t.Fatalf("DeleteProduct() error = %v", err)
		}

		page, err := svc.ListProducts(ctx, storage.ProductFilter{})
		if err != nil {
			t.Fatalf("ListProducts() error = %v", err)
		}
		for _, row := range page.Items {
			if row.SKU == "CBL-100" {
				t.Error("an archived product is still listed in the catalogue")
			}
		}
	})
}
