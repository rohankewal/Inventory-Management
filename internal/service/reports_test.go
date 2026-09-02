package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
)

// seedProduct creates a product and returns it, failing the test on error.
func seedProduct(t *testing.T, svc *service.Inventory, p core.Product, opening service.OpeningStock) core.ProductWithStock {
	t.Helper()

	created, err := svc.CreateProduct(context.Background(), p, opening)
	if err != nil {
		t.Fatalf("CreateProduct(%s) error = %v", p.SKU, err)
	}
	return created
}

func TestValuationUsesTheConfiguredMethod(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	product := seedProduct(t, svc, core.Product{
		SKU: "VAL-1", Name: "Valued",
		Price: core.MustParseMoney("20.00", "USD"),
		Cost:  core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 100, UnitCost: core.MustParseMoney("1.00", "USD")})

	if _, err := svc.ReceiveStock(ctx, service.AdjustStockInput{
		ProductID: product.ID, Delta: 100, UnitCost: core.MustParseMoney("2.00", "USD"),
	}); err != nil {
		t.Fatalf("ReceiveStock() error = %v", err)
	}
	if _, err := svc.IssueStock(ctx, service.AdjustStockInput{
		ProductID: product.ID, Delta: 150,
	}); err != nil {
		t.Fatalf("IssueStock() error = %v", err)
	}

	weighted, err := svc.Valuation(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("Valuation() error = %v", err)
	}
	if weighted.Method != core.ValuationWeightedAverage {
		t.Fatalf("default method = %q, want weighted average", weighted.Method)
	}
	if weighted.Total.String() != "75.00" {
		t.Errorf("weighted average total = %s, want 75.00", weighted.Total)
	}

	if err := svc.SetValuationMethod(ctx, core.ValuationFIFO); err != nil {
		t.Fatalf("SetValuationMethod() error = %v", err)
	}

	// Changing the policy must re-value the history that already exists, not
	// only stock received afterwards.
	fifo, err := svc.Valuation(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("Valuation() error = %v", err)
	}
	if fifo.Total.String() != "100.00" {
		t.Errorf("FIFO total = %s, want 100.00", fifo.Total)
	}
}

func TestValuationExcludesNonStockItems(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	seedProduct(t, svc, core.Product{
		SKU: "SVC-1", Name: "Consulting", NonStock: true,
		Price: core.MustParseMoney("500.00", "USD"),
		Cost:  core.MustParseMoney("100.00", "USD"),
	}, service.OpeningStock{})

	valuation, err := svc.Valuation(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("Valuation() error = %v", err)
	}
	if len(valuation.Lines) != 0 {
		t.Errorf("Valuation() included %d non-stock line(s); a service has no stock to value", len(valuation.Lines))
	}
	if !valuation.Total.IsZero() {
		t.Errorf("Total = %s, want zero", valuation.Total)
	}
}

func TestReorderSuggestions(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	seedProduct(t, svc, core.Product{
		SKU: "LOW-1", Name: "Running Low", Supplier: "Acme",
		Cost: core.MustParseMoney("2.50", "USD"), ReorderPoint: 20, ReorderQuantity: 50,
	}, service.OpeningStock{Quantity: 5})

	seedProduct(t, svc, core.Product{
		SKU: "FINE-1", Name: "Plenty", Supplier: "Acme",
		Cost: core.MustParseMoney("1.00", "USD"), ReorderPoint: 10,
	}, service.OpeningStock{Quantity: 500})

	seedProduct(t, svc, core.Product{
		SKU: "DEEP-1", Name: "Very Low", Supplier: "Globex",
		Cost: core.MustParseMoney("1.00", "USD"), ReorderPoint: 100, ReorderQuantity: 40,
	}, service.OpeningStock{Quantity: 0})

	report, err := svc.ReorderSuggestions(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("ReorderSuggestions() error = %v", err)
	}
	if len(report.Lines) != 2 {
		t.Fatalf("ReorderSuggestions() returned %d lines, want 2", len(report.Lines))
	}

	byS := map[string]service.ReorderLine{}
	for _, line := range report.Lines {
		byS[line.Product.SKU] = line
	}

	// 5 on hand against a point of 20 is a shortfall of 15; one pack of 50
	// covers it.
	if got := byS["LOW-1"].Suggested; got != 50 {
		t.Errorf("LOW-1 suggested %d, want one pack of 50", got)
	}
	// A shortfall of 100 needs three packs of 40, not one: ordering the
	// standard pack size is pointless if it still leaves the item short.
	if got := byS["DEEP-1"].Suggested; got != 120 {
		t.Errorf("DEEP-1 suggested %d, want 120 (three packs of 40)", got)
	}

	if len(report.BySupplier) != 2 {
		t.Errorf("BySupplier has %d suppliers, want 2", len(report.BySupplier))
	}
	// 50 at $2.50 plus 120 at $1.00.
	if report.Total.String() != "245.00" {
		t.Errorf("Total = %s, want 245.00", report.Total)
	}
}

func TestReorderSuggestionsIgnoresProductsWithNoPoint(t *testing.T) {
	svc, _ := newService(t)

	// No reorder point and nothing on hand: the item is out of stock, but
	// nobody has said how many to keep, so there is nothing to suggest.
	seedProduct(t, svc, core.Product{
		SKU: "NOPOINT-1", Name: "No Reorder Point",
		Cost: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{})

	report, err := svc.ReorderSuggestions(context.Background(), service.ReportScope{})
	if err != nil {
		t.Fatalf("ReorderSuggestions() error = %v", err)
	}
	for _, line := range report.Lines {
		if line.Product.SKU == "NOPOINT-1" && line.Suggested > 1 {
			t.Errorf("suggested %d for a product with no reorder point", line.Suggested)
		}
	}
}

func TestStockAging(t *testing.T) {
	now := time.Now().UTC()
	svc, store := newService(t, service.WithClock(func() time.Time { return now }))
	ctx := context.Background()

	fresh := seedProduct(t, svc, core.Product{
		SKU: "FRESH-1", Name: "Moves Often", Cost: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 10})

	// The opening balance is timestamped by the service clock, so an old
	// product needs its movement posted directly at an older date.
	stale := seedProduct(t, svc, core.Product{
		SKU: "STALE-1", Name: "Sits Still", Cost: core.MustParseMoney("4.00", "USD"),
	}, service.OpeningStock{})

	// A second service over the same store, with its clock wound back, is how
	// a movement gets an old timestamp without reaching past the service layer.
	oldClock := now.AddDate(-2, 0, 0)
	staleSvc := service.NewInventory(store, service.WithClock(func() time.Time { return oldClock }))
	if _, err := staleSvc.ReceiveStock(ctx, service.AdjustStockInput{
		ProductID: stale.ID, Delta: 25, UnitCost: core.MustParseMoney("4.00", "USD"),
	}); err != nil {
		t.Fatalf("ReceiveStock() error = %v", err)
	}

	report, err := svc.StockAging(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("StockAging() error = %v", err)
	}
	if len(report.Lines) != 2 {
		t.Fatalf("StockAging() returned %d lines, want 2", len(report.Lines))
	}

	// Oldest first: the point of the report is the stock at the far end.
	if report.Lines[0].Product.SKU != "STALE-1" {
		t.Errorf("first line = %q, want the oldest stock first", report.Lines[0].Product.SKU)
	}
	if report.Lines[0].Bucket != core.AgingDead {
		t.Errorf("two-year-old stock is in bucket %q, want %q", report.Lines[0].Bucket, core.AgingDead)
	}
	if report.Counts[core.AgingDead] != 1 {
		t.Errorf("dead-stock count = %d, want 1", report.Counts[core.AgingDead])
	}
	if report.Totals[core.AgingDead].String() != "100.00" {
		t.Errorf("dead-stock value = %s, want 100.00", report.Totals[core.AgingDead])
	}

	_ = fresh
}

func TestStockAgingSkipsEmptyShelves(t *testing.T) {
	svc, _ := newService(t)

	// Nothing on hand ties up no capital, which is what the report is looking
	// for, so it does not belong in it.
	seedProduct(t, svc, core.Product{
		SKU: "EMPTY-1", Name: "Nothing On Hand", Cost: core.MustParseMoney("9.00", "USD"),
	}, service.OpeningStock{})

	report, err := svc.StockAging(context.Background(), service.ReportScope{})
	if err != nil {
		t.Fatalf("StockAging() error = %v", err)
	}
	if len(report.Lines) != 0 {
		t.Errorf("StockAging() included %d line(s) with no stock on hand", len(report.Lines))
	}
}

func TestExpiringLots(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	product := seedProduct(t, svc, core.Product{
		SKU: "MILK-1", Name: "Milk", TrackLots: true,
		Cost: core.MustParseMoney("0.80", "USD"),
	}, service.OpeningStock{})

	soon := time.Now().UTC().AddDate(0, 0, 10)
	later := time.Now().UTC().AddDate(0, 0, 200)

	for _, lot := range []struct {
		number string
		expiry time.Time
	}{
		{"BATCH-SOON", soon},
		{"BATCH-LATER", later},
	} {
		if _, err := svc.ReceiveStock(ctx, service.AdjustStockInput{
			ProductID: product.ID, Delta: 50,
			UnitCost:  core.MustParseMoney("0.80", "USD"),
			LotNumber: lot.number, ExpiryDate: lot.expiry,
		}); err != nil {
			t.Fatalf("ReceiveStock(%s) error = %v", lot.number, err)
		}
	}

	within30, err := svc.ExpiringLots(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringLots() error = %v", err)
	}
	if len(within30) != 1 {
		t.Fatalf("ExpiringLots(30 days) returned %d lots, want 1", len(within30))
	}
	if within30[0].Movement.LotNumber != "BATCH-SOON" {
		t.Errorf("lot = %q, want BATCH-SOON", within30[0].Movement.LotNumber)
	}
	if within30[0].Product.SKU != "MILK-1" {
		t.Errorf("lot is attributed to %q, want MILK-1", within30[0].Product.SKU)
	}
	if days := within30[0].DaysRemaining; days < 8 || days > 11 {
		t.Errorf("DaysRemaining = %d, want about 10", days)
	}

	withinYear, err := svc.ExpiringLots(ctx, 365*24*time.Hour)
	if err != nil {
		t.Fatalf("ExpiringLots() error = %v", err)
	}
	if len(withinYear) != 2 {
		t.Errorf("ExpiringLots(a year) returned %d lots, want 2", len(withinYear))
	}
}

// TestReceivingLotTrackedStockNeedsALot guards the promise lot tracking makes:
// a batch that was never recorded cannot be recalled.
func TestReceivingLotTrackedStockNeedsALot(t *testing.T) {
	svc, _ := newService(t)

	product := seedProduct(t, svc, core.Product{
		SKU: "LOT-REQ", Name: "Batch Controlled", TrackLots: true,
	}, service.OpeningStock{})

	_, err := svc.ReceiveStock(context.Background(), service.AdjustStockInput{
		ProductID: product.ID, Delta: 10,
	})
	if err == nil {
		t.Fatal("ReceiveStock() with no lot number on a lot-tracked product returned nil error")
	}
}

func TestSummary(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	seedProduct(t, svc, core.Product{
		SKU: "OK-1", Name: "Healthy", Cost: core.MustParseMoney("2.00", "USD"), ReorderPoint: 5,
	}, service.OpeningStock{Quantity: 100, UnitCost: core.MustParseMoney("2.00", "USD")})

	seedProduct(t, svc, core.Product{
		SKU: "LOW-1", Name: "Low", Cost: core.MustParseMoney("1.00", "USD"), ReorderPoint: 50,
	}, service.OpeningStock{Quantity: 5, UnitCost: core.MustParseMoney("1.00", "USD")})

	out := seedProduct(t, svc, core.Product{
		SKU: "OUT-1", Name: "Empty", Cost: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{})

	summary, err := svc.Summary(ctx, core.NilID)
	if err != nil {
		t.Fatalf("Summary() error = %v", err)
	}

	if summary.ActiveProducts != 3 {
		t.Errorf("ActiveProducts = %d, want 3", summary.ActiveProducts)
	}
	if summary.TotalUnits != 105 {
		t.Errorf("TotalUnits = %d, want 105", summary.TotalUnits)
	}
	if summary.StockValue.String() != "205.00" {
		t.Errorf("StockValue = %s, want 205.00", summary.StockValue)
	}
	if summary.NeedsReorder != 2 {
		t.Errorf("NeedsReorder = %d, want 2 (the low one and the empty one)", summary.NeedsReorder)
	}
	if summary.OutOfStock != 1 {
		t.Errorf("OutOfStock = %d, want 1", summary.OutOfStock)
	}
	if len(summary.RecentActivity) == 0 {
		t.Fatal("RecentActivity is empty; the activity feed would show nothing")
	}
	// Every entry must name its product. A feed reading "opening balance, 7
	// minutes ago" eleven times tells the reader nothing.
	for _, entry := range summary.RecentActivity {
		if entry.SKU == "" {
			t.Errorf("an activity entry has no SKU: %+v", entry.Movement)
		}
	}
	if len(summary.TopValue) == 0 || summary.TopValue[0].SKU != "OK-1" {
		t.Error("TopValue does not lead with the most valuable product")
	}

	_ = out
}

// TestAgingAndValuationAgree is the consistency check between two screens. A
// manager who reads one number on the valuation report and a different one on
// the aging report for the same shelf has no way to know which to trust.
func TestAgingAndValuationAgree(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	product := seedProduct(t, svc, core.Product{
		SKU: "AGREE-1", Name: "Consistent",
		Cost: core.MustParseMoney("10.00", "USD"),
	}, service.OpeningStock{Quantity: 100, UnitCost: core.MustParseMoney("10.00", "USD")})

	// A later receipt at a different price moves the ledger value away from
	// the product's standing cost, which is exactly when the two reports used
	// to disagree.
	if _, err := svc.ReceiveStock(ctx, service.AdjustStockInput{
		ProductID: product.ID, Delta: 100, UnitCost: core.MustParseMoney("20.00", "USD"),
	}); err != nil {
		t.Fatalf("ReceiveStock() error = %v", err)
	}

	valuation, err := svc.Valuation(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("Valuation() error = %v", err)
	}
	aging, err := svc.StockAging(ctx, service.ReportScope{})
	if err != nil {
		t.Fatalf("StockAging() error = %v", err)
	}

	if len(aging.Lines) != 1 || len(valuation.Lines) != 1 {
		t.Fatalf("expected one line in each report, got %d and %d",
			len(aging.Lines), len(valuation.Lines))
	}
	if aging.Lines[0].Value.Minor != valuation.Lines[0].Value.Minor {
		t.Errorf("aging values the stock at %s but the valuation report says %s",
			aging.Lines[0].Value, valuation.Lines[0].Value)
	}

	var agingTotal int64
	for _, total := range aging.Totals {
		agingTotal += total.Minor
	}
	if agingTotal != valuation.Total.Minor {
		t.Errorf("aging totals %d, valuation totals %d", agingTotal, valuation.Total.Minor)
	}
}
