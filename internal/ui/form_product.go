package ui

import (
	"context"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
)

// productForm creates and edits products.
//
// The fields are grouped rather than listed: identification, pricing, stock
// control, then the rest. A flat list of eighteen inputs is a form nobody
// completes correctly, and the groups match the questions someone is actually
// answering.
type productForm struct {
	app *App
	// existing is nil when creating.
	existing *core.ProductWithStock

	sku         *widget.Entry
	barcode     *widget.Entry
	name        *widget.Entry
	description *widget.Entry
	category    *widget.SelectEntry
	supplier    *widget.SelectEntry
	tags        *widget.Entry
	notes       *widget.Entry

	unit  *widget.Select
	price *widget.Entry
	cost  *widget.Entry

	opening         *widget.Entry
	reorderPoint    *widget.Entry
	reorderQuantity *widget.Entry
	weight          *widget.Entry

	nonStock  *widget.Check
	trackLots *widget.Check

	marginLabel *widget.Label
}

// openProductForm shows the create or edit dialog. Pass nil to create.
func (a *App) openProductForm(existing *core.ProductWithStock) {
	form := &productForm{app: a, existing: existing}

	// Category and supplier autocomplete from what is already in use, which is
	// how a catalogue keeps a handful of tidy categories instead of nine
	// spellings of the same one.
	a.background(func(ctx context.Context) {
		categories, _ := a.svc.Categories(ctx)
		suppliers, _ := a.svc.Suppliers(ctx)
		a.onMain(func() { form.show(categories, suppliers) })
	})
}

func (f *productForm) show(categories, suppliers []string) {
	f.build(categories, suppliers)

	title := "New product"
	confirm := "Create"
	if f.existing != nil {
		title = "Edit " + f.existing.SKU
		confirm = "Save changes"
	}

	d := dialog.NewCustomConfirm(title, confirm, "Cancel", f.content(), f.submit, f.app.window)
	d.Resize(fyne.NewSize(720, 640))
	d.Show()

	f.app.window.Canvas().Focus(f.sku)
}

func (f *productForm) build(categories, suppliers []string) {
	f.sku = newEntry("Required — the code you use for this item")
	f.barcode = newEntry("Scan or type the barcode printed on the label")
	f.name = newEntry("Required")
	f.description = widget.NewMultiLineEntry()
	f.description.SetPlaceHolder("What this item is")
	f.description.Wrapping = fyne.TextWrapWord

	f.category = widget.NewSelectEntry(categories)
	f.category.SetPlaceHolder("e.g. Fasteners")
	f.supplier = widget.NewSelectEntry(suppliers)
	f.supplier.SetPlaceHolder("Who you buy it from")

	f.tags = newEntry("Comma separated, e.g. fragile, bestseller")
	f.notes = widget.NewMultiLineEntry()
	f.notes.SetPlaceHolder("Anything else worth recording")
	f.notes.Wrapping = fyne.TextWrapWord

	unitLabels := make([]string, len(core.Units))
	for i, u := range core.Units {
		unitLabels[i] = u.Label()
	}
	f.unit = widget.NewSelect(unitLabels, nil)
	f.unit.SetSelectedIndex(0)

	f.price = newEntry("0.00")
	f.cost = newEntry("0.00")
	f.marginLabel = widget.NewLabel("")
	f.marginLabel.Importance = widget.LowImportance

	// Margin updates as the two amounts are typed, so a pricing mistake is
	// visible while it is being made rather than in a report next month.
	f.price.OnChanged = func(string) { f.updateMargin() }
	f.cost.OnChanged = func(string) { f.updateMargin() }

	f.opening = newEntry("0")
	f.reorderPoint = newEntry("0 — no reorder alert")
	f.reorderQuantity = newEntry("How much to buy when it runs low")
	f.weight = newEntry("Grams, for shipping")

	f.nonStock = widget.NewCheck("Non-stock item (a service or anything without a quantity)", nil)
	f.trackLots = widget.NewCheck("Track lot or batch numbers and expiry dates", nil)

	f.nonStock.OnChanged = func(bool) { f.applyNonStock() }

	if f.existing != nil {
		f.populate(*f.existing)
	}
	f.applyNonStock()
	f.updateMargin()
}

func (f *productForm) populate(p core.ProductWithStock) {
	f.sku.SetText(p.SKU)
	f.barcode.SetText(p.Barcode)
	f.name.SetText(p.Name)
	f.description.SetText(p.Description)
	f.category.SetText(p.Category)
	f.supplier.SetText(p.Supplier)
	f.tags.SetText(p.Tags.String())
	f.notes.SetText(p.Notes)
	f.price.SetText(p.Price.String())
	f.cost.SetText(p.Cost.String())
	f.reorderPoint.SetText(strconv.FormatInt(p.ReorderPoint, 10))
	f.reorderQuantity.SetText(strconv.FormatInt(p.ReorderQuantity, 10))
	if p.WeightGrams > 0 {
		f.weight.SetText(strconv.FormatInt(p.WeightGrams, 10))
	}
	f.nonStock.SetChecked(p.NonStock)
	f.trackLots.SetChecked(p.TrackLots)

	for i, u := range core.Units {
		if u == p.Unit.Normalize() {
			f.unit.SetSelectedIndex(i)
			break
		}
	}

	// Editing must not offer to re-set the opening balance: stock is changed
	// through receive, issue and count, each of which records why.
	f.opening.SetText(formatQuantity(p.OnHand))
	f.opening.Disable()
}

// applyNonStock disables the stock fields for an item that has no quantity,
// rather than leaving inputs on screen that quietly do nothing.
func (f *productForm) applyNonStock() {
	stockFields := []*widget.Entry{f.reorderPoint, f.reorderQuantity}
	if f.existing == nil {
		stockFields = append(stockFields, f.opening)
	}

	for _, field := range stockFields {
		if f.nonStock.Checked {
			field.SetText("")
			field.Disable()
		} else {
			field.Enable()
		}
	}
	if f.nonStock.Checked {
		f.trackLots.SetChecked(false)
		f.trackLots.Disable()
	} else {
		f.trackLots.Enable()
	}
}

func (f *productForm) updateMargin() {
	price, priceErr := core.ParseMoney(f.price.Text, f.app.currency)
	cost, costErr := core.ParseMoney(f.cost.Text, f.app.currency)

	if priceErr != nil || costErr != nil || price.IsZero() {
		f.marginLabel.SetText("")
		return
	}

	margin, err := price.Sub(cost)
	if err != nil {
		f.marginLabel.SetText("")
		return
	}

	percent := float64(margin.Minor) / float64(price.Minor) * 100
	f.marginLabel.Importance = widget.SuccessImportance
	if margin.IsNegative() {
		f.marginLabel.Importance = widget.DangerImportance
	}
	f.marginLabel.SetText(sprintf("Margin %s  (%.1f%%)", margin.Display(), percent))
	f.marginLabel.Refresh()
}

func (f *productForm) content() fyne.CanvasObject {
	identity := container.New(newFormGrid())
	formRow(identity, "SKU", f.sku)
	formRow(identity, "Barcode", f.barcode)
	formRow(identity, "Name", f.name)
	formRow(identity, "Category", f.category)
	formRow(identity, "Supplier", f.supplier)
	formRow(identity, "Unit", f.unit)
	formRow(identity, "Tags", f.tags)

	pricing := container.New(newFormGrid())
	formRow(pricing, "Unit cost", f.cost)
	formRow(pricing, "Sale price", f.price)
	formRow(pricing, "", f.marginLabel)

	stock := container.New(newFormGrid())
	if f.existing == nil {
		formRow(stock, "Opening stock", f.opening)
	} else {
		formRow(stock, "On hand", f.opening)
	}
	formRow(stock, "Reorder point", f.reorderPoint)
	formRow(stock, "Reorder quantity", f.reorderQuantity)
	formRow(stock, "Weight (g)", f.weight)

	more := container.New(newFormGrid())
	formRow(more, "Description", f.description)
	formRow(more, "Notes", f.notes)

	body := container.NewVBox(
		identity,
		sectionHeading("Pricing"),
		pricing,
		sectionHeading("Stock control"),
		stock,
		f.nonStock,
		f.trackLots,
		sectionHeading("More"),
		more,
	)
	return container.NewVScroll(container.NewPadded(body))
}

func (f *productForm) submit(confirmed bool) {
	if !confirmed {
		return
	}

	product, opening, err := f.read()
	if err != nil {
		f.app.showError(err)
		return
	}

	if f.existing == nil {
		f.app.createProduct(product, opening)
		return
	}
	f.app.saveProduct(product)
}

// read turns the form into a product, reporting every field problem at once.
func (f *productForm) read() (core.Product, service.OpeningStock, error) {
	var problems core.ValidationError

	price, err := core.ParseMoney(orZero(f.price.Text), f.app.currency)
	if err != nil {
		problems.Add("price", "Sale price is not a valid amount")
	}
	cost, err := core.ParseMoney(orZero(f.cost.Text), f.app.currency)
	if err != nil {
		problems.Add("cost", "Unit cost is not a valid amount")
	}

	reorderPoint, err := parseQuantity(f.reorderPoint.Text)
	if err != nil {
		problems.Add("reorder_point", "Reorder point must be a whole number")
	}
	reorderQuantity, err := parseQuantity(f.reorderQuantity.Text)
	if err != nil {
		problems.Add("reorder_quantity", "Reorder quantity must be a whole number")
	}
	weight, err := parseQuantity(f.weight.Text)
	if err != nil {
		problems.Add("weight", "Weight must be a whole number of grams")
	}

	var opening service.OpeningStock
	if f.existing == nil && !f.nonStock.Checked {
		quantity, err := parseQuantity(f.opening.Text)
		if err != nil {
			problems.Add("opening_stock", "Opening stock must be a whole number")
		} else if quantity < 0 {
			problems.Add("opening_stock", "Opening stock cannot be negative")
		}
		opening.Quantity = quantity
		opening.UnitCost = cost
		opening.LocationID = f.app.location
	}

	if err := problems.ErrOrNil(); err != nil {
		return core.Product{}, service.OpeningStock{}, err
	}

	product := core.Product{
		SKU:             strings.TrimSpace(f.sku.Text),
		Barcode:         strings.TrimSpace(f.barcode.Text),
		Name:            strings.TrimSpace(f.name.Text),
		Description:     f.description.Text,
		Category:        strings.TrimSpace(f.category.Text),
		Supplier:        strings.TrimSpace(f.supplier.Text),
		Tags:            core.ParseTags(f.tags.Text),
		Notes:           f.notes.Text,
		Price:           price,
		Cost:            cost,
		Unit:            f.selectedUnit(),
		NonStock:        f.nonStock.Checked,
		TrackLots:       f.trackLots.Checked,
		ReorderPoint:    reorderPoint,
		ReorderQuantity: reorderQuantity,
		WeightGrams:     weight,
		Active:          true,
	}

	if f.existing != nil {
		product.ID = f.existing.ID
		product.Version = f.existing.Version
		product.CreatedAt = f.existing.CreatedAt
		product.Active = f.existing.Active
		product.ImagePath = f.existing.ImagePath
		product.CustomFields = f.existing.CustomFields
	}
	return product, opening, nil
}

func (f *productForm) selectedUnit() core.UnitOfMeasure {
	i := f.unit.SelectedIndex()
	if i < 0 || i >= len(core.Units) {
		return core.DefaultUnit
	}
	return core.Units[i]
}

func newEntry(placeholder string) *widget.Entry {
	entry := widget.NewEntry()
	entry.SetPlaceHolder(placeholder)
	return entry
}

// orZero substitutes "0" for a blank amount, so leaving a price empty means
// free rather than invalid.
func orZero(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}
