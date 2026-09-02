package service

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// CSV column headers. Export writes them in this order and import accepts them
// in any order, so a file exported here re-imports without editing.
var csvColumns = []string{
	"SKU", "Barcode", "Name", "Description", "Category", "Supplier", "Tags",
	"Unit", "Price", "Cost", "Quantity", "Reorder Point", "Reorder Quantity",
	"Weight (g)", "Non-Stock", "Track Lots", "Notes", "Status",
}

// headerAliases maps the many ways a spreadsheet spells a column onto the one
// this importer uses. Every business exports its catalogue with slightly
// different headings, and rejecting a file over "Qty" versus "Quantity" is the
// fastest way to make an import feature useless.
var headerAliases = map[string]string{
	"sku": "sku", "itemcode": "sku", "productcode": "sku", "code": "sku", "partnumber": "sku",
	"barcode": "barcode", "upc": "barcode", "ean": "barcode", "gtin": "barcode", "scancode": "barcode",
	"name": "name", "productname": "name", "description": "description", "itemname": "name",
	"longdescription": "description", "details": "description",
	"category": "category", "group": "category", "producttype": "category", "type": "category",
	"supplier": "supplier", "vendor": "supplier", "manufacturer": "supplier",
	"tags": "tags", "labels": "tags",
	"unit": "unit", "uom": "unit", "unitofmeasure": "unit", "measure": "unit",
	"price": "price", "saleprice": "price", "sellprice": "price", "retailprice": "price", "unitprice": "price",
	"cost": "cost", "unitcost": "cost", "costprice": "cost", "buyprice": "cost", "purchaseprice": "cost",
	"quantity": "quantity", "qty": "quantity", "stock": "quantity", "onhand": "quantity",
	"quantityonhand": "quantity", "stocklevel": "quantity", "count": "quantity",
	"reorderpoint": "reorderpoint", "reorderlevel": "reorderpoint", "minstock": "reorderpoint",
	"minimum": "reorderpoint", "min": "reorderpoint", "safetystock": "reorderpoint",
	"reorderquantity": "reorderquantity", "reorderqty": "reorderquantity", "orderquantity": "reorderquantity",
	"weightg": "weight", "weight": "weight", "weightgrams": "weight",
	"nonstock": "nonstock", "service": "nonstock", "nontracked": "nonstock",
	"tracklots": "tracklots", "lottracked": "tracklots", "batchtracked": "tracklots",
	"notes": "notes", "comment": "notes", "comments": "notes", "remarks": "notes",
	"status": "status", "active": "status", "archived": "status",
}

// normalizeHeader reduces a heading to its comparison form.
func normalizeHeader(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// RowProblem is one thing wrong with one row of an import.
type RowProblem struct {
	// Line is the line number in the file, counting the header as line 1, so
	// it matches what a spreadsheet shows.
	Line    int
	SKU     string
	Message string
}

func (p RowProblem) Error() string {
	if p.SKU != "" {
		return fmt.Sprintf("line %d (%s): %s", p.Line, p.SKU, p.Message)
	}
	return fmt.Sprintf("line %d: %s", p.Line, p.Message)
}

// ImportOptions control an import run.
type ImportOptions struct {
	// DryRun validates everything and reports what would happen without
	// writing. An import that cannot be previewed is one nobody dares run on a
	// real catalogue.
	DryRun bool
	// UpdateExisting matches rows to products by SKU and updates them.
	// Without it, a row whose SKU already exists is reported as a conflict.
	UpdateExisting bool
	// LocationID is where opening quantities are booked in.
	LocationID core.ID
	// ActorID is recorded on the ledger entries the import creates.
	ActorID core.ID
}

// ImportResult summarises an import run.
type ImportResult struct {
	DryRun   bool
	Rows     int
	Created  int
	Updated  int
	Skipped  int
	Problems []RowProblem
	// Mapped lists the columns that were recognised, and Ignored the ones that
	// were not, so a user can see why a field did not come through.
	Mapped  []string
	Ignored []string
}

// OK reports whether the run had no problems.
func (r ImportResult) OK() bool { return len(r.Problems) == 0 }

// Summary is a one-line description for the UI.
func (r ImportResult) Summary() string {
	verb := "Imported"
	if r.DryRun {
		verb = "Preview:"
	}
	summary := fmt.Sprintf("%s %d created, %d updated, %d skipped, from %d row(s)",
		verb, r.Created, r.Updated, r.Skipped, r.Rows)
	if len(r.Problems) > 0 {
		summary += fmt.Sprintf(" — %d problem(s)", len(r.Problems))
	}
	return summary
}

// maxImportProblems bounds how many problems are collected, so a completely
// wrong file produces a readable report rather than a hundred thousand lines.
const maxImportProblems = 200

// ImportCSV reads products from a CSV stream.
//
// The whole run happens in one transaction: a partially applied import leaves
// a catalogue in a state nobody can reason about, and "fix the file and run it
// again" only works if the failed run changed nothing.
func (s *Inventory) ImportCSV(ctx context.Context, r io.Reader, opts ImportOptions) (ImportResult, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1 // ragged rows are reported per row, not fatally
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err == io.EOF {
		return ImportResult{}, fmt.Errorf("import: %w: the file is empty", core.ErrInvalid)
	}
	if err != nil {
		return ImportResult{}, fmt.Errorf("import: reading the header row: %w", err)
	}

	index, result := mapHeader(header)
	if _, ok := index["sku"]; !ok {
		return ImportResult{}, fmt.Errorf(
			"import: %w: no SKU column found; the file needs a column headed SKU, Item Code or Part Number",
			core.ErrInvalid)
	}
	if _, ok := index["name"]; !ok {
		return ImportResult{}, fmt.Errorf(
			"import: %w: no Name column found", core.ErrInvalid)
	}
	result.DryRun = opts.DryRun

	rows, err := readAllRows(reader)
	if err != nil {
		return ImportResult{}, err
	}
	result.Rows = len(rows)

	apply := func(st storage.Store) error {
		seen := map[string]int{}

		for i, row := range rows {
			line := i + 2 // the header occupies line 1

			sku := strings.TrimSpace(field(row, index, "sku"))
			if sku == "" && strings.TrimSpace(strings.Join(row, "")) == "" {
				result.Skipped++
				continue // a blank separator row
			}

			// A file that lists the same SKU twice would otherwise have its
			// second row silently overwrite the first.
			if previous, duplicate := seen[strings.ToLower(sku)]; duplicate {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, SKU: sku,
					Message: fmt.Sprintf("duplicate SKU, already on line %d", previous)})
				continue
			}
			seen[strings.ToLower(sku)] = line

			action, err := s.importRow(ctx, st, row, index, opts, line)
			if err != nil {
				result.Skipped++
				var problem RowProblem
				if errors.As(err, &problem) {
					result.addProblem(problem)
				} else {
					result.addProblem(RowProblem{Line: line, SKU: sku, Message: err.Error()})
				}
				continue
			}

			switch action {
			case actionCreated:
				result.Created++
			case actionUpdated:
				result.Updated++
			default:
				result.Skipped++
			}
		}
		return nil
	}

	if opts.DryRun {
		// A dry run still exercises every write so that constraint violations
		// surface in the preview, then rolls the whole thing back.
		sentinel := errors.New("dry run")
		err = s.store.InTx(ctx, func(st storage.Store) error {
			if err := apply(st); err != nil {
				return err
			}
			return sentinel
		})
		if err != nil && !errors.Is(err, sentinel) {
			return result, err
		}
		return result, nil
	}

	if err := s.store.InTx(ctx, apply); err != nil {
		return result, err
	}

	s.log.Info("csv import complete",
		"rows", result.Rows, "created", result.Created,
		"updated", result.Updated, "skipped", result.Skipped)
	return result, nil
}

type importAction int

const (
	actionSkipped importAction = iota
	actionCreated
	actionUpdated
)

func (s *Inventory) importRow(ctx context.Context, st storage.Store, row []string, index map[string]int, opts ImportOptions, line int) (importAction, error) {
	sku := strings.TrimSpace(field(row, index, "sku"))
	fail := func(format string, args ...any) (importAction, error) {
		return actionSkipped, RowProblem{Line: line, SKU: sku, Message: fmt.Sprintf(format, args...)}
	}

	if sku == "" {
		return fail("SKU is required")
	}

	existing, err := st.Products().GetBySKU(ctx, sku)
	switch {
	case err == nil && !opts.UpdateExisting:
		return fail("SKU already exists; enable \"update existing\" to overwrite it")
	case err != nil && !errors.Is(err, core.ErrNotFound):
		return actionSkipped, err
	}
	isUpdate := err == nil

	product := existing
	if !isUpdate {
		product = core.Product{Active: true, Unit: core.DefaultUnit}
	}
	product.SKU = sku

	if value, ok := lookup(row, index, "name"); ok {
		product.Name = value
	}
	if value, ok := lookup(row, index, "barcode"); ok {
		product.Barcode = value
	}
	if value, ok := lookup(row, index, "description"); ok {
		product.Description = value
	}
	if value, ok := lookup(row, index, "category"); ok {
		product.Category = value
	}
	if value, ok := lookup(row, index, "supplier"); ok {
		product.Supplier = value
	}
	if value, ok := lookup(row, index, "notes"); ok {
		product.Notes = value
	}
	if value, ok := lookup(row, index, "tags"); ok {
		product.Tags = core.ParseTags(value)
	}
	if value, ok := lookup(row, index, "unit"); ok {
		product.Unit = core.ParseUnit(value)
	}

	if value, ok := lookup(row, index, "price"); ok {
		price, err := core.ParseMoney(value, s.defaultCurrency)
		if err != nil {
			return fail("price %q is not a valid amount", value)
		}
		product.Price = price
	}
	if value, ok := lookup(row, index, "cost"); ok {
		cost, err := core.ParseMoney(value, s.defaultCurrency)
		if err != nil {
			return fail("cost %q is not a valid amount", value)
		}
		product.Cost = cost
	}

	if value, ok := lookup(row, index, "reorderpoint"); ok {
		n, err := parseWholeNumber(value)
		if err != nil {
			return fail("reorder point %q is not a whole number", value)
		}
		product.ReorderPoint = n
	}
	if value, ok := lookup(row, index, "reorderquantity"); ok {
		n, err := parseWholeNumber(value)
		if err != nil {
			return fail("reorder quantity %q is not a whole number", value)
		}
		product.ReorderQuantity = n
	}
	if value, ok := lookup(row, index, "weight"); ok {
		n, err := parseWholeNumber(value)
		if err != nil {
			return fail("weight %q is not a whole number of grams", value)
		}
		product.WeightGrams = n
	}
	if value, ok := lookup(row, index, "nonstock"); ok {
		product.NonStock = parseBoolish(value)
	}
	if value, ok := lookup(row, index, "tracklots"); ok {
		product.TrackLots = parseBoolish(value)
	}
	if value, ok := lookup(row, index, "status"); ok {
		product.Active = !strings.EqualFold(strings.TrimSpace(value), "archived")
	}

	s.applyDefaults(&product)

	// Quantity means "this is the level now", so on an update it posts the
	// difference as a count rather than adding on top. An import that added
	// its quantity to the existing one would double stock every time somebody
	// re-ran the same file.
	quantity, hasQuantity := int64(0), false
	if value, ok := lookup(row, index, "quantity"); ok {
		n, err := parseWholeNumber(value)
		if err != nil {
			return fail("quantity %q is not a whole number", value)
		}
		if n < 0 {
			return fail("quantity cannot be negative")
		}
		quantity, hasQuantity = n, true
	}

	if isUpdate {
		if err := st.Products().Update(ctx, &product); err != nil {
			return fail("%s", importErrorText(err))
		}
		if hasQuantity && !product.NonStock {
			if err := s.setStockIn(ctx, st, product.ID, opts, quantity); err != nil {
				return fail("%s", importErrorText(err))
			}
		}
		return actionUpdated, nil
	}

	if err := st.Products().Create(ctx, &product); err != nil {
		return fail("%s", importErrorText(err))
	}
	if hasQuantity && quantity != 0 && !product.NonStock {
		movement := core.StockMovement{
			ProductID:  product.ID,
			LocationID: s.locationOrDefault(opts.LocationID),
			QtyDelta:   quantity,
			Reason:     core.ReasonOpeningBalance,
			Note:       "CSV import",
			UnitCost:   product.Cost,
			ActorID:    opts.ActorID,
			OccurredAt: s.now(),
		}
		if err := st.Movements().Append(ctx, &movement); err != nil {
			return fail("%s", importErrorText(err))
		}
	}
	return actionCreated, nil
}

// setStockIn reconciles a product to a counted quantity inside an existing
// transaction.
func (s *Inventory) setStockIn(ctx context.Context, st storage.Store, productID core.ID, opts ImportOptions, quantity int64) error {
	location := s.locationOrDefault(opts.LocationID)

	current, err := st.Movements().OnHand(ctx, productID, location)
	if err != nil {
		return err
	}
	variance := quantity - current
	if variance == 0 {
		return nil
	}

	movement := core.StockMovement{
		ProductID:  productID,
		LocationID: location,
		QtyDelta:   variance,
		Reason:     core.ReasonStockCount,
		Note:       "CSV import",
		ActorID:    opts.ActorID,
		OccurredAt: s.now(),
	}
	return st.Movements().Append(ctx, &movement)
}

// importErrorText renders an error for a per-row report, keeping validation
// detail and discarding the internal operation prefix.
func importErrorText(err error) string {
	var validation *core.ValidationError
	if errors.As(err, &validation) {
		parts := make([]string, len(validation.Fields))
		for i, f := range validation.Fields {
			parts[i] = f.Message
		}
		return strings.Join(parts, "; ")
	}
	if errors.Is(err, core.ErrConflict) {
		return "conflicts with an existing record (a duplicate SKU or barcode)"
	}
	return err.Error()
}

func (r *ImportResult) addProblem(p RowProblem) {
	if len(r.Problems) >= maxImportProblems {
		return
	}
	r.Problems = append(r.Problems, p)
}

// mapHeader resolves the file's headings to canonical field names.
func mapHeader(header []string) (map[string]int, ImportResult) {
	index := map[string]int{}
	var result ImportResult

	for i, raw := range header {
		canonical, ok := headerAliases[normalizeHeader(raw)]
		if !ok {
			if strings.TrimSpace(raw) != "" {
				result.Ignored = append(result.Ignored, strings.TrimSpace(raw))
			}
			continue
		}
		// First column wins, so a file with both "Qty" and "Quantity" uses the
		// leftmost rather than whichever the map happens to visit last.
		if _, taken := index[canonical]; !taken {
			index[canonical] = i
			result.Mapped = append(result.Mapped, strings.TrimSpace(raw))
		}
	}
	return index, result
}

func readAllRows(reader *csv.Reader) ([][]string, error) {
	var rows [][]string
	for {
		row, err := reader.Read()
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				return nil, fmt.Errorf("import: %w: line %d is malformed: %s",
					core.ErrInvalid, parseErr.Line, parseErr.Err)
			}
			return nil, fmt.Errorf("import: reading the file: %w", err)
		}
		rows = append(rows, row)
	}
}

// field returns a column's value, or empty if the row is short.
func field(row []string, index map[string]int, name string) string {
	value, _ := lookup(row, index, name)
	return value
}

// lookup returns a column's value and whether the column was present at all,
// which is what lets an update leave absent columns untouched instead of
// blanking them.
func lookup(row []string, index map[string]int, name string) (string, bool) {
	i, ok := index[name]
	if !ok || i >= len(row) {
		return "", false
	}
	return strings.TrimSpace(row[i]), true
}

func parseWholeNumber(s string) (int64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", ""))
	if s == "" {
		return 0, nil
	}
	// Spreadsheets export whole numbers as "12.00" often enough that rejecting
	// them would fail imports for no good reason.
	if whole, frac, ok := strings.Cut(s, "."); ok && strings.Trim(frac, "0") == "" {
		s = whole
	}
	return strconv.ParseInt(s, 10, 64)
}

// parseBoolish accepts the many ways a spreadsheet says yes.
func parseBoolish(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "y", "yes", "true", "t", "x", "on":
		return true
	}
	return false
}

// ExportCSV writes the matching products as CSV, using the same columns the
// importer reads so that a round trip is lossless.
func (s *Inventory) ExportCSV(ctx context.Context, w io.Writer, f storage.ProductFilter) (int, error) {
	f.Limit, f.Offset = 0, 0 // export the whole selection, not one page
	if !f.Sort.Valid() {
		f.Sort = storage.SortProductSKU
	}
	f.LocationID = s.locationOrDefault(f.LocationID)

	products, err := s.store.Products().List(ctx, f)
	if err != nil {
		return 0, err
	}

	writer := csv.NewWriter(w)
	if err := writer.Write(csvColumns); err != nil {
		return 0, fmt.Errorf("export: writing the header row: %w", err)
	}

	for _, p := range products {
		status := "Active"
		if !p.Active {
			status = "Archived"
		}
		record := []string{
			p.SKU,
			p.Barcode,
			p.Name,
			p.Description,
			p.Category,
			p.Supplier,
			p.Tags.String(),
			string(p.Unit),
			p.Price.String(),
			p.Cost.String(),
			strconv.FormatInt(p.OnHand, 10),
			strconv.FormatInt(p.ReorderPoint, 10),
			strconv.FormatInt(p.ReorderQuantity, 10),
			strconv.FormatInt(p.WeightGrams, 10),
			yesNo(p.NonStock),
			yesNo(p.TrackLots),
			p.Notes,
			status,
		}
		if err := writer.Write(record); err != nil {
			return 0, fmt.Errorf("export: writing %s: %w", p.SKU, err)
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return 0, fmt.Errorf("export: %w", err)
	}

	s.log.Info("csv export complete", "products", len(products))
	return len(products), nil
}

func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// CSVTemplate writes a header row plus one example line, so someone starting
// from nothing has a file to fill in rather than a format to guess.
func CSVTemplate(w io.Writer) error {
	writer := csv.NewWriter(w)
	if err := writer.Write(csvColumns); err != nil {
		return err
	}
	example := []string{
		"WIDGET-001", "0123456789012", "Blue Widget", "Standard blue widget",
		"Widgets", "Acme Supply Co", "fragile, bestseller", "each",
		"24.99", "11.50", "120", "25", "100", "340", "No", "No",
		"Stored in aisle 4", "Active",
	}
	if err := writer.Write(example); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}
