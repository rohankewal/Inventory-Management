package service_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

const storeListCSV = `Store Number,Store Name,Address,City,State,Zip,Country,Delivery Instructions
0047,Herald Square,151 W 34th St,New York,NY,10001,USA,Appointment required
0100,Roosevelt Field,630 Old Country Rd,Garden City,NY,11530,USA,
0233,Union Square,50 O'Farrell St,San Francisco,CA,94102,USA,Rear dock only
`

func TestImportStoresCSV(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	customer, err := svc.CreateCustomer(ctx, core.Customer{Code: "MACYS", Name: "Macy's"})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}

	result, err := svc.ImportStoresCSV(ctx, customer.ID, strings.NewReader(storeListCSV), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportStoresCSV() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("ImportStoresCSV() reported problems: %v", result.Problems)
	}
	if result.Created != 3 {
		t.Errorf("Created = %d, want 3", result.Created)
	}

	stores, err := svc.ListStores(ctx, storage.StoreFilter{CustomerID: customer.ID})
	if err != nil {
		t.Fatalf("ListStores() error = %v", err)
	}
	if len(stores) != 3 {
		t.Fatalf("ListStores() returned %d, want 3", len(stores))
	}

	// The alternative headings the file used must all have mapped.
	first := stores[0]
	if first.Code != "0047" || first.Name != "Herald Square" {
		t.Errorf("first store = %q/%q, want 0047/Herald Square", first.Code, first.Name)
	}
	if first.ShipTo.Line1 != "151 W 34th St" || first.ShipTo.PostalCode != "10001" {
		t.Errorf("address = %+v, want the CSV values", first.ShipTo)
	}
	// Routing notes are what stop a chargeback, so they must come through.
	if first.RoutingNotes != "Appointment required" {
		t.Errorf("RoutingNotes = %q, want the delivery instructions", first.RoutingNotes)
	}
}

func TestImportStoresCSVNeedsACustomer(t *testing.T) {
	svc, _ := newService(t)

	_, err := svc.ImportStoresCSV(context.Background(), core.NilID,
		strings.NewReader(storeListCSV), service.ImportOptions{})
	if err == nil {
		t.Error("ImportStoresCSV() with no customer returned nil error")
	}
}

// orderSheetCSV is the shape a retailer actually sends: one file, many stores,
// a separate PO number for each.
const orderSheetCSV = `PO Number,Door,Style,Qty,Unit Price,Ship Date,Cancel Date
MCY-0123,0047,THROW-1,240,12.50,2026-10-01,2026-10-15
MCY-0123,0047,THROW-2,60,14.00,2026-10-01,2026-10-15
MCY-0124,0100,THROW-1,180,12.50,2026-10-01,2026-10-15
MCY-0125,0233,THROW-1,120,12.50,2026-10-08,2026-10-22
MCY-0125,0233,THROW-2,40,14.00,2026-10-08,2026-10-22
`

func seedOrderImportFixture(t *testing.T) (*service.Inventory, core.Customer) {
	t.Helper()
	ctx := context.Background()

	svc, _ := newService(t)
	customer, err := svc.CreateCustomer(ctx, core.Customer{Code: "MACYS", Name: "Macy's"})
	if err != nil {
		t.Fatalf("CreateCustomer() error = %v", err)
	}
	if _, err := svc.ImportStoresCSV(ctx, customer.ID, strings.NewReader(storeListCSV), service.ImportOptions{}); err != nil {
		t.Fatalf("ImportStoresCSV() error = %v", err)
	}

	for _, sku := range []string{"THROW-1", "THROW-2"} {
		seedProduct(t, svc, core.Product{
			SKU: sku, Name: "Throw " + sku,
			Price: core.MustParseMoney("24.99", "USD"),
			Cost:  core.MustParseMoney("11.50", "USD"),
		}, service.OpeningStock{Quantity: 1000, UnitCost: core.MustParseMoney("11.50", "USD")})
	}
	return svc, customer
}

// TestImportOrdersCSVGroupsRowsIntoOnePOPerStore is the core of the feature:
// one spreadsheet becomes one order per store, which is how the business works.
func TestImportOrdersCSVGroupsRowsIntoOnePOPerStore(t *testing.T) {
	svc, customer := seedOrderImportFixture(t)
	ctx := context.Background()

	result, err := svc.ImportOrdersCSV(ctx, customer.ID, strings.NewReader(orderSheetCSV), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportOrdersCSV() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("ImportOrdersCSV() reported problems: %v", result.Problems)
	}
	// Five rows, three POs.
	if result.Rows != 5 {
		t.Errorf("Rows = %d, want 5", result.Rows)
	}
	if result.Created != 3 {
		t.Errorf("Created = %d orders, want 3", result.Created)
	}

	page, err := svc.ListOrders(ctx, storage.OrderFilter{CustomerID: customer.ID})
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if page.Total != 3 {
		t.Fatalf("ListOrders() returned %d orders, want 3", page.Total)
	}

	byPO := map[string]core.OrderSummary{}
	for _, order := range page.Items {
		byPO[order.CustomerPONumber] = order
	}

	// The two-line PO carries both lines and ships to the right door.
	first := byPO["MCY-0123"]
	if first.Totals.Lines != 2 || first.Totals.Units != 300 {
		t.Errorf("MCY-0123 = %d lines, %d units, want 2 and 300",
			first.Totals.Lines, first.Totals.Units)
	}
	if first.StoreCode != "0047" {
		t.Errorf("MCY-0123 ships to %q, want 0047", first.StoreCode)
	}
	// 240 at 12.50 plus 60 at 14.00.
	if first.Totals.Value.String() != "3840.00" {
		t.Errorf("MCY-0123 value = %s, want 3840.00", first.Totals.Value)
	}

	if byPO["MCY-0124"].StoreCode != "0100" || byPO["MCY-0125"].StoreCode != "0233" {
		t.Errorf("orders were routed to the wrong stores: %+v", byPO)
	}

	// Dates carried through, which is what the whole schedule hangs off.
	if byPO["MCY-0123"].RequestedShipDate.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("ship date = %v, want 2026-10-01", byPO["MCY-0123"].RequestedShipDate)
	}
	if byPO["MCY-0125"].CancelAfterDate.Format("2006-01-02") != "2026-10-22" {
		t.Errorf("cancel date = %v, want 2026-10-22", byPO["MCY-0125"].CancelAfterDate)
	}
}

func TestImportOrdersCSVDryRunWritesNothing(t *testing.T) {
	svc, customer := seedOrderImportFixture(t)
	ctx := context.Background()

	result, err := svc.ImportOrdersCSV(ctx, customer.ID,
		strings.NewReader(orderSheetCSV), service.ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ImportOrdersCSV(dry run) error = %v", err)
	}
	if result.Created != 3 {
		t.Errorf("Created = %d, want the preview to report 3", result.Created)
	}

	page, err := svc.ListOrders(ctx, storage.OrderFilter{CustomerID: customer.ID})
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if page.Total != 0 {
		t.Errorf("a dry run created %d order(s); it must write nothing", page.Total)
	}
}

// TestImportOrdersCSVReportsUnknownStores checks the most common real failure:
// a PO for a door that was never set up.
func TestImportOrdersCSVReportsUnknownStores(t *testing.T) {
	svc, customer := seedOrderImportFixture(t)
	ctx := context.Background()

	const withUnknownStore = `PO Number,Door,Style,Qty,Unit Price
MCY-0123,0047,THROW-1,240,12.50
MCY-0199,9999,THROW-1,100,12.50
`
	result, err := svc.ImportOrdersCSV(ctx, customer.ID,
		strings.NewReader(withUnknownStore), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportOrdersCSV() error = %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want the valid PO only", result.Created)
	}
	if len(result.Problems) != 1 {
		t.Fatalf("Problems = %v, want one", result.Problems)
	}
	// The message has to say what to do about it.
	if !strings.Contains(result.Problems[0].Message, "9999") ||
		!strings.Contains(result.Problems[0].Message, "store list") {
		t.Errorf("problem = %q, want it to name the missing door and the fix",
			result.Problems[0].Message)
	}
}

func TestImportOrdersCSVReportsUnknownProducts(t *testing.T) {
	svc, customer := seedOrderImportFixture(t)

	const withUnknownSKU = `PO Number,Door,Style,Qty
MCY-0123,0047,NOT-A-SKU,240
`
	result, err := svc.ImportOrdersCSV(context.Background(), customer.ID,
		strings.NewReader(withUnknownSKU), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportOrdersCSV() error = %v", err)
	}
	if result.Created != 0 {
		t.Errorf("Created = %d, want 0", result.Created)
	}
	if len(result.Problems) != 1 || !strings.Contains(result.Problems[0].Message, "NOT-A-SKU") {
		t.Errorf("Problems = %v, want the unknown SKU named", result.Problems)
	}
}

func TestImportOrdersCSVUpdateExisting(t *testing.T) {
	svc, customer := seedOrderImportFixture(t)
	ctx := context.Background()

	if _, err := svc.ImportOrdersCSV(ctx, customer.ID,
		strings.NewReader(orderSheetCSV), service.ImportOptions{}); err != nil {
		t.Fatalf("ImportOrdersCSV() error = %v", err)
	}

	// Re-sending the same file without the flag must not silently rewrite the
	// client's orders.
	blocked, err := svc.ImportOrdersCSV(ctx, customer.ID,
		strings.NewReader(orderSheetCSV), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportOrdersCSV() error = %v", err)
	}
	if blocked.Created != 0 || blocked.Updated != 0 {
		t.Errorf("a repeat import wrote %d/%d, want nothing", blocked.Created, blocked.Updated)
	}
	if len(blocked.Problems) != 3 {
		t.Errorf("Problems = %d, want one per existing PO", len(blocked.Problems))
	}

	// A revised sheet with the flag replaces the lines.
	const revised = `PO Number,Door,Style,Qty,Unit Price
MCY-0123,0047,THROW-1,500,11.75
`
	updated, err := svc.ImportOrdersCSV(ctx, customer.ID,
		strings.NewReader(revised), service.ImportOptions{UpdateExisting: true})
	if err != nil {
		t.Fatalf("ImportOrdersCSV(update) error = %v", err)
	}
	if updated.Updated != 1 {
		t.Errorf("Updated = %d, want 1", updated.Updated)
	}

	page, err := svc.ListOrders(ctx, storage.OrderFilter{Search: "MCY-0123"})
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("ListOrders() returned %d, want 1", len(page.Items))
	}
	if page.Items[0].Totals.Units != 500 || page.Items[0].Totals.Lines != 1 {
		t.Errorf("after revision = %d units on %d lines, want 500 on 1",
			page.Items[0].Totals.Units, page.Items[0].Totals.Lines)
	}
}

func TestImportOrdersCSVRequiresItsColumns(t *testing.T) {
	svc, customer := seedOrderImportFixture(t)

	for _, csv := range []string{
		"Door,Style,Qty\n0047,THROW-1,10\n",       // no PO number
		"PO Number,Style,Qty\nMCY-1,THROW-1,10\n", // no store
		"PO Number,Door,Qty\nMCY-1,0047,10\n",     // no product
		"PO Number,Door,Style\nMCY-1,0047,THROW-1\n",
	} {
		if _, err := svc.ImportOrdersCSV(context.Background(), customer.ID,
			strings.NewReader(csv), service.ImportOptions{}); err == nil {
			t.Errorf("ImportOrdersCSV() accepted a file missing a required column:\n%s", csv)
		}
	}
}

func TestOrderTemplatesImportCleanly(t *testing.T) {
	ctx := context.Background()

	t.Run("stores", func(t *testing.T) {
		svc, _ := newService(t)
		customer, err := svc.CreateCustomer(ctx, core.Customer{Code: "T", Name: "Template Test"})
		if err != nil {
			t.Fatalf("CreateCustomer() error = %v", err)
		}

		var template bytes.Buffer
		if err := service.StoresCSVTemplate(&template); err != nil {
			t.Fatalf("StoresCSVTemplate() error = %v", err)
		}
		result, err := svc.ImportStoresCSV(ctx, customer.ID, bytes.NewReader(template.Bytes()), service.ImportOptions{})
		if err != nil {
			t.Fatalf("the store template does not import: %v", err)
		}
		if result.Created != 1 || !result.OK() {
			t.Errorf("importing the store template created %d with problems %v, want 1 and none",
				result.Created, result.Problems)
		}
	})

	t.Run("orders", func(t *testing.T) {
		svc, customer := seedOrderImportFixture(t)

		var template bytes.Buffer
		if err := service.OrdersCSVTemplate(&template); err != nil {
			t.Fatalf("OrdersCSVTemplate() error = %v", err)
		}
		result, err := svc.ImportOrdersCSV(ctx, customer.ID, bytes.NewReader(template.Bytes()), service.ImportOptions{})
		if err != nil {
			t.Fatalf("the order template does not import: %v", err)
		}
		if result.Created != 1 || !result.OK() {
			t.Errorf("importing the order template created %d with problems %v, want 1 and none",
				result.Created, result.Problems)
		}
	})
}
