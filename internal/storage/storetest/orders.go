package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// orderTests are the client-side conformance cases, appended to the suite in
// storetest.go.
var orderTests = []namedTest{
	{"Customers/CreateAndGet", testCustomerCreateAndGet},
	{"Customers/DuplicateCodeConflicts", testCustomerDuplicateCode},
	{"Customers/ListCountsStoresAndOrders", testCustomerCounts},
	{"Stores/CodesAreUniquePerCustomer", testStoreCodeScope},
	{"Stores/AddressRoundTrips", testStoreAddress},
	{"Stores/CascadeWithCustomer", testStoreCascade},
	{"Programs/CreateAndFilter", testProgramCreateAndFilter},
	{"Orders/CreateWithLines", testOrderCreateWithLines},
	{"Orders/PONumberUniquePerCustomer", testOrderPOScope},
	{"Orders/FindByPONumberAcrossCustomers", testOrderFindByPO},
	{"Orders/DetailResolvesEverything", testOrderDetail},
	{"Orders/ListAggregatesTotals", testOrderListTotals},
	{"Orders/FiltersAndSorts", testOrderFilters},
	{"Orders/ShippedLinesCannotBeRemoved", testOrderLineRemoval},
	{"Orders/RejectsOverShipping", testOrderOverShipping},
	{"Orders/RequiresALine", testOrderRequiresLine},
}

// --- customers --------------------------------------------------------------

func newCustomer(code, name string) core.Customer {
	return core.Customer{
		Code: code, Name: name, Currency: "USD", Terms: "Net 60", Active: true,
		Contact: core.Contact{Name: "Buying Office", Email: "buyers@example.com"},
	}
}

func seedCustomer(t *testing.T, s storage.Store, code, name string) core.Customer {
	t.Helper()

	customer := newCustomer(code, name)
	if err := s.Customers().Create(context.Background(), &customer); err != nil {
		t.Fatalf("Create(customer %s) error = %v", code, err)
	}
	return customer
}

func seedStore(t *testing.T, s storage.Store, customerID core.ID, code, name string) core.CustomerStore {
	t.Helper()

	store := core.CustomerStore{
		CustomerID: customerID, Code: code, Name: name, Active: true,
		ShipTo: core.Address{
			Line1: "1 Example Way", City: "Springfield", Region: "IL",
			PostalCode: "62701", Country: "USA",
		},
	}
	if err := s.Stores().Create(context.Background(), &store); err != nil {
		t.Fatalf("Create(store %s) error = %v", code, err)
	}
	return store
}

func testCustomerCreateAndGet(t *testing.T, s storage.Store) {
	ctx := context.Background()

	// Codes are normalised to upper case on write.
	customer := newCustomer("macys", "Macy's")
	if err := s.Customers().Create(ctx, &customer); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if customer.Code != "MACYS" {
		t.Errorf("Code = %q, want it normalised to MACYS", customer.Code)
	}

	got, err := s.Customers().Get(ctx, customer.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "Macy's" || got.Currency != "USD" || got.Terms != "Net 60" {
		t.Errorf("Get() = %+v, want the created values", got)
	}
	if got.Contact.Email != "buyers@example.com" {
		t.Errorf("Contact.Email = %q, want it preserved", got.Contact.Email)
	}

	byCode, err := s.Customers().GetByCode(ctx, "macys")
	if err != nil {
		t.Fatalf("GetByCode() error = %v", err)
	}
	if byCode.ID != customer.ID {
		t.Errorf("GetByCode() resolved a different customer")
	}

	if _, err := s.Customers().Get(ctx, core.NewID()); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Get(missing) error = %v, want core.ErrNotFound", err)
	}
}

func testCustomerDuplicateCode(t *testing.T, s storage.Store) {
	ctx := context.Background()

	seedCustomer(t, s, "DUP", "First")

	clash := newCustomer("dup", "Second")
	if err := s.Customers().Create(ctx, &clash); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a duplicate code error = %v, want core.ErrConflict", err)
	}
}

// testCustomerCounts checks the aggregates the customer screen shows. Getting
// them from the listing query rather than a query per row is what keeps that
// screen usable.
func testCustomerCounts(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "ACME", "Acme Retail")
	first := seedStore(t, s, customer.ID, "001", "Downtown")
	seedStore(t, s, customer.ID, "002", "Mall")

	closed := seedStore(t, s, customer.ID, "003", "Closed Branch")
	if err := s.Stores().SetActive(ctx, closed.ID, false); err != nil {
		t.Fatalf("SetActive() error = %v", err)
	}

	product := newProduct("ORD-1", "Ordered Item", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}
	seedOrder(t, s, customer, first, product, "ACME-001", core.OrderConfirmed)
	shipped := seedOrder(t, s, customer, first, product, "ACME-002", core.OrderConfirmed)
	if err := s.Orders().SetStatus(ctx, shipped.ID, core.OrderShipped); err != nil {
		t.Fatalf("SetStatus() error = %v", err)
	}

	listed, err := s.Customers().List(ctx, storage.CustomerFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List() returned %d customers, want 1", len(listed))
	}

	got := listed[0]
	if got.StoreCount != 3 {
		t.Errorf("StoreCount = %d, want 3", got.StoreCount)
	}
	if got.ActiveStores != 2 {
		t.Errorf("ActiveStores = %d, want 2", got.ActiveStores)
	}
	// Only the still-open order counts; a shipped one is not outstanding work.
	if got.OpenOrders != 1 {
		t.Errorf("OpenOrders = %d, want 1", got.OpenOrders)
	}
}

// --- stores -----------------------------------------------------------------

// testStoreCodeScope is the rule that makes multi-customer operation possible:
// two retailers both numbering a store 001 is entirely normal.
func testStoreCodeScope(t *testing.T, s storage.Store) {
	ctx := context.Background()

	macys := seedCustomer(t, s, "MACYS", "Macy's")
	kohls := seedCustomer(t, s, "KOHLS", "Kohl's")

	seedStore(t, s, macys.ID, "001", "Herald Square")
	seedStore(t, s, kohls.ID, "001", "Menomonee Falls")

	// The same code under the same customer must still collide.
	clash := core.CustomerStore{CustomerID: macys.ID, Code: "001", Name: "Duplicate", Active: true}
	if err := s.Stores().Create(ctx, &clash); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a duplicate store code error = %v, want core.ErrConflict", err)
	}

	// And lookup must be scoped to the customer, or a picker would show the
	// wrong retailer's store.
	got, err := s.Stores().GetByCode(ctx, kohls.ID, "001")
	if err != nil {
		t.Fatalf("GetByCode() error = %v", err)
	}
	if got.Name != "Menomonee Falls" {
		t.Errorf("GetByCode() = %q, want the Kohl's store", got.Name)
	}
}

func testStoreAddress(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "ADDR", "Address Test")
	store := core.CustomerStore{
		CustomerID: customer.ID, Code: "0047", Name: "Herald Square", Active: true,
		ShipTo: core.Address{
			Line1: "151 W 34th St", Line2: "Receiving Dock B",
			City: "New York", Region: "NY", PostalCode: "10001", Country: "USA",
		},
		Contact:      core.Contact{Name: "Dock Supervisor", Phone: "+1 212 555 0100"},
		RoutingNotes: "Appointment required 24h ahead. GS1-128 carton labels.",
	}
	if err := s.Stores().Create(ctx, &store); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := s.Stores().Get(ctx, store.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.ShipTo != store.ShipTo {
		t.Errorf("ShipTo = %+v, want %+v", got.ShipTo, store.ShipTo)
	}
	if got.RoutingNotes != store.RoutingNotes {
		t.Errorf("RoutingNotes did not survive the round trip")
	}
	// The routing notes are what stop a chargeback, so they must reach the
	// paperwork intact.
	if want := "151 W 34th St, Receiving Dock B, New York, NY 10001, USA"; got.ShipTo.SingleLine() != want {
		t.Errorf("SingleLine() = %q, want %q", got.ShipTo.SingleLine(), want)
	}
}

func testStoreCascade(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "GONE", "Departing")
	seedStore(t, s, customer.ID, "001", "Only Store")

	// A customer with no orders can be removed, and its stores go with it —
	// they have no meaning without the customer they belong to.
	if err := s.Customers().Delete(ctx, customer.ID); err != nil {
		t.Fatalf("Delete(customer) error = %v", err)
	}
	remaining, err := s.Stores().List(ctx, storage.StoreFilter{CustomerID: customer.ID})
	if err != nil {
		t.Fatalf("List(stores) error = %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("%d store(s) outlived their customer", len(remaining))
	}
}

// --- programs ---------------------------------------------------------------

func testProgramCreateAndFilter(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "PROG", "Program Client")

	open := core.Program{
		CustomerID: customer.ID, Code: "fw26-throws", Name: "FW26 Throws",
		Season: "Fall/Winter 2026", Status: core.ProgramInProduction,
		TargetDeliveryDate: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.Programs().Create(ctx, &open); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if open.Code != "FW26-THROWS" {
		t.Errorf("Code = %q, want it upper-cased", open.Code)
	}

	done := core.Program{
		CustomerID: customer.ID, Code: "SS25-TOWELS", Name: "SS25 Towels",
		Status: core.ProgramClosed,
	}
	if err := s.Programs().Create(ctx, &done); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := s.Programs().Get(ctx, open.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !got.TargetDeliveryDate.Equal(open.TargetDeliveryDate) {
		t.Errorf("TargetDeliveryDate = %v, want %v", got.TargetDeliveryDate, open.TargetDeliveryDate)
	}

	// The default screen shows live work, not finished history.
	live, err := s.Programs().List(ctx, storage.ProgramFilter{OpenOnly: true})
	if err != nil {
		t.Fatalf("List(open) error = %v", err)
	}
	if len(live) != 1 || live[0].Code != "FW26-THROWS" {
		t.Errorf("List(open) returned %d programs, want just the live one", len(live))
	}

	all, err := s.Programs().List(ctx, storage.ProgramFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(all) != 2 {
		t.Errorf("List() returned %d programs, want 2", len(all))
	}
}

// --- orders -----------------------------------------------------------------

func seedOrder(t *testing.T, s storage.Store, customer core.Customer, store core.CustomerStore,
	product core.Product, poNumber string, status core.OrderStatus) core.StoreOrder {
	t.Helper()

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID,
		CustomerPONumber: poNumber, Status: status, Currency: "USD",
		RequestedShipDate: time.Now().UTC().AddDate(0, 0, 30),
		CancelAfterDate:   time.Now().UTC().AddDate(0, 0, 45),
	}
	lines := []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 100, UnitPrice: core.MustParseMoney("12.50", "USD")},
	}
	if err := s.Orders().Create(context.Background(), &order, lines); err != nil {
		t.Fatalf("Create(order %s) error = %v", poNumber, err)
	}
	return order
}

func testOrderCreateWithLines(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	store := seedStore(t, s, customer.ID, "0047", "Herald Square")

	product := newProduct("THROW-1", "Sherpa Throw", "24.99")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID,
		CustomerPONumber: "mcy-0123", Currency: "USD",
	}
	lines := []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 240, UnitPrice: core.MustParseMoney("12.50", "USD")},
		{ProductID: product.ID, Quantity: 60, UnitPrice: core.MustParseMoney("11.00", "USD")},
	}
	if err := s.Orders().Create(ctx, &order, lines); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// The PO number is normalised, because it is typed by hand from an email
	// as often as it is imported.
	if order.CustomerPONumber != "MCY-0123" {
		t.Errorf("CustomerPONumber = %q, want MCY-0123", order.CustomerPONumber)
	}
	if order.Status != core.OrderDraft {
		t.Errorf("Status = %q, want a new order to start as draft", order.Status)
	}
	if order.OrderedAt.IsZero() {
		t.Error("OrderedAt was left unset")
	}

	// Line numbers are assigned in order so the document reads the way it was
	// entered.
	detail, err := s.Orders().Detail(ctx, order.ID)
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if len(detail.Lines) != 2 {
		t.Fatalf("Detail() returned %d lines, want 2", len(detail.Lines))
	}
	if detail.Lines[0].LineNo != 1 || detail.Lines[1].LineNo != 2 {
		t.Errorf("line numbers = %d, %d, want 1, 2", detail.Lines[0].LineNo, detail.Lines[1].LineNo)
	}
}

func testOrderPOScope(t *testing.T, s storage.Store) {
	ctx := context.Background()

	macys := seedCustomer(t, s, "MACYS", "Macy's")
	kohls := seedCustomer(t, s, "KOHLS", "Kohl's")
	macysStore := seedStore(t, s, macys.ID, "0047", "Herald Square")
	kohlsStore := seedStore(t, s, kohls.ID, "0047", "Menomonee Falls")

	product := newProduct("PO-1", "Ordered", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}

	seedOrder(t, s, macys, macysStore, product, "PO-9000", core.OrderConfirmed)

	// The same PO number under a different customer is not a conflict: clients
	// number their own paperwork and cannot be expected to coordinate.
	other := core.StoreOrder{
		CustomerID: kohls.ID, StoreID: kohlsStore.ID,
		CustomerPONumber: "PO-9000", Currency: "USD",
	}
	lines := []core.StoreOrderLine{{ProductID: product.ID, Quantity: 10}}
	if err := s.Orders().Create(ctx, &other, lines); err != nil {
		t.Fatalf("Create() across customers error = %v, want it allowed", err)
	}

	// Under the same customer it must collide.
	clash := core.StoreOrder{
		CustomerID: macys.ID, StoreID: macysStore.ID,
		CustomerPONumber: "po-9000", Currency: "USD",
	}
	if err := s.Orders().Create(ctx, &clash, lines); !errors.Is(err, core.ErrConflict) {
		t.Errorf("Create() with a duplicate PO error = %v, want core.ErrConflict", err)
	}
}

func testOrderFindByPO(t *testing.T, s storage.Store) {
	ctx := context.Background()

	macys := seedCustomer(t, s, "MACYS", "Macy's")
	kohls := seedCustomer(t, s, "KOHLS", "Kohl's")
	macysStore := seedStore(t, s, macys.ID, "0047", "Herald Square")
	kohlsStore := seedStore(t, s, kohls.ID, "0100", "Menomonee Falls")

	product := newProduct("FIND-1", "Findable", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}
	seedOrder(t, s, macys, macysStore, product, "SHARED-1", core.OrderConfirmed)
	seedOrder(t, s, kohls, kohlsStore, product, "SHARED-1", core.OrderConfirmed)

	// Somebody reading a number off an email does not know whose it is, so the
	// search resolves across customers and may legitimately find several.
	found, err := s.Orders().FindByPONumber(ctx, "shared-1")
	if err != nil {
		t.Fatalf("FindByPONumber() error = %v", err)
	}
	if len(found) != 2 {
		t.Errorf("FindByPONumber() returned %d orders, want 2", len(found))
	}

	none, err := s.Orders().FindByPONumber(ctx, "NOT-A-PO")
	if err != nil {
		t.Fatalf("FindByPONumber() error = %v", err)
	}
	if len(none) != 0 {
		t.Errorf("FindByPONumber(unknown) returned %d orders, want 0", len(none))
	}
}

func testOrderDetail(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	store := seedStore(t, s, customer.ID, "0047", "Herald Square")

	program := core.Program{
		CustomerID: customer.ID, Code: "FW26", Name: "FW26 Throws", Status: core.ProgramConfirmed,
	}
	if err := s.Programs().Create(ctx, &program); err != nil {
		t.Fatalf("Create(program) error = %v", err)
	}

	product := newProduct("THROW-1", "Sherpa Throw", "24.99")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}
	appendMovement(t, s, product.ID, 500, core.ReasonOpeningBalance)

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID, ProgramID: program.ID,
		CustomerPONumber: "MCY-0123", Currency: "USD",
	}
	lines := []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 240, UnitPrice: core.MustParseMoney("12.50", "USD")},
	}
	if err := s.Orders().Create(ctx, &order, lines); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// One call must return everything a document needs, or every screen and
	// every PDF re-assembles it differently.
	detail, err := s.Orders().Detail(ctx, order.ID)
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if detail.Customer.Name != "Macy's" {
		t.Errorf("Customer = %q, want Macy's", detail.Customer.Name)
	}
	if detail.Store.Code != "0047" {
		t.Errorf("Store = %q, want 0047", detail.Store.Code)
	}
	if detail.Program == nil || detail.Program.Code != "FW26" {
		t.Errorf("Program = %+v, want FW26", detail.Program)
	}
	if len(detail.Lines) != 1 {
		t.Fatalf("Lines = %d, want 1", len(detail.Lines))
	}
	if detail.Lines[0].SKU != "THROW-1" || detail.Lines[0].Name != "Sherpa Throw" {
		t.Errorf("line product = %q/%q, want THROW-1/Sherpa Throw",
			detail.Lines[0].SKU, detail.Lines[0].Name)
	}
	// On-hand travels with the line so a picker can see whether it can go.
	if detail.Lines[0].OnHand != 500 {
		t.Errorf("line OnHand = %d, want 500", detail.Lines[0].OnHand)
	}
	if detail.Totals.Units != 240 || detail.Totals.Value.String() != "3000.00" {
		t.Errorf("Totals = %d units worth %s, want 240 and 3000.00",
			detail.Totals.Units, detail.Totals.Value)
	}

	// An order with no program must still resolve.
	plain := seedOrder(t, s, customer, store, product, "MCY-0124", core.OrderConfirmed)
	plainDetail, err := s.Orders().Detail(ctx, plain.ID)
	if err != nil {
		t.Fatalf("Detail() without a program error = %v", err)
	}
	if plainDetail.Program != nil {
		t.Error("Detail() invented a program for an order that has none")
	}
}

func testOrderListTotals(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	store := seedStore(t, s, customer.ID, "0047", "Herald Square")

	product := newProduct("TOT-1", "Totalled", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID,
		CustomerPONumber: "MCY-TOT", Currency: "USD",
	}
	lines := []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 100, UnitPrice: core.MustParseMoney("10.00", "USD"), ShippedQty: 40},
		{ProductID: product.ID, Quantity: 50, UnitPrice: core.MustParseMoney("8.00", "USD"), CancelledQty: 10},
	}
	if err := s.Orders().Create(ctx, &order, lines); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	listed, err := s.Orders().List(ctx, storage.OrderFilter{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List() returned %d orders, want 1", len(listed))
	}

	got := listed[0].Totals
	if got.Lines != 2 || got.Units != 150 {
		t.Errorf("Totals = %d lines, %d units, want 2 and 150", got.Lines, got.Units)
	}
	if got.Shipped != 40 || got.Cancelled != 10 {
		t.Errorf("Shipped/Cancelled = %d/%d, want 40/10", got.Shipped, got.Cancelled)
	}
	if got.Outstanding != 100 {
		t.Errorf("Outstanding = %d, want 100", got.Outstanding)
	}
	// 100 at 10.00 plus 50 at 8.00.
	if got.Value.String() != "1400.00" {
		t.Errorf("Value = %s, want 1400.00", got.Value)
	}

	// The names shown on the screen come from the same query.
	if listed[0].CustomerName != "Macy's" || listed[0].StoreCode != "0047" {
		t.Errorf("List() did not resolve the customer and store names")
	}
}

func testOrderFilters(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	downtown := seedStore(t, s, customer.ID, "0047", "Herald Square")
	mall := seedStore(t, s, customer.ID, "0100", "Roosevelt Field")

	product := newProduct("FILT-1", "Filtered", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}

	now := time.Now().UTC()
	orders := []struct {
		po     string
		store  core.CustomerStore
		status core.OrderStatus
		ship   time.Time
		cancel time.Time
	}{
		{"MCY-0001", downtown, core.OrderConfirmed, now.AddDate(0, 0, 7), now.AddDate(0, 0, 14)},
		{"MCY-0002", mall, core.OrderConfirmed, now.AddDate(0, 0, 30), now.AddDate(0, 0, 45)},
		{"MCY-0003", downtown, core.OrderShipped, now.AddDate(0, 0, -30), now.AddDate(0, 0, -20)},
		{"MCY-0004", mall, core.OrderConfirmed, now.AddDate(0, 0, -20), now.AddDate(0, 0, -5)},
	}
	for _, o := range orders {
		order := core.StoreOrder{
			CustomerID: customer.ID, StoreID: o.store.ID,
			CustomerPONumber: o.po, Status: o.status, Currency: "USD",
			RequestedShipDate: o.ship, CancelAfterDate: o.cancel,
		}
		lines := []core.StoreOrderLine{{ProductID: product.ID, Quantity: 10, UnitPrice: core.MustParseMoney("10.00", "USD")}}
		if err := s.Orders().Create(ctx, &order, lines); err != nil {
			t.Fatalf("Create(%s) error = %v", o.po, err)
		}
	}

	t.Run("open only", func(t *testing.T) {
		got, err := s.Orders().List(ctx, storage.OrderFilter{OpenOnly: true})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 3 {
			t.Errorf("open orders = %d, want 3", len(got))
		}
	})

	t.Run("by store", func(t *testing.T) {
		got, err := s.Orders().List(ctx, storage.OrderFilter{StoreID: mall.ID})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 2 {
			t.Errorf("orders for one store = %d, want 2", len(got))
		}
	})

	t.Run("late only", func(t *testing.T) {
		// MCY-0004 is past its cancel date and still open. MCY-0003 is also
		// past it but has shipped, so it is not a problem any more.
		got, err := s.Orders().List(ctx, storage.OrderFilter{LateOnly: true})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 1 || got[0].CustomerPONumber != "MCY-0004" {
			t.Errorf("late orders = %v, want just MCY-0004", poNumbers(got))
		}
	})

	t.Run("search by PO and store", func(t *testing.T) {
		byPO, err := s.Orders().List(ctx, storage.OrderFilter{Search: "0002"})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(byPO) != 1 || byPO[0].CustomerPONumber != "MCY-0002" {
			t.Errorf("search by PO returned %v, want MCY-0002", poNumbers(byPO))
		}

		byStore, err := s.Orders().List(ctx, storage.OrderFilter{Search: "Roosevelt"})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(byStore) != 2 {
			t.Errorf("search by store name returned %d, want 2", len(byStore))
		}
	})

	t.Run("default order is by ship date, soonest first", func(t *testing.T) {
		got, err := s.Orders().List(ctx, storage.OrderFilter{})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) == 0 || got[0].CustomerPONumber != "MCY-0003" {
			t.Errorf("default order = %v, want the earliest ship date first", poNumbers(got))
		}
	})

	t.Run("ship date window", func(t *testing.T) {
		got, err := s.Orders().List(ctx, storage.OrderFilter{
			ShipAfter: now, ShipBefore: now.AddDate(0, 0, 10),
		})
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(got) != 1 || got[0].CustomerPONumber != "MCY-0001" {
			t.Errorf("ship window returned %v, want MCY-0001", poNumbers(got))
		}
	})
}

// testOrderLineRemoval guards the ledger's integrity: goods that physically
// left must stay traceable to the order that sent them.
func testOrderLineRemoval(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	store := seedStore(t, s, customer.ID, "0047", "Herald Square")

	product := newProduct("EDIT-1", "Editable", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID,
		CustomerPONumber: "MCY-EDIT", Currency: "USD",
	}
	lines := []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 100, UnitPrice: core.MustParseMoney("10.00", "USD"), ShippedQty: 30},
		{ProductID: product.ID, Quantity: 50, UnitPrice: core.MustParseMoney("10.00", "USD")},
	}
	if err := s.Orders().Create(ctx, &order, lines); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	detail, err := s.Orders().Detail(ctx, order.ID)
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}

	// Dropping the un-shipped line is fine.
	keep := []core.StoreOrderLine{detail.Lines[0].StoreOrderLine}
	if err := s.Orders().ReplaceLines(ctx, order.ID, keep); err != nil {
		t.Fatalf("ReplaceLines() error = %v", err)
	}

	// Dropping the shipped one is not.
	err = s.Orders().ReplaceLines(ctx, order.ID, []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 25, UnitPrice: core.MustParseMoney("10.00", "USD")},
	})
	if !errors.Is(err, core.ErrConflict) {
		t.Errorf("ReplaceLines() dropping a shipped line error = %v, want core.ErrConflict", err)
	}

	// And the shipped line must still be there.
	after, err := s.Orders().Detail(ctx, order.ID)
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if len(after.Lines) != 1 || after.Lines[0].ShippedQty != 30 {
		t.Errorf("the shipped line did not survive a rejected edit: %+v", after.Lines)
	}
}

// testOrderOverShipping checks the database itself refuses an impossible state,
// so no code path can create one.
func testOrderOverShipping(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	store := seedStore(t, s, customer.ID, "0047", "Herald Square")

	product := newProduct("OVER-1", "Over-shipped", "10.00")
	if err := s.Products().Create(ctx, &product); err != nil {
		t.Fatalf("Create(product) error = %v", err)
	}

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID,
		CustomerPONumber: "MCY-OVER", Currency: "USD",
	}
	lines := []core.StoreOrderLine{
		{ProductID: product.ID, Quantity: 10, ShippedQty: 11,
			UnitPrice: core.MustParseMoney("10.00", "USD")},
	}
	if err := s.Orders().Create(ctx, &order, lines); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Create() shipping more than ordered error = %v, want core.ErrInvalid", err)
	}
}

func testOrderRequiresLine(t *testing.T, s storage.Store) {
	ctx := context.Background()

	customer := seedCustomer(t, s, "MACYS", "Macy's")
	store := seedStore(t, s, customer.ID, "0047", "Herald Square")

	order := core.StoreOrder{
		CustomerID: customer.ID, StoreID: store.ID,
		CustomerPONumber: "MCY-EMPTY", Currency: "USD",
	}
	if err := s.Orders().Create(ctx, &order, nil); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Create() with no lines error = %v, want core.ErrInvalid", err)
	}

	// And nothing may be left behind.
	if _, err := s.Orders().GetByPONumber(ctx, customer.ID, "MCY-EMPTY"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("an order with no lines was created anyway: err = %v", err)
	}
}

func poNumbers(orders []core.OrderSummary) []string {
	out := make([]string, len(orders))
	for i, o := range orders {
		out[i] = o.CustomerPONumber
	}
	return out
}
