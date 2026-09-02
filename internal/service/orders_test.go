package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
)

// clientFixture is a customer with two stores and a stocked product, which is
// the smallest arrangement most order tests need.
type clientFixture struct {
	svc      *service.Inventory
	customer core.Customer
	storeA   core.CustomerStore
	storeB   core.CustomerStore
	product  core.ProductWithStock
}

func newClientFixture(t *testing.T, opening int64) clientFixture {
	t.Helper()
	ctx := context.Background()

	svc, _ := newService(t)

	customer, err := svc.CreateCustomer(ctx, core.Customer{
		Code: "MACYS", Name: "Macy's", Currency: "USD", Terms: "Net 60",
	})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}

	newStore := func(code, name string) core.CustomerStore {
		store, err := svc.CreateStore(ctx, core.CustomerStore{
			CustomerID: customer.ID, Code: code, Name: name,
			ShipTo: core.Address{Line1: "1 Example Way", City: "New York", Country: "USA"},
		})
		if err != nil {
			t.Fatalf("CreateStore(%s) error = %v", code, err)
		}
		return store
	}

	product := seedProduct(t, svc, core.Product{
		SKU: "THROW-1", Name: "Sherpa Throw",
		Price: core.MustParseMoney("24.99", "USD"),
		Cost:  core.MustParseMoney("11.50", "USD"),
	}, service.OpeningStock{Quantity: opening, UnitCost: core.MustParseMoney("11.50", "USD")})

	return clientFixture{
		svc: svc, customer: customer,
		storeA:  newStore("0047", "Herald Square"),
		storeB:  newStore("0100", "Roosevelt Field"),
		product: product,
	}
}

func (f clientFixture) order(t *testing.T, store core.CustomerStore, po string, qty int64) core.StoreOrder {
	t.Helper()

	order, err := f.svc.SaveOrder(context.Background(), core.StoreOrder{
		CustomerID: f.customer.ID, StoreID: store.ID,
		CustomerPONumber:  po,
		Status:            core.OrderConfirmed,
		RequestedShipDate: time.Now().UTC().AddDate(0, 0, 21),
		CancelAfterDate:   time.Now().UTC().AddDate(0, 0, 35),
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: qty, UnitPrice: core.MustParseMoney("12.50", "USD")},
	})
	if err != nil {
		t.Fatalf("SaveOrder(%s) error = %v", po, err)
	}
	return order
}

func TestSaveOrderCreatesAndUpdates(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	order := f.order(t, f.storeA, "MCY-0123", 240)

	detail, err := f.svc.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("GetOrder() error = %v", err)
	}
	if detail.Totals.Units != 240 {
		t.Errorf("Units = %d, want 240", detail.Totals.Units)
	}
	if detail.Store.Code != "0047" {
		t.Errorf("Store = %q, want 0047", detail.Store.Code)
	}

	// Editing replaces the line set.
	updated := detail.StoreOrder
	_, err = f.svc.SaveOrder(ctx, updated, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 300, UnitPrice: core.MustParseMoney("12.00", "USD")},
	})
	if err != nil {
		t.Fatalf("SaveOrder(update) error = %v", err)
	}

	after, err := f.svc.GetOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("GetOrder() error = %v", err)
	}
	if after.Totals.Units != 300 || after.Totals.Value.String() != "3600.00" {
		t.Errorf("after edit = %d units worth %s, want 300 and 3600.00",
			after.Totals.Units, after.Totals.Value)
	}
}

// TestSaveOrderRejectsAStoreFromAnotherCustomer guards the mistake that sends
// goods to the wrong company. Both references are individually valid, so
// nothing below the service layer can catch it.
func TestSaveOrderRejectsAStoreFromAnotherCustomer(t *testing.T) {
	f := newClientFixture(t, 100)
	ctx := context.Background()

	other, err := f.svc.CreateCustomer(ctx, core.Customer{Code: "KOHLS", Name: "Kohl's"})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}

	_, err = f.svc.SaveOrder(ctx, core.StoreOrder{
		CustomerID:       other.ID,
		StoreID:          f.storeA.ID, // belongs to Macy's
		CustomerPONumber: "KHL-0001",
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 10, UnitPrice: core.MustParseMoney("10.00", "USD")},
	})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("SaveOrder() with another customer's store error = %v, want core.ErrInvalid", err)
	}
}

func TestSaveOrderRejectsAProgramFromAnotherCustomer(t *testing.T) {
	f := newClientFixture(t, 100)
	ctx := context.Background()

	other, err := f.svc.CreateCustomer(ctx, core.Customer{Code: "KOHLS", Name: "Kohl's"})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}
	foreign, err := f.svc.CreateProgram(ctx, core.Program{
		CustomerID: other.ID, Code: "KHL-FW26", Name: "Kohl's FW26",
	})
	if err != nil {
		t.Fatalf("CreateProgram() error = %v", err)
	}

	_, err = f.svc.SaveOrder(ctx, core.StoreOrder{
		CustomerID: f.customer.ID, StoreID: f.storeA.ID, ProgramID: foreign.ID,
		CustomerPONumber: "MCY-XPROG",
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 10, UnitPrice: core.MustParseMoney("10.00", "USD")},
	})
	if !errors.Is(err, core.ErrInvalid) {
		t.Errorf("SaveOrder() with another customer's program error = %v, want core.ErrInvalid", err)
	}
}

// TestFulfilmentStatusesCannotBeSetByHand keeps the screen honest: an order
// must not claim to have shipped when nothing left the building.
func TestFulfilmentStatusesCannotBeSetByHand(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	order := f.order(t, f.storeA, "MCY-0123", 240)

	for _, status := range []core.OrderStatus{core.OrderShipped, core.OrderPartial} {
		if err := f.svc.SetOrderStatus(ctx, order.ID, status); !errors.Is(err, core.ErrInvalid) {
			t.Errorf("SetOrderStatus(%s) error = %v, want core.ErrInvalid", status, err)
		}
	}

	// The statuses a person genuinely decides are allowed.
	if err := f.svc.ConfirmOrder(ctx, order.ID); err != nil {
		t.Errorf("ConfirmOrder() error = %v", err)
	}
	if err := f.svc.CancelOrder(ctx, order.ID); err != nil {
		t.Errorf("CancelOrder() error = %v", err)
	}
}

func TestCancelOrderRefusesOnceShipped(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	order, err := f.svc.SaveOrder(ctx, core.StoreOrder{
		CustomerID: f.customer.ID, StoreID: f.storeA.ID,
		CustomerPONumber: "MCY-SHIPPED", Status: core.OrderConfirmed,
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 100, ShippedQty: 40,
			UnitPrice: core.MustParseMoney("12.50", "USD")},
	})
	if err != nil {
		t.Fatalf("SaveOrder() error = %v", err)
	}

	if err := f.svc.CancelOrder(ctx, order.ID); !errors.Is(err, core.ErrConflict) {
		t.Errorf("CancelOrder() on a part-shipped order error = %v, want core.ErrConflict", err)
	}
	if err := f.svc.DeleteOrder(ctx, order.ID); !errors.Is(err, core.ErrConflict) {
		t.Errorf("DeleteOrder() on a part-shipped order error = %v, want core.ErrConflict", err)
	}
}

// TestLookupResolvesAPONumberFirst is the behaviour the search box exists for:
// a PO number is what a client says on the phone.
func TestLookupResolvesAPONumberFirst(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	f.order(t, f.storeA, "MCY-0123", 240)

	found, err := f.svc.Lookup(ctx, "mcy-0123", core.NilID)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found.Orders) != 1 {
		t.Fatalf("Lookup() returned %d orders, want 1", len(found.Orders))
	}
	if found.Orders[0].StoreCode != "0047" {
		t.Errorf("Lookup() resolved store %q, want 0047", found.Orders[0].StoreCode)
	}
	if found.Product != nil {
		t.Error("Lookup() returned a product for a PO number")
	}

	// A SKU still resolves to the product.
	bySKU, err := f.svc.Lookup(ctx, "THROW-1", core.NilID)
	if err != nil {
		t.Fatalf("Lookup(sku) error = %v", err)
	}
	if bySKU.Product == nil || bySKU.Product.SKU != "THROW-1" {
		t.Errorf("Lookup(sku) = %+v, want the product", bySKU)
	}

	if _, err := f.svc.Lookup(ctx, "NOTHING", core.NilID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Lookup(unknown) error = %v, want core.ErrNotFound", err)
	}
}

// TestLookupFindsThePOOfEitherCustomer covers two clients using the same
// number, which is normal because they number their own paperwork.
func TestLookupFindsThePOOfEitherCustomer(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	other, err := f.svc.CreateCustomer(ctx, core.Customer{Code: "KOHLS", Name: "Kohl's"})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}
	otherStore, err := f.svc.CreateStore(ctx, core.CustomerStore{
		CustomerID: other.ID, Code: "0047", Name: "Menomonee Falls",
	})
	if err != nil {
		t.Fatalf("CreateStore() error = %v", err)
	}

	f.order(t, f.storeA, "PO-777", 100)
	if _, err := f.svc.SaveOrder(ctx, core.StoreOrder{
		CustomerID: other.ID, StoreID: otherStore.ID,
		CustomerPONumber: "PO-777", Status: core.OrderConfirmed,
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 50, UnitPrice: core.MustParseMoney("12.50", "USD")},
	}); err != nil {
		t.Fatalf("SaveOrder() error = %v", err)
	}

	found, err := f.svc.Lookup(ctx, "PO-777", core.NilID)
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}
	if len(found.Orders) != 2 {
		t.Errorf("Lookup() returned %d orders, want both customers'", len(found.Orders))
	}
}

func TestDeleteCustomerArchivesWhenItHasOrders(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	f.order(t, f.storeA, "MCY-0123", 240)

	outcome, err := f.svc.DeleteCustomer(ctx, f.customer.ID)
	if err != nil {
		t.Fatalf("DeleteCustomer() error = %v", err)
	}
	if outcome != service.OutcomeArchived {
		t.Errorf("outcome = %q, want %q", outcome, service.OutcomeArchived)
	}

	// The record and its orders must both survive.
	got, err := f.svc.GetCustomer(ctx, f.customer.ID)
	if err != nil {
		t.Fatalf("GetCustomer() error = %v", err)
	}
	if got.Active {
		t.Error("an archived customer is still active")
	}

	clean, err := f.svc.CreateCustomer(ctx, core.Customer{Code: "NEW", Name: "No Orders"})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}
	outcome, err = f.svc.DeleteCustomer(ctx, clean.ID)
	if err != nil {
		t.Fatalf("DeleteCustomer() error = %v", err)
	}
	if outcome != service.OutcomeDeleted {
		t.Errorf("outcome = %q, want %q", outcome, service.OutcomeDeleted)
	}
}

func TestOrderBookSummary(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	f.order(t, f.storeA, "MCY-0001", 100)
	f.order(t, f.storeB, "MCY-0002", 50)

	// One that is past the date the client will refuse it.
	if _, err := f.svc.SaveOrder(ctx, core.StoreOrder{
		CustomerID: f.customer.ID, StoreID: f.storeA.ID,
		CustomerPONumber: "MCY-LATE", Status: core.OrderConfirmed,
		RequestedShipDate: time.Now().UTC().AddDate(0, 0, -20),
		CancelAfterDate:   time.Now().UTC().AddDate(0, 0, -5),
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 25, UnitPrice: core.MustParseMoney("12.50", "USD")},
	}); err != nil {
		t.Fatalf("SaveOrder() error = %v", err)
	}

	book, err := f.svc.OrderBookSummary(ctx)
	if err != nil {
		t.Fatalf("OrderBookSummary() error = %v", err)
	}
	if book.OpenOrders != 3 {
		t.Errorf("OpenOrders = %d, want 3", book.OpenOrders)
	}
	if book.OpenUnits != 175 {
		t.Errorf("OpenUnits = %d, want 175", book.OpenUnits)
	}
	if book.Late != 1 {
		t.Errorf("Late = %d, want 1", book.Late)
	}
	if book.Customers != 1 {
		t.Errorf("Customers = %d, want 1", book.Customers)
	}
	// 100 + 50 + 25 units at 12.50.
	if book.OpenValue.String() != "2187.50" {
		t.Errorf("OpenValue = %s, want 2187.50", book.OpenValue)
	}
}

// TestCoverageFindsWhatCannotShip is the report that replaces reorder points
// for this business: demand is known exactly because it is sitting in signed
// purchase orders.
func TestCoverageFindsWhatCannotShip(t *testing.T) {
	f := newClientFixture(t, 200)
	ctx := context.Background()

	f.order(t, f.storeA, "MCY-0001", 150)
	f.order(t, f.storeB, "MCY-0002", 150)

	coverage, err := f.svc.Coverage(ctx, core.NilID)
	if err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	if len(coverage.Lines) != 1 {
		t.Fatalf("Coverage() returned %d lines, want 1", len(coverage.Lines))
	}

	line := coverage.Lines[0]
	if line.Committed != 300 {
		t.Errorf("Committed = %d, want 300 across both orders", line.Committed)
	}
	if line.OnHand != 200 {
		t.Errorf("OnHand = %d, want 200", line.OnHand)
	}
	if line.Short != 100 {
		t.Errorf("Short = %d, want 100", line.Short)
	}
	if line.Orders != 2 {
		t.Errorf("Orders = %d, want 2", line.Orders)
	}
	// 100 short at a cost of 11.50.
	if line.ShortValue.String() != "1150.00" {
		t.Errorf("ShortValue = %s, want 1150.00", line.ShortValue)
	}
	if coverage.ShortLines != 1 {
		t.Errorf("ShortLines = %d, want 1", coverage.ShortLines)
	}
	// Both orders need the product that is short, so both are blocked.
	if coverage.BlockedOrders != 2 {
		t.Errorf("BlockedOrders = %d, want 2", coverage.BlockedOrders)
	}
}

func TestCoverageIsQuietWhenEverythingIsCovered(t *testing.T) {
	f := newClientFixture(t, 1000)
	ctx := context.Background()

	f.order(t, f.storeA, "MCY-0001", 100)

	coverage, err := f.svc.Coverage(ctx, core.NilID)
	if err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	if coverage.ShortLines != 0 || coverage.BlockedOrders != 0 {
		t.Errorf("Coverage() reported %d short lines and %d blocked orders, want none",
			coverage.ShortLines, coverage.BlockedOrders)
	}
	if len(coverage.Lines) != 1 || !coverage.Lines[0].Covered() {
		t.Errorf("Coverage() = %+v, want one covered line", coverage.Lines)
	}
}

// TestCoverageIgnoresShippedQuantities checks that only what still has to go
// counts as demand.
func TestCoverageIgnoresShippedQuantities(t *testing.T) {
	f := newClientFixture(t, 50)
	ctx := context.Background()

	if _, err := f.svc.SaveOrder(ctx, core.StoreOrder{
		CustomerID: f.customer.ID, StoreID: f.storeA.ID,
		CustomerPONumber: "MCY-PART", Status: core.OrderConfirmed,
	}, []core.StoreOrderLine{
		{ProductID: f.product.ID, Quantity: 100, ShippedQty: 80,
			UnitPrice: core.MustParseMoney("12.50", "USD")},
	}); err != nil {
		t.Fatalf("SaveOrder() error = %v", err)
	}

	coverage, err := f.svc.Coverage(ctx, core.NilID)
	if err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	if len(coverage.Lines) != 1 {
		t.Fatalf("Coverage() returned %d lines, want 1", len(coverage.Lines))
	}
	// 20 outstanding against 50 on hand, so nothing is short.
	if coverage.Lines[0].Committed != 20 {
		t.Errorf("Committed = %d, want the 20 still to ship", coverage.Lines[0].Committed)
	}
	if coverage.ShortLines != 0 {
		t.Errorf("ShortLines = %d, want 0", coverage.ShortLines)
	}
}

// TestCoverageExcludesClosedOrders checks that finished work stops counting as
// demand.
func TestCoverageExcludesClosedOrders(t *testing.T) {
	f := newClientFixture(t, 10)
	ctx := context.Background()

	order := f.order(t, f.storeA, "MCY-CANCEL", 500)

	before, err := f.svc.Coverage(ctx, core.NilID)
	if err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	if before.ShortLines != 1 {
		t.Fatalf("ShortLines = %d before cancelling, want 1", before.ShortLines)
	}

	if err := f.svc.CancelOrder(ctx, order.ID); err != nil {
		t.Fatalf("CancelOrder() error = %v", err)
	}

	after, err := f.svc.Coverage(ctx, core.NilID)
	if err != nil {
		t.Fatalf("Coverage() error = %v", err)
	}
	if len(after.Lines) != 0 {
		t.Errorf("Coverage() still counts a cancelled order as demand: %+v", after.Lines)
	}
}
