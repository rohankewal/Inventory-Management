package ui

import (
	"context"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// orderForm creates and edits one store purchase order.
//
// The line editor is deliberately simple: most orders arrive as a spreadsheet
// and are imported. Typing one by hand is the exception, so the form optimises
// for correctness — the store list narrows to the chosen customer, and each
// line shows whether there is stock to cover it.
type orderForm struct {
	app      *App
	existing *core.OrderDetail

	customers []core.CustomerWithStores
	stores    []core.CustomerStore
	programs  []core.Program
	products  []core.ProductWithStock

	customerSel *widget.Select
	storeSel    *widget.Select
	programSel  *widget.Select
	poEntry     *widget.Entry
	shipEntry   *widget.Entry
	cancelEntry *widget.Entry
	notesEntry  *widget.Entry

	linesBox   *fyne.Container
	lineRows   []*orderLineEditor
	totalLabel *widget.Label
}

// orderLineEditor is one editable line.
type orderLineEditor struct {
	productSel *widget.Select
	quantity   *widget.Entry
	price      *widget.Entry
	coverage   *widget.Label
	row        fyne.CanvasObject
	// existing carries the identity of a line already saved, so editing an
	// order does not discard shipped quantities.
	existing *core.StoreOrderLine
}

// openOrderForm creates or edits an order. Pass nil to create.
func (a *App) openOrderForm(existing *core.OrderDetail) {
	form := &orderForm{app: a, existing: existing}

	a.background(func(ctx context.Context) {
		customers, custErr := a.svc.ListCustomers(ctx, storage.CustomerFilter{})
		products, prodErr := a.svc.ListProducts(ctx, storage.ProductFilter{
			Sort: storage.SortProductSKU, Limit: 2000,
		})

		a.onMain(func() {
			if custErr != nil || prodErr != nil {
				a.showError(wrapf(firstError(custErr, prodErr), "the order form could not be opened"))
				return
			}
			if len(customers.Items) == 0 {
				a.showInfoDialog("New order", wrappedLabel(
					"There are no customers yet. Add a customer and its stores first — "+
						"an order has to ship somewhere."))
				return
			}
			if len(products.Items) == 0 {
				a.showInfoDialog("New order", wrappedLabel(
					"There are no products yet. Add or import products before raising an order."))
				return
			}

			form.customers = customers.Items
			form.products = products.Items
			form.show()
		})
	})
}

// openOrderByID loads an order and opens it for editing.
func (a *App) openOrderByID(id core.ID) {
	a.background(func(ctx context.Context) {
		detail, err := a.svc.GetOrder(ctx, id)
		a.onMain(func() {
			if err != nil {
				a.showError(wrapf(err, "the order could not be opened"))
				return
			}
			a.openOrderForm(&detail)
		})
	})
}

func (f *orderForm) show() {
	f.build()

	title, confirm := "New order", "Create"
	if f.existing != nil {
		title, confirm = "Edit "+f.existing.CustomerPONumber, "Save changes"
	}

	d := dialog.NewCustomConfirm(title, confirm, "Cancel", f.content(), f.submit, f.app.window)
	d.Resize(fyne.NewSize(880, 700))
	d.Show()
	f.app.window.Canvas().Focus(f.poEntry)
}

func (f *orderForm) build() {
	names := make([]string, len(f.customers))
	for i, c := range f.customers {
		names[i] = c.Name
	}

	f.customerSel = widget.NewSelect(names, nil)
	f.customerSel.PlaceHolder = "Choose a customer"

	f.storeSel = widget.NewSelect(nil, nil)
	f.storeSel.PlaceHolder = "Choose a store"

	f.programSel = widget.NewSelect(nil, nil)
	f.programSel.PlaceHolder = "No program"

	f.poEntry = newEntry("The client's PO number, e.g. MCY-0123")
	f.shipEntry = newEntry("YYYY-MM-DD")
	f.cancelEntry = newEntry("YYYY-MM-DD")

	f.notesEntry = widget.NewMultiLineEntry()
	f.notesEntry.SetPlaceHolder("Anything the warehouse needs to know")
	f.notesEntry.Wrapping = fyne.TextWrapWord

	f.linesBox = container.NewVBox()
	f.totalLabel = widget.NewLabel("")
	f.totalLabel.TextStyle = fyne.TextStyle{Bold: true}

	// Changing the customer must re-scope the store and program lists, or an
	// order can be pointed at another client's door.
	f.customerSel.OnChanged = func(string) { f.loadCustomerLists() }

	if f.existing != nil {
		f.populate()
	} else {
		f.addLine(nil)
	}
}

func (f *orderForm) populate() {
	detail := f.existing

	for i, c := range f.customers {
		if c.ID == detail.CustomerID {
			f.customerSel.SetSelectedIndex(i)
			break
		}
	}
	f.poEntry.SetText(detail.CustomerPONumber)
	if !detail.RequestedShipDate.IsZero() {
		f.shipEntry.SetText(detail.RequestedShipDate.Format("2006-01-02"))
	}
	if !detail.CancelAfterDate.IsZero() {
		f.cancelEntry.SetText(detail.CancelAfterDate.Format("2006-01-02"))
	}
	f.notesEntry.SetText(detail.Notes)

	// The customer cannot change on an existing order: the PO number is unique
	// per customer and the store belongs to one, so moving it would invalidate
	// both.
	f.customerSel.Disable()

	for i := range detail.Lines {
		f.addLine(&detail.Lines[i])
	}
}

// loadCustomerLists narrows the store and program pickers to the chosen client.
func (f *orderForm) loadCustomerLists() {
	customer := f.selectedCustomer()
	if customer == nil {
		return
	}

	f.app.background(func(ctx context.Context) {
		stores, storeErr := f.app.svc.ListStores(ctx, storage.StoreFilter{CustomerID: customer.ID})
		programs, _ := f.app.svc.ListPrograms(ctx, storage.ProgramFilter{
			CustomerID: customer.ID, OpenOnly: true,
		})

		f.app.onMain(func() {
			if storeErr != nil {
				f.app.showError(storeErr)
				return
			}

			f.stores = stores
			labels := make([]string, len(stores))
			for i, s := range stores {
				labels[i] = s.Label()
			}
			f.storeSel.Options = labels
			f.storeSel.Refresh()

			f.programs = programs
			programLabels := make([]string, len(programs))
			for i, p := range programs {
				programLabels[i] = p.Code + " — " + p.Name
			}
			f.programSel.Options = programLabels
			f.programSel.Refresh()

			// Restore the saved selections once the lists exist.
			if f.existing != nil {
				for i, s := range stores {
					if s.ID == f.existing.StoreID {
						f.storeSel.SetSelectedIndex(i)
						break
					}
				}
				for i, p := range programs {
					if p.ID == f.existing.ProgramID {
						f.programSel.SetSelectedIndex(i)
						break
					}
				}
			} else if len(stores) == 1 {
				f.storeSel.SetSelectedIndex(0)
			}

			if len(stores) == 0 {
				f.app.status.warn("%s has no stores yet — add one before raising an order.", customer.Name)
			}
		})
	})
}

func (f *orderForm) selectedCustomer() *core.CustomerWithStores {
	i := f.customerSel.SelectedIndex()
	if i < 0 || i >= len(f.customers) {
		return nil
	}
	return &f.customers[i]
}

func (f *orderForm) addLine(existing *core.OrderLineDetail) {
	labels := make([]string, len(f.products))
	for i, p := range f.products {
		labels[i] = p.SKU + " — " + p.Name
	}

	editor := &orderLineEditor{
		productSel: widget.NewSelect(labels, nil),
		quantity:   newEntry("0"),
		price:      newEntry("0.00"),
		coverage:   widget.NewLabel(""),
	}
	editor.productSel.PlaceHolder = "Choose a product"
	editor.coverage.Importance = widget.LowImportance
	editor.coverage.SizeName = theme.SizeNameCaptionText

	remove := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() { f.removeLine(editor) })
	remove.Importance = widget.LowImportance

	editor.row = container.NewBorder(nil, nil, nil, remove,
		container.NewVBox(
			container.NewBorder(nil, nil, nil,
				container.NewHBox(
					container.NewGridWrap(fyne.NewSize(110, 36), editor.quantity),
					container.NewGridWrap(fyne.NewSize(120, 36), editor.price),
				),
				editor.productSel,
			),
			editor.coverage,
		),
	)

	if existing != nil {
		editor.existing = &existing.StoreOrderLine
		for i, p := range f.products {
			if p.ID == existing.ProductID {
				editor.productSel.SetSelectedIndex(i)
				break
			}
		}
		editor.quantity.SetText(formatQuantity(existing.Quantity))
		editor.price.SetText(existing.UnitPrice.String())
	}

	update := func(string) { f.refreshLine(editor) }
	editor.productSel.OnChanged = update
	editor.quantity.OnChanged = update
	editor.price.OnChanged = update

	f.lineRows = append(f.lineRows, editor)
	f.linesBox.Add(editor.row)
	f.linesBox.Refresh()
	f.refreshLine(editor)
}

func (f *orderForm) removeLine(editor *orderLineEditor) {
	// A line that has already shipped cannot be dropped; the store refuses the
	// edit anyway, and saying so here is much clearer than failing on save.
	if editor.existing != nil && editor.existing.ShippedQty > 0 {
		f.app.status.warn("That line has already shipped and cannot be removed.")
		return
	}
	if len(f.lineRows) == 1 {
		f.app.status.warn("An order needs at least one line.")
		return
	}

	for i, row := range f.lineRows {
		if row == editor {
			f.lineRows = append(f.lineRows[:i], f.lineRows[i+1:]...)
			break
		}
	}
	f.linesBox.Remove(editor.row)
	f.linesBox.Refresh()
	f.refreshTotal()
}

// refreshLine shows whether the line can actually be shipped, as it is typed.
func (f *orderForm) refreshLine(editor *orderLineEditor) {
	product := f.lineProduct(editor)
	if product == nil {
		editor.coverage.SetText("")
		f.refreshTotal()
		return
	}

	// Default the price to the product's own, so a hand-typed order does not
	// silently go out at zero.
	if strings.TrimSpace(editor.price.Text) == "" || editor.price.Text == "0.00" {
		editor.price.SetText(product.Price.String())
	}

	quantity, err := parseQuantity(editor.quantity.Text)
	if err != nil {
		editor.coverage.SetText("Quantity must be a whole number.")
		editor.coverage.Importance = widget.DangerImportance
		editor.coverage.Refresh()
		f.refreshTotal()
		return
	}

	switch {
	case quantity <= 0:
		editor.coverage.SetText(sprintf("%s on hand", formatQuantity(product.OnHand)))
		editor.coverage.Importance = widget.LowImportance
	case product.OnHand < quantity:
		editor.coverage.SetText(sprintf("short %s — only %s on hand",
			formatQuantity(quantity-product.OnHand), formatQuantity(product.OnHand)))
		editor.coverage.Importance = widget.WarningImportance
	default:
		editor.coverage.SetText(sprintf("covered — %s on hand", formatQuantity(product.OnHand)))
		editor.coverage.Importance = widget.SuccessImportance
	}
	editor.coverage.Refresh()
	f.refreshTotal()
}

func (f *orderForm) lineProduct(editor *orderLineEditor) *core.ProductWithStock {
	i := editor.productSel.SelectedIndex()
	if i < 0 || i >= len(f.products) {
		return nil
	}
	return &f.products[i]
}

func (f *orderForm) refreshTotal() {
	total := core.Zero(f.app.currency)
	var units int64

	for _, editor := range f.lineRows {
		quantity, err := parseQuantity(editor.quantity.Text)
		if err != nil || quantity <= 0 {
			continue
		}
		price, err := core.ParseMoney(orZero(editor.price.Text), f.app.currency)
		if err != nil {
			continue
		}
		units += quantity
		if price.Currency == total.Currency {
			total.Minor += price.MulQty(quantity).Minor
		}
	}

	f.totalLabel.SetText(sprintf("%s units  ·  %s", formatQuantity(units), total.Display()))
}

func (f *orderForm) content() fyne.CanvasObject {
	header := container.New(newFormGrid())
	formRow(header, "Customer", f.customerSel)
	formRow(header, "Ship to store", f.storeSel)
	formRow(header, "PO number", f.poEntry)
	formRow(header, "Program", f.programSel)
	formRow(header, "Requested ship date", f.shipEntry)
	formRow(header, "Cancel after", f.cancelEntry)

	addLine := widget.NewButtonWithIcon("Add line", theme.ContentAddIcon(), func() { f.addLine(nil) })

	return container.NewVScroll(container.NewPadded(container.NewVBox(
		header,
		sectionHeading("Lines"),
		f.linesBox,
		container.NewBorder(nil, nil, addLine, f.totalLabel),
		sectionHeading("Notes"),
		f.notesEntry,
	)))
}

func (f *orderForm) submit(confirmed bool) {
	if !confirmed {
		return
	}

	order, lines, err := f.read()
	if err != nil {
		f.app.showError(err)
		return
	}
	f.app.saveOrder(order, lines)
}

func (f *orderForm) read() (core.StoreOrder, []core.StoreOrderLine, error) {
	var problems core.ValidationError

	customer := f.selectedCustomer()
	if customer == nil {
		problems.Add("customer", "Choose a customer")
	}

	var storeID core.ID
	if i := f.storeSel.SelectedIndex(); i >= 0 && i < len(f.stores) {
		storeID = f.stores[i].ID
	} else {
		problems.Add("store", "Choose the store this order ships to")
	}

	var programID core.ID
	if i := f.programSel.SelectedIndex(); i >= 0 && i < len(f.programs) {
		programID = f.programs[i].ID
	}

	shipDate, err := parseFormDate(f.shipEntry.Text)
	if err != nil {
		problems.Add("ship_date", "Requested ship date must look like 2026-10-01")
	}
	cancelDate, err := parseFormDate(f.cancelEntry.Text)
	if err != nil {
		problems.Add("cancel_date", "Cancel date must look like 2026-10-15")
	}

	var lines []core.StoreOrderLine
	for i, editor := range f.lineRows {
		product := f.lineProduct(editor)
		if product == nil {
			problems.Add("lines", "Line %d has no product", i+1)
			continue
		}

		quantity, err := parseQuantity(editor.quantity.Text)
		if err != nil || quantity <= 0 {
			problems.Add("lines", "Line %d needs a quantity greater than zero", i+1)
			continue
		}
		price, err := core.ParseMoney(orZero(editor.price.Text), f.app.currency)
		if err != nil {
			problems.Add("lines", "Line %d has an invalid price", i+1)
			continue
		}

		line := core.StoreOrderLine{
			ProductID: product.ID, Quantity: quantity, UnitPrice: price, LineNo: i + 1,
		}
		// Carry forward what has already happened to a saved line, so editing
		// an order does not erase its fulfilment history.
		if editor.existing != nil {
			line.ID = editor.existing.ID
			line.ShippedQty = editor.existing.ShippedQty
			line.AllocatedQty = editor.existing.AllocatedQty
			line.CancelledQty = editor.existing.CancelledQty
			line.Notes = editor.existing.Notes
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		problems.Add("lines", "An order needs at least one line")
	}

	if err := problems.ErrOrNil(); err != nil {
		return core.StoreOrder{}, nil, err
	}

	order := core.StoreOrder{
		CustomerID:        customer.ID,
		StoreID:           storeID,
		ProgramID:         programID,
		CustomerPONumber:  strings.TrimSpace(f.poEntry.Text),
		RequestedShipDate: shipDate,
		CancelAfterDate:   cancelDate,
		Notes:             f.notesEntry.Text,
		Currency:          customer.Currency,
	}
	if f.existing != nil {
		order.ID = f.existing.ID
		order.Version = f.existing.Version
		order.Status = f.existing.Status
		order.CreatedAt = f.existing.CreatedAt
		order.OrderedAt = f.existing.OrderedAt
	}
	return order, lines, nil
}

// parseFormDate reads a date field, treating blank as unset.
func parseFormDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse("2006-01-02", s)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
