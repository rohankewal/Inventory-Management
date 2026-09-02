package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
	"github.com/rohankewalramani/inventory-sys/internal/storage/sqlite"
)

func newService(t *testing.T, opts ...service.Option) (*service.Inventory, storage.Store) {
	t.Helper()

	store, err := sqlite.Open(context.Background(), sqlite.Options{
		Path: filepath.Join(t.TempDir(), "service.db"),
	})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return service.NewInventory(store, opts...), store
}

func TestCreateProductPostsOpeningBalance(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	got, err := svc.CreateProduct(ctx, core.Product{
		SKU: "OPEN-1", Name: "Opening Item", Price: core.MustParseMoney("4.99", "USD"),
	}, service.OpeningStock{Quantity: 25})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}
	if got.OnHand != 25 {
		t.Errorf("OnHand = %d, want 25", got.OnHand)
	}

	// The opening quantity must be a ledger entry, not an assigned number, so
	// that where the stock came from is answerable a year later.
	history, err := svc.MovementHistory(ctx, storage.MovementFilter{ProductID: got.ID})
	if err != nil {
		t.Fatalf("MovementHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("MovementHistory() returned %d entries, want 1", len(history))
	}
	if history[0].Reason != core.ReasonOpeningBalance || history[0].QtyDelta != 25 {
		t.Errorf("opening entry = %s %d, want opening_balance 25",
			history[0].Reason, history[0].QtyDelta)
	}
}

func TestCreateProductWithoutStockPostsNothing(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	got, err := svc.CreateProduct(ctx, core.Product{
		SKU: "EMPTY-1", Name: "No Stock Yet", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	history, err := svc.MovementHistory(ctx, storage.MovementFilter{ProductID: got.ID})
	if err != nil {
		t.Fatalf("MovementHistory() error = %v", err)
	}
	if len(history) != 0 {
		t.Errorf("MovementHistory() returned %d entries, want none for a zero opening balance", len(history))
	}
}

func TestCreateProductRejectsNegativeOpeningStock(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	_, err := svc.CreateProduct(ctx, core.Product{
		SKU: "NEG-1", Name: "Negative", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: -5})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("CreateProduct() error = %v, want core.ErrInvalid", err)
	}

	// The rejection must leave nothing behind.
	if _, err := store.Products().GetBySKU(ctx, "NEG-1"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("a rejected create left a product behind: err = %v", err)
	}
}

func TestCreateProductIsAtomic(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	// A location that does not exist makes the ledger write fail after the
	// product insert has already succeeded. Both must roll back together.
	_, err := svc.CreateProduct(ctx, core.Product{
		SKU: "ATOMIC-1", Name: "Atomic", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 10, LocationID: core.NewID()})
	if err == nil {
		t.Fatal("CreateProduct() with an unknown location returned nil error")
	}

	if _, err := store.Products().GetBySKU(ctx, "ATOMIC-1"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("product survived a failed opening-balance write: err = %v", err)
	}
}

func TestAdjustStockBlocksNegativeOnHand(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "ADJ-1", Name: "Adjusted", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 10})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	// Selling 15 of the 10 you have is a data-entry mistake far more often
	// than it is a real event.
	_, err = svc.AdjustStock(ctx, service.AdjustStockInput{
		ProductID: p.ID, Delta: -15, Reason: core.ReasonAdjustment,
	})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("AdjustStock() below zero error = %v, want core.ErrInvalid", err)
	}

	after, err := svc.GetProduct(ctx, p.ID, core.NilID)
	if err != nil {
		t.Fatalf("GetProduct() error = %v", err)
	}
	if after.OnHand != 10 {
		t.Errorf("OnHand = %d, want the rejected adjustment to have changed nothing", after.OnHand)
	}
}

func TestAdjustStockAllowsNegativeWhenConfigured(t *testing.T) {
	svc, _ := newService(t, service.AllowNegativeStock(true))
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "BACK-1", Name: "Backorder", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 2})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	onHand, err := svc.AdjustStock(ctx, service.AdjustStockInput{
		ProductID: p.ID, Delta: -5, Reason: core.ReasonAdjustment,
	})
	if err != nil {
		t.Fatalf("AdjustStock() error = %v", err)
	}
	if onHand != -3 {
		t.Errorf("OnHand = %d, want -3", onHand)
	}
}

func TestAdjustStockRejectsSystemReasons(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "SYS-1", Name: "System Reason", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	// A receipt is posted by receiving a purchase order. Letting someone type
	// one by hand would make the ledger stop reconciling with the documents.
	for _, reason := range []core.MovementReason{core.ReasonReceipt, core.ReasonSale, core.ReasonTransferIn} {
		_, err := svc.AdjustStock(ctx, service.AdjustStockInput{
			ProductID: p.ID, Delta: 5, Reason: reason,
		})
		if !errors.Is(err, core.ErrInvalid) {
			t.Errorf("AdjustStock(%s) error = %v, want core.ErrInvalid", reason, err)
		}
	}
}

func TestSetStockPostsTheVariance(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "COUNT-1", Name: "Counted", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 100})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	onHand, err := svc.SetStock(ctx, service.SetStockInput{
		ProductID: p.ID, Counted: 97, Note: "annual count",
	})
	if err != nil {
		t.Fatalf("SetStock() error = %v", err)
	}
	if onHand != 97 {
		t.Errorf("SetStock() = %d, want 97", onHand)
	}

	// The three missing units must be visible as a variance, not absorbed by
	// overwriting the level.
	history, err := svc.MovementHistory(ctx, storage.MovementFilter{
		ProductID: p.ID, Reason: core.ReasonStockCount,
	})
	if err != nil {
		t.Fatalf("MovementHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("found %d stock_count entries, want 1", len(history))
	}
	if history[0].QtyDelta != -3 {
		t.Errorf("variance = %d, want -3", history[0].QtyDelta)
	}
}

func TestSetStockOnMatchPostsNothing(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "MATCH-1", Name: "Matches", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 40})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	if _, err := svc.SetStock(ctx, service.SetStockInput{ProductID: p.ID, Counted: 40}); err != nil {
		t.Fatalf("SetStock() error = %v", err)
	}

	// A count that agrees is not an event worth recording.
	history, err := svc.MovementHistory(ctx, storage.MovementFilter{
		ProductID: p.ID, Reason: core.ReasonStockCount,
	})
	if err != nil {
		t.Fatalf("MovementHistory() error = %v", err)
	}
	if len(history) != 0 {
		t.Errorf("found %d stock_count entries for a matching count, want 0", len(history))
	}
}

func TestDeleteProductArchivesWhenItHasHistory(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	stocked, err := svc.CreateProduct(ctx, core.Product{
		SKU: "ARCH-1", Name: "Has History", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 5})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	outcome, err := svc.DeleteProduct(ctx, stocked.ID, core.NilID)
	if err != nil {
		t.Fatalf("DeleteProduct() error = %v", err)
	}
	if outcome != service.OutcomeArchived {
		t.Errorf("outcome = %q, want %q", outcome, service.OutcomeArchived)
	}

	// The record and its ledger must both survive, just out of sight.
	got, err := svc.GetProduct(ctx, stocked.ID, core.NilID)
	if err != nil {
		t.Fatalf("GetProduct() after archive error = %v", err)
	}
	if got.Active {
		t.Error("archived product is still active")
	}
	history, err := svc.MovementHistory(ctx, storage.MovementFilter{ProductID: stocked.ID})
	if err != nil {
		t.Fatalf("MovementHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Errorf("archiving lost %d ledger entries", 1-len(history))
	}
}

func TestDeleteProductRemovesWhenItHasNoHistory(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	clean, err := svc.CreateProduct(ctx, core.Product{
		SKU: "GONE-1", Name: "Never Stocked", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	outcome, err := svc.DeleteProduct(ctx, clean.ID, core.NilID)
	if err != nil {
		t.Fatalf("DeleteProduct() error = %v", err)
	}
	if outcome != service.OutcomeDeleted {
		t.Errorf("outcome = %q, want %q", outcome, service.OutcomeDeleted)
	}
	if _, err := svc.GetProduct(ctx, clean.ID, core.NilID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("GetProduct() after delete error = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteMissingProduct(t *testing.T) {
	svc, _ := newService(t)

	if _, err := svc.DeleteProduct(context.Background(), core.NewID(), core.NilID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("DeleteProduct(missing) error = %v, want core.ErrNotFound", err)
	}
}

func TestUpdateProductDetectsConcurrentEdit(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "EDIT-1", Name: "Original", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}

	base := core.Product{
		ID: p.ID, Version: p.Version, SKU: p.SKU, Active: true,
		Price: core.MustParseMoney("2.00", "USD"),
	}

	first := base
	first.Name = "First Edit"
	if _, err := svc.UpdateProduct(ctx, first); err != nil {
		t.Fatalf("first UpdateProduct() error = %v", err)
	}

	second := base // still carrying the stale version
	second.Name = "Second Edit"
	if _, err := svc.UpdateProduct(ctx, second); !errors.Is(err, core.ErrConflict) {
		t.Errorf("stale UpdateProduct() error = %v, want core.ErrConflict", err)
	}
}

func TestListProductsReportsUnpagedTotal(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	for _, sku := range []string{"L-1", "L-2", "L-3", "L-4"} {
		if _, err := svc.CreateProduct(ctx, core.Product{
			SKU: sku, Name: "Item " + sku, Price: core.MustParseMoney("1.00", "USD"),
		}, service.OpeningStock{}); err != nil {
			t.Fatalf("CreateProduct(%s) error = %v", sku, err)
		}
	}

	page, err := svc.ListProducts(ctx, storage.ProductFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListProducts() error = %v", err)
	}
	if len(page.Items) != 2 {
		t.Errorf("Items = %d, want the page size of 2", len(page.Items))
	}
	if page.Total != 4 {
		t.Errorf("Total = %d, want the unpaged count of 4", page.Total)
	}
}

func TestVerifyStockLevelsMatchesLedger(t *testing.T) {
	svc, store := newService(t)
	ctx := context.Background()

	p, err := svc.CreateProduct(ctx, core.Product{
		SKU: "VERIFY-1", Name: "Verified", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 60})
	if err != nil {
		t.Fatalf("CreateProduct() error = %v", err)
	}
	if _, err := svc.AdjustStock(ctx, service.AdjustStockInput{
		ProductID: p.ID, Delta: -11, Reason: core.ReasonWriteOff,
	}); err != nil {
		t.Fatalf("AdjustStock() error = %v", err)
	}

	if err := svc.VerifyStockLevels(ctx, p.ID); err != nil {
		t.Fatalf("VerifyStockLevels() error = %v", err)
	}

	onHand, err := store.Movements().OnHand(ctx, p.ID, core.DefaultLocationID)
	if err != nil {
		t.Fatalf("OnHand() error = %v", err)
	}
	if onHand != 49 {
		t.Errorf("OnHand() after rebuild = %d, want 49", onHand)
	}
}
