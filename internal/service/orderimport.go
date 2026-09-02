package service

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// Store import columns, written by ExportStoresCSV and read by ImportStoresCSV.
var storeCSVColumns = []string{
	"Store Code", "Store Name", "Address 1", "Address 2", "City", "State",
	"Postal Code", "Country", "Contact Name", "Contact Email", "Contact Phone",
	"Routing Notes", "Status",
}

var storeHeaderAliases = map[string]string{
	"storecode": "code", "code": "code", "storenumber": "code", "store": "code",
	"storeno": "code", "shiptocode": "code", "location": "code", "doornumber": "code", "door": "code",
	"storename": "name", "name": "name", "shiptoname": "name", "description": "name",
	"address1": "line1", "address": "line1", "addressline1": "line1", "street": "line1",
	"address2": "line2", "addressline2": "line2", "suite": "line2",
	"city": "city", "town": "city",
	"state": "region", "region": "region", "province": "region", "county": "region",
	"postalcode": "postal", "zip": "postal", "zipcode": "postal", "postcode": "postal",
	"country":     "country",
	"contactname": "contact_name", "contact": "contact_name", "manager": "contact_name",
	"contactemail": "contact_email", "email": "contact_email",
	"contactphone": "contact_phone", "phone": "contact_phone", "telephone": "contact_phone",
	"routingnotes": "routing", "routing": "routing", "deliveryinstructions": "routing",
	"notes": "routing", "instructions": "routing",
	"status": "status", "active": "status",
}

// ImportStoresCSV loads a customer's ship-to list.
//
// Clients send their store list as a spreadsheet, in whatever shape their own
// system exports. Matching the headings loosely is the difference between this
// taking a minute and taking an afternoon of re-typing.
func (s *Inventory) ImportStoresCSV(ctx context.Context, customerID core.ID, r io.Reader, opts ImportOptions) (ImportResult, error) {
	if customerID.IsZero() {
		return ImportResult{}, fmt.Errorf("import stores: %w: choose a customer first", core.ErrInvalid)
	}
	if _, err := s.store.Customers().Get(ctx, customerID); err != nil {
		return ImportResult{}, err
	}

	header, rows, err := readCSV(r)
	if err != nil {
		return ImportResult{}, err
	}

	index, result := mapHeaderWith(header, storeHeaderAliases)
	if _, ok := index["code"]; !ok {
		return ImportResult{}, fmt.Errorf(
			"import stores: %w: no store code column found; the file needs a column headed Store Code, Store Number or Door",
			core.ErrInvalid)
	}
	result.DryRun = opts.DryRun
	result.Rows = len(rows)

	apply := func(st storage.Store) error {
		seen := map[string]int{}

		for i, row := range rows {
			line := i + 2

			code := strings.ToUpper(strings.TrimSpace(field(row, index, "code")))
			if code == "" && strings.TrimSpace(strings.Join(row, "")) == "" {
				result.Skipped++
				continue
			}
			if code == "" {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, Message: "store code is required"})
				continue
			}
			if previous, duplicate := seen[code]; duplicate {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, SKU: code,
					Message: fmt.Sprintf("duplicate store code, already on line %d", previous)})
				continue
			}
			seen[code] = line

			existing, err := st.Stores().GetByCode(ctx, customerID, code)
			switch {
			case err == nil && !opts.UpdateExisting:
				result.Skipped++
				result.addProblem(RowProblem{Line: line, SKU: code,
					Message: "store already exists; enable \"update existing\" to overwrite it"})
				continue
			case err != nil && !errors.Is(err, core.ErrNotFound):
				return err
			}
			isUpdate := err == nil

			store := existing
			if !isUpdate {
				store = core.CustomerStore{CustomerID: customerID, Active: true}
			}
			store.Code = code

			assign := func(name string, target *string) {
				if value, ok := lookup(row, index, name); ok {
					*target = value
				}
			}
			assign("name", &store.Name)
			assign("line1", &store.ShipTo.Line1)
			assign("line2", &store.ShipTo.Line2)
			assign("city", &store.ShipTo.City)
			assign("region", &store.ShipTo.Region)
			assign("postal", &store.ShipTo.PostalCode)
			assign("country", &store.ShipTo.Country)
			assign("contact_name", &store.Contact.Name)
			assign("contact_email", &store.Contact.Email)
			assign("contact_phone", &store.Contact.Phone)
			assign("routing", &store.RoutingNotes)

			if value, ok := lookup(row, index, "status"); ok {
				store.Active = !strings.EqualFold(strings.TrimSpace(value), "archived") &&
					!strings.EqualFold(strings.TrimSpace(value), "inactive") &&
					!strings.EqualFold(strings.TrimSpace(value), "closed")
			}
			// A store with no name is unusable on paperwork, so fall back to
			// the code rather than rejecting an otherwise good row.
			if store.Name == "" {
				store.Name = "Store " + code
			}

			var writeErr error
			if isUpdate {
				writeErr = st.Stores().Update(ctx, &store)
			} else {
				writeErr = st.Stores().Create(ctx, &store)
			}
			if writeErr != nil {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, SKU: code, Message: importErrorText(writeErr)})
				continue
			}

			if isUpdate {
				result.Updated++
			} else {
				result.Created++
			}
		}
		return nil
	}

	if err := s.runImport(ctx, apply, opts.DryRun); err != nil {
		return result, err
	}
	if !opts.DryRun {
		s.log.Info("store import complete",
			"customer_id", customerID, "created", result.Created, "updated", result.Updated)
	}
	return result, nil
}

// Order import columns.
var orderCSVColumns = []string{
	"PO Number", "Store Code", "Ship Date", "Cancel Date", "Program",
	"SKU", "Quantity", "Unit Price", "Line Notes",
}

var orderHeaderAliases = map[string]string{
	"ponumber": "po", "po": "po", "purchaseorder": "po", "customerpo": "po",
	"ponum": "po", "orderno": "po", "ordernumber": "po", "porefererence": "po", "poreference": "po",
	"storecode": "store", "store": "store", "storenumber": "store", "shipto": "store",
	"door": "store", "doornumber": "store", "location": "store", "storeno": "store",
	"shipdate": "ship_date", "requestedshipdate": "ship_date", "startdate": "ship_date",
	"deliverydate": "ship_date", "duedate": "ship_date", "shipby": "ship_date",
	"canceldate": "cancel_date", "cancelafter": "cancel_date", "canceldateafter": "cancel_date",
	"cancelafterdate": "cancel_date", "expirydate": "cancel_date",
	"program": "program", "programcode": "program", "season": "program", "collection": "program",
	"sku": "sku", "itemcode": "sku", "productcode": "sku", "style": "sku",
	"stylenumber": "sku", "item": "sku", "partnumber": "sku", "upc": "barcode", "barcode": "barcode",
	"quantity": "quantity", "qty": "quantity", "units": "quantity", "ordered": "quantity",
	"unitprice": "price", "price": "price", "cost": "price", "unitcost": "price", "sellprice": "price",
	"linenotes": "notes", "notes": "notes", "comment": "notes", "comments": "notes",
}

// ImportOrdersCSV loads store purchase orders from one spreadsheet.
//
// Clients send a single file covering many stores. Rows are grouped by PO
// number, so one file becomes one order per store — which is exactly the shape
// a retailer's system exports and exactly the shape the business works in.
func (s *Inventory) ImportOrdersCSV(ctx context.Context, customerID core.ID, r io.Reader, opts ImportOptions) (ImportResult, error) {
	if customerID.IsZero() {
		return ImportResult{}, fmt.Errorf("import orders: %w: choose a customer first", core.ErrInvalid)
	}
	customer, err := s.store.Customers().Get(ctx, customerID)
	if err != nil {
		return ImportResult{}, err
	}

	header, rows, err := readCSV(r)
	if err != nil {
		return ImportResult{}, err
	}

	index, result := mapHeaderWith(header, orderHeaderAliases)
	for _, required := range []struct{ key, label string }{
		{"po", "PO Number"},
		{"store", "Store Code"},
		{"quantity", "Quantity"},
	} {
		if _, ok := index[required.key]; !ok {
			return ImportResult{}, fmt.Errorf(
				"import orders: %w: no %s column found", core.ErrInvalid, required.label)
		}
	}
	if _, hasSKU := index["sku"]; !hasSKU {
		if _, hasBarcode := index["barcode"]; !hasBarcode {
			return ImportResult{}, fmt.Errorf(
				"import orders: %w: the file needs a SKU or barcode column to identify products",
				core.ErrInvalid)
		}
	}
	result.DryRun = opts.DryRun
	result.Rows = len(rows)

	apply := func(st storage.Store) error {
		// Rows are grouped by PO number, preserving the order they appear in
		// so line numbers match the client's own document.
		type pending struct {
			poNumber string
			firstRow int
			header   core.StoreOrder
			lines    []core.StoreOrderLine
			broken   bool
		}
		var order []string
		grouped := map[string]*pending{}

		for i, row := range rows {
			line := i + 2

			poNumber := strings.ToUpper(strings.TrimSpace(field(row, index, "po")))
			if poNumber == "" && strings.TrimSpace(strings.Join(row, "")) == "" {
				result.Skipped++
				continue
			}
			if poNumber == "" {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, Message: "PO number is required"})
				continue
			}

			group, ok := grouped[poNumber]
			if !ok {
				group = &pending{poNumber: poNumber, firstRow: line}
				grouped[poNumber] = group
				order = append(order, poNumber)

				storeCode := strings.ToUpper(strings.TrimSpace(field(row, index, "store")))
				store, err := st.Stores().GetByCode(ctx, customerID, storeCode)
				if err != nil {
					group.broken = true
					result.addProblem(RowProblem{Line: line, SKU: poNumber, Message: fmt.Sprintf(
						"no store %q for %s — import the store list first", storeCode, customer.Code)})
					continue
				}

				group.header = core.StoreOrder{
					CustomerID: customerID, StoreID: store.ID,
					CustomerPONumber: poNumber,
					Status:           core.OrderDraft,
					Currency:         customer.Currency,
				}
				if value, ok := lookup(row, index, "ship_date"); ok && value != "" {
					when, err := parseImportDate(value)
					if err != nil {
						result.addProblem(RowProblem{Line: line, SKU: poNumber, Message: err.Error()})
					} else {
						group.header.RequestedShipDate = when
					}
				}
				if value, ok := lookup(row, index, "cancel_date"); ok && value != "" {
					when, err := parseImportDate(value)
					if err != nil {
						result.addProblem(RowProblem{Line: line, SKU: poNumber, Message: err.Error()})
					} else {
						group.header.CancelAfterDate = when
					}
				}
				if value, ok := lookup(row, index, "program"); ok && value != "" {
					program, err := st.Programs().GetByCode(ctx, customerID, value)
					if err == nil {
						group.header.ProgramID = program.ID
					} else if !errors.Is(err, core.ErrNotFound) {
						return err
					}
				}
			}
			if group.broken {
				result.Skipped++
				continue
			}

			product, err := resolveImportProduct(ctx, st, row, index)
			if err != nil {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, SKU: poNumber, Message: err.Error()})
				continue
			}

			quantity, err := parseWholeNumber(field(row, index, "quantity"))
			if err != nil || quantity <= 0 {
				result.Skipped++
				result.addProblem(RowProblem{Line: line, SKU: poNumber, Message: fmt.Sprintf(
					"quantity %q is not a whole number greater than zero", field(row, index, "quantity"))})
				continue
			}

			price := product.Price
			if value, ok := lookup(row, index, "price"); ok && value != "" {
				parsed, err := core.ParseMoney(value, customer.Currency)
				if err != nil {
					result.Skipped++
					result.addProblem(RowProblem{Line: line, SKU: poNumber,
						Message: fmt.Sprintf("price %q is not a valid amount", value)})
					continue
				}
				price = parsed
			}

			notes, _ := lookup(row, index, "notes")
			group.lines = append(group.lines, core.StoreOrderLine{
				ProductID: product.ID, Quantity: quantity, UnitPrice: price, Notes: notes,
			})
		}

		for _, poNumber := range order {
			group := grouped[poNumber]
			if group.broken || len(group.lines) == 0 {
				continue
			}

			existing, err := st.Orders().GetByPONumber(ctx, customerID, poNumber)
			switch {
			case err == nil && !opts.UpdateExisting:
				result.addProblem(RowProblem{Line: group.firstRow, SKU: poNumber,
					Message: "PO already exists; enable \"update existing\" to replace its lines"})
				continue
			case err != nil && !errors.Is(err, core.ErrNotFound):
				return err
			}

			if err == nil {
				group.header.ID = existing.ID
				group.header.Version = existing.Version
				group.header.Status = existing.Status
				group.header.CreatedAt = existing.CreatedAt
				group.header.OrderedAt = existing.OrderedAt

				if updateErr := st.Orders().Update(ctx, &group.header); updateErr != nil {
					result.addProblem(RowProblem{Line: group.firstRow, SKU: poNumber,
						Message: importErrorText(updateErr)})
					continue
				}
				if replaceErr := st.Orders().ReplaceLines(ctx, group.header.ID, group.lines); replaceErr != nil {
					result.addProblem(RowProblem{Line: group.firstRow, SKU: poNumber,
						Message: importErrorText(replaceErr)})
					continue
				}
				result.Updated++
				continue
			}

			if createErr := st.Orders().Create(ctx, &group.header, group.lines); createErr != nil {
				result.addProblem(RowProblem{Line: group.firstRow, SKU: poNumber,
					Message: importErrorText(createErr)})
				continue
			}
			result.Created++
		}
		return nil
	}

	if err := s.runImport(ctx, apply, opts.DryRun); err != nil {
		return result, err
	}
	if !opts.DryRun {
		s.log.Info("order import complete",
			"customer_id", customerID, "created", result.Created, "updated", result.Updated)
	}
	return result, nil
}

// resolveImportProduct finds the product a row refers to, by SKU or barcode.
func resolveImportProduct(ctx context.Context, st storage.Store, row []string, index map[string]int) (core.Product, error) {
	if sku, ok := lookup(row, index, "sku"); ok && sku != "" {
		product, err := st.Products().GetBySKU(ctx, sku)
		if err == nil {
			return product, nil
		}
		if !errors.Is(err, core.ErrNotFound) {
			return core.Product{}, err
		}
		return core.Product{}, fmt.Errorf("no product has the SKU %q", sku)
	}
	if barcode, ok := lookup(row, index, "barcode"); ok && barcode != "" {
		product, err := st.Products().GetByBarcode(ctx, barcode)
		if err == nil {
			return product, nil
		}
		if !errors.Is(err, core.ErrNotFound) {
			return core.Product{}, err
		}
		return core.Product{}, fmt.Errorf("no product has the barcode %q", barcode)
	}
	return core.Product{}, errors.New("the row names no product")
}

// importDateLayouts are the date formats a spreadsheet realistically contains.
// Day-first and month-first are both listed, which is genuinely ambiguous for
// dates below the 13th — so ISO is tried first and the rest are a fallback.
var importDateLayouts = []string{
	"2006-01-02", "2006/01/02", "01/02/2006", "1/2/2006",
	"02-Jan-2006", "2 Jan 2006", "Jan 2, 2006", "02.01.2006",
}

func parseImportDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	for _, layout := range importDateLayouts {
		if when, err := time.Parse(layout, s); err == nil {
			return when.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("date %q is not one this importer recognises; use YYYY-MM-DD", s)
}

// runImport applies an import inside one transaction, rolling back for a dry
// run so the preview exercises every write without keeping any of them.
func (s *Inventory) runImport(ctx context.Context, apply func(storage.Store) error, dryRun bool) error {
	if !dryRun {
		return s.store.InTx(ctx, apply)
	}

	sentinel := errors.New("dry run")
	err := s.store.InTx(ctx, func(st storage.Store) error {
		if err := apply(st); err != nil {
			return err
		}
		return sentinel
	})
	if err != nil && !errors.Is(err, sentinel) {
		return err
	}
	return nil
}

// readCSV reads a whole file, separating the header from the rows.
func readCSV(r io.Reader) ([]string, [][]string, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	header, err := reader.Read()
	if err == io.EOF {
		return nil, nil, fmt.Errorf("import: %w: the file is empty", core.ErrInvalid)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("import: reading the header row: %w", err)
	}

	rows, err := readAllRows(reader)
	if err != nil {
		return nil, nil, err
	}
	return header, rows, nil
}

// mapHeaderWith resolves a file's headings using a given alias table.
func mapHeaderWith(header []string, aliases map[string]string) (map[string]int, ImportResult) {
	index := map[string]int{}
	var result ImportResult

	for i, raw := range header {
		canonical, ok := aliases[normalizeHeader(raw)]
		if !ok {
			if strings.TrimSpace(raw) != "" {
				result.Ignored = append(result.Ignored, strings.TrimSpace(raw))
			}
			continue
		}
		if _, taken := index[canonical]; !taken {
			index[canonical] = i
			result.Mapped = append(result.Mapped, strings.TrimSpace(raw))
		}
	}
	return index, result
}

// StoresCSVTemplate writes a blank store list with the expected headings.
func StoresCSVTemplate(w io.Writer) error {
	return writeTemplate(w, storeCSVColumns, []string{
		"0047", "Herald Square", "151 W 34th St", "Receiving Dock B", "New York",
		"NY", "10001", "USA", "Dock Supervisor", "dock47@example.com", "+1 212 555 0100",
		"Appointment required 24h ahead. GS1-128 carton labels.", "Active",
	})
}

// OrdersCSVTemplate writes a blank order sheet with the expected headings.
func OrdersCSVTemplate(w io.Writer) error {
	return writeTemplate(w, orderCSVColumns, []string{
		"MCY-0123", "0047", "2026-10-01", "2026-10-15", "FW26-THROWS",
		"THROW-1", "240", "12.50", "Ship complete",
	})
}

func writeTemplate(w io.Writer, columns, example []string) error {
	writer := csv.NewWriter(w)
	if err := writer.Write(columns); err != nil {
		return err
	}
	if err := writer.Write(example); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}
