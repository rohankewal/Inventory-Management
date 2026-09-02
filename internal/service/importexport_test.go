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

const goodCSV = `SKU,Name,Category,Supplier,Price,Cost,Quantity,Reorder Point,Unit,Tags
WID-1,Blue Widget,Widgets,Acme,24.99,11.50,120,25,each,"fragile, bestseller"
WID-2,Red Widget,Widgets,Acme,19.99,9.00,0,10,each,
BOX-1,Cardboard Box,Packaging,Globex,1.25,0.40,5000,1000,each,bulky
`

func TestImportCSVCreatesProducts(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	result, err := svc.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}
	if !result.OK() {
		t.Fatalf("ImportCSV() reported problems: %v", result.Problems)
	}
	if result.Created != 3 {
		t.Errorf("Created = %d, want 3", result.Created)
	}

	product, err := svc.GetProductBySKU(ctx, "WID-1", core.NilID)
	if err != nil {
		t.Fatalf("GetProductBySKU() error = %v", err)
	}
	if product.Name != "Blue Widget" || product.Category != "Widgets" || product.Supplier != "Acme" {
		t.Errorf("imported product = %+v, want the CSV values", product.Product)
	}
	if product.Price.String() != "24.99" || product.Cost.String() != "11.50" {
		t.Errorf("prices = %s/%s, want 24.99/11.50", product.Price, product.Cost)
	}
	if product.OnHand != 120 {
		t.Errorf("OnHand = %d, want 120", product.OnHand)
	}
	if product.ReorderPoint != 25 {
		t.Errorf("ReorderPoint = %d, want 25", product.ReorderPoint)
	}
	if !product.Tags.Has("fragile") || !product.Tags.Has("bestseller") {
		t.Errorf("Tags = %v, want fragile and bestseller", product.Tags)
	}

	// The opening quantity must arrive as a ledger entry, not an assigned
	// number, or an imported catalogue has stock with no recorded origin.
	history, err := svc.MovementHistory(ctx, storage.MovementFilter{ProductID: product.ID})
	if err != nil {
		t.Fatalf("MovementHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].Reason != core.ReasonOpeningBalance {
		t.Errorf("import produced %d movements, want one opening balance", len(history))
	}
}

// TestImportCSVDryRunWritesNothing is the guarantee the preview rests on.
func TestImportCSVDryRunWritesNothing(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	result, err := svc.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ImportCSV(dry run) error = %v", err)
	}
	if result.Created != 3 {
		t.Errorf("Created = %d, want the preview to report 3", result.Created)
	}
	if !result.DryRun {
		t.Error("DryRun is false on a dry-run result")
	}

	page, err := svc.ListProducts(ctx, storage.ProductFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("ListProducts() error = %v", err)
	}
	if page.Total != 0 {
		t.Errorf("a dry run created %d product(s); it must write nothing", page.Total)
	}
}

// TestImportCSVSkipsBadRows checks the per-row contract: a row that cannot be
// read is reported and skipped, the rest of the file still imports, and the
// bad row leaves nothing half-written behind.
func TestImportCSVSkipsBadRows(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	// The second row's price is unparseable. Rows are reported individually so
	// that one bad cell does not cost someone the other nine hundred rows.
	const partial = `SKU,Name,Price,Quantity
GOOD-1,First,1.00,10
BAD-1,Second,not-a-price,5
GOOD-2,Third,3.00,7
`
	result, err := svc.ImportCSV(ctx, strings.NewReader(partial), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}
	if result.Created != 2 {
		t.Errorf("Created = %d, want the two valid rows", result.Created)
	}
	if len(result.Problems) != 1 {
		t.Fatalf("Problems = %v, want exactly one", result.Problems)
	}
	if result.Problems[0].SKU != "BAD-1" {
		t.Errorf("problem is attributed to %q, want BAD-1", result.Problems[0].SKU)
	}
	if result.Problems[0].Line != 3 {
		t.Errorf("problem reported on line %d, want 3 as the spreadsheet numbers it", result.Problems[0].Line)
	}

	if _, err := svc.GetProductBySKU(ctx, "BAD-1", core.NilID); err == nil {
		t.Error("the failed row created a product anyway")
	}
}

// TestImportCSVWritesNothingOnAHardFailure covers the other half of the
// contract: a file the reader chokes on partway through must leave the
// catalogue exactly as it was, so re-running the fixed file is safe.
func TestImportCSVWritesNothingOnAHardFailure(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	// The second row has an unterminated quoted field, which fails the CSV
	// reader itself rather than any one row's validation.
	const malformed = "SKU,Name,Price\nGOOD-1,First,1.00\nBAD-1,\"Unterminated,2.00\n"

	if _, err := svc.ImportCSV(ctx, strings.NewReader(malformed), service.ImportOptions{}); err == nil {
		t.Fatal("ImportCSV() accepted a malformed file")
	}

	page, err := svc.ListProducts(ctx, storage.ProductFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("ListProducts() error = %v", err)
	}
	if page.Total != 0 {
		t.Errorf("a failed import left %d product(s) behind, want none", page.Total)
	}
}

func TestImportCSVRejectsDuplicateSKUsInOneFile(t *testing.T) {
	svc, _ := newService(t)

	const duplicates = `SKU,Name,Price
DUP-1,First,1.00
DUP-1,Second,2.00
`
	result, err := svc.ImportCSV(context.Background(), strings.NewReader(duplicates), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}
	if result.Created != 1 {
		t.Errorf("Created = %d, want 1", result.Created)
	}
	if len(result.Problems) != 1 || !strings.Contains(result.Problems[0].Message, "duplicate") {
		t.Errorf("Problems = %v, want a duplicate-SKU report", result.Problems)
	}
}

func TestImportCSVUpdateExisting(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	seedProduct(t, svc, core.Product{
		SKU: "WID-1", Name: "Old Name", Price: core.MustParseMoney("1.00", "USD"),
	}, service.OpeningStock{Quantity: 500})

	// Without the flag, an existing SKU is a conflict rather than a silent
	// overwrite. The other two rows are new, so they still import.
	blocked, err := svc.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}
	if blocked.Updated != 0 {
		t.Errorf("Updated = %d without the update flag, want 0", blocked.Updated)
	}
	if blocked.Created != 2 {
		t.Errorf("Created = %d, want the two new rows", blocked.Created)
	}
	if len(blocked.Problems) != 1 || !strings.Contains(blocked.Problems[0].Message, "already exists") {
		t.Errorf("Problems = %v, want the existing SKU reported as a conflict", blocked.Problems)
	}
	if unchanged, _ := svc.GetProductBySKU(ctx, "WID-1", core.NilID); unchanged.Name != "Old Name" {
		t.Errorf("the conflicting row overwrote the product anyway: name = %q", unchanged.Name)
	}

	updated, err := svc.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{UpdateExisting: true})
	if err != nil {
		t.Fatalf("ImportCSV(update) error = %v", err)
	}
	if updated.Updated != 3 || updated.Created != 0 {
		t.Errorf("Updated/Created = %d/%d, want 3/0 now that every row exists",
			updated.Updated, updated.Created)
	}

	product, err := svc.GetProductBySKU(ctx, "WID-1", core.NilID)
	if err != nil {
		t.Fatalf("GetProductBySKU() error = %v", err)
	}
	if product.Name != "Blue Widget" {
		t.Errorf("Name = %q, want the imported value", product.Name)
	}

	// Quantity means "the level is now this", so re-importing the same file
	// must not stack. An import that added its quantity would double stock
	// every time somebody re-ran it.
	if product.OnHand != 120 {
		t.Errorf("OnHand = %d, want 120, not the previous 500 plus 120", product.OnHand)
	}

	if _, err := svc.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{UpdateExisting: true}); err != nil {
		t.Fatalf("second ImportCSV() error = %v", err)
	}
	product, _ = svc.GetProductBySKU(ctx, "WID-1", core.NilID)
	if product.OnHand != 120 {
		t.Errorf("OnHand = %d after re-importing the same file, want 120", product.OnHand)
	}
}

// TestImportCSVAcceptsCommonHeaderSpellings is what keeps the feature usable:
// every business exports its catalogue with slightly different headings.
func TestImportCSVAcceptsCommonHeaderSpellings(t *testing.T) {
	svc, _ := newService(t)

	const alternative = `Item Code,Product Name,Sale Price,Unit Cost,Qty,Min Stock,UPC,Vendor
ALT-1,Alternative Headings,9.99,4.00,42,7,0123456789012,Initech
`
	result, err := svc.ImportCSV(context.Background(), strings.NewReader(alternative), service.ImportOptions{})
	if err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}
	if result.Created != 1 {
		t.Fatalf("Created = %d, want 1. Problems: %v", result.Created, result.Problems)
	}

	product, err := svc.GetProductBySKU(context.Background(), "ALT-1", core.NilID)
	if err != nil {
		t.Fatalf("GetProductBySKU() error = %v", err)
	}
	if product.Price.String() != "9.99" || product.Cost.String() != "4.00" {
		t.Errorf("prices = %s/%s, want 9.99/4.00", product.Price, product.Cost)
	}
	if product.OnHand != 42 || product.ReorderPoint != 7 {
		t.Errorf("quantity/reorder = %d/%d, want 42/7", product.OnHand, product.ReorderPoint)
	}
	if product.Barcode != "0123456789012" || product.Supplier != "Initech" {
		t.Errorf("barcode/supplier = %q/%q, want the UPC and Vendor columns", product.Barcode, product.Supplier)
	}
}

func TestImportCSVRequiresSKUAndName(t *testing.T) {
	svc, _ := newService(t)

	for _, csv := range []string{
		"Name,Price\nNo SKU column,1.00\n",
		"SKU,Price\nNO-NAME,1.00\n",
	} {
		if _, err := svc.ImportCSV(context.Background(), strings.NewReader(csv), service.ImportOptions{}); err == nil {
			t.Errorf("ImportCSV() accepted a file missing a required column:\n%s", csv)
		}
	}

	if _, err := svc.ImportCSV(context.Background(), strings.NewReader(""), service.ImportOptions{}); err == nil {
		t.Error("ImportCSV() accepted an empty file")
	}
}

// TestCSVRoundTrip is the promise the shared column list makes: what this
// application exports, it can read back without editing.
func TestCSVRoundTrip(t *testing.T) {
	source, _ := newService(t)
	ctx := context.Background()

	if _, err := source.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{}); err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}

	var exported bytes.Buffer
	count, err := source.ExportCSV(ctx, &exported, storage.ProductFilter{})
	if err != nil {
		t.Fatalf("ExportCSV() error = %v", err)
	}
	if count != 3 {
		t.Fatalf("ExportCSV() wrote %d products, want 3", count)
	}

	destination, _ := newService(t)
	result, err := destination.ImportCSV(ctx, bytes.NewReader(exported.Bytes()), service.ImportOptions{})
	if err != nil {
		t.Fatalf("re-importing the export failed: %v", err)
	}
	if !result.OK() {
		t.Fatalf("re-importing the export reported problems: %v", result.Problems)
	}
	if result.Created != 3 {
		t.Errorf("Created = %d, want 3", result.Created)
	}

	original, err := source.GetProductBySKU(ctx, "BOX-1", core.NilID)
	if err != nil {
		t.Fatalf("GetProductBySKU() error = %v", err)
	}
	copied, err := destination.GetProductBySKU(ctx, "BOX-1", core.NilID)
	if err != nil {
		t.Fatalf("GetProductBySKU() error = %v", err)
	}

	for _, field := range []struct {
		name      string
		want, got string
	}{
		{"name", original.Name, copied.Name},
		{"category", original.Category, copied.Category},
		{"supplier", original.Supplier, copied.Supplier},
		{"price", original.Price.String(), copied.Price.String()},
		{"cost", original.Cost.String(), copied.Cost.String()},
		{"tags", original.Tags.String(), copied.Tags.String()},
		{"unit", string(original.Unit), string(copied.Unit)},
	} {
		if field.want != field.got {
			t.Errorf("%s survived the round trip as %q, want %q", field.name, field.got, field.want)
		}
	}
	if original.OnHand != copied.OnHand {
		t.Errorf("quantity = %d after the round trip, want %d", copied.OnHand, original.OnHand)
	}
	if original.ReorderPoint != copied.ReorderPoint {
		t.Errorf("reorder point = %d after the round trip, want %d", copied.ReorderPoint, original.ReorderPoint)
	}
}

func TestCSVTemplateImportsCleanly(t *testing.T) {
	var template bytes.Buffer
	if err := service.CSVTemplate(&template); err != nil {
		t.Fatalf("CSVTemplate() error = %v", err)
	}

	svc, _ := newService(t)
	result, err := svc.ImportCSV(context.Background(), bytes.NewReader(template.Bytes()), service.ImportOptions{})
	if err != nil {
		t.Fatalf("the template does not import: %v", err)
	}
	if result.Created != 1 || !result.OK() {
		t.Errorf("importing the template created %d rows with problems %v, want 1 and none",
			result.Created, result.Problems)
	}
}

func TestExportCSVRespectsTheFilter(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()

	if _, err := svc.ImportCSV(ctx, strings.NewReader(goodCSV), service.ImportOptions{}); err != nil {
		t.Fatalf("ImportCSV() error = %v", err)
	}

	var out bytes.Buffer
	count, err := svc.ExportCSV(ctx, &out, storage.ProductFilter{Category: "Packaging"})
	if err != nil {
		t.Fatalf("ExportCSV() error = %v", err)
	}
	if count != 1 {
		t.Errorf("ExportCSV(category) wrote %d products, want 1", count)
	}
	if !strings.Contains(out.String(), "BOX-1") || strings.Contains(out.String(), "WID-1") {
		t.Error("the export ignored the category filter")
	}
}
