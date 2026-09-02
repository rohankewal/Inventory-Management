package ui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// pageSize is how many products one screenful of the catalogue loads.
const pageSize = 200

// searchDebounce is how long typing settles before a query runs. Without it,
// every keystroke in the search box is a database round trip.
const searchDebounce = 220 * time.Millisecond

// stockFilterOption pairs a menu label with the filter it applies.
type stockFilterOption struct {
	label string
	state storage.StockState
}

var stockFilterOptions = []stockFilterOption{
	{"All stock levels", storage.StockAny},
	{"Needs reordering", storage.StockNeedsReorder},
	{"Out of stock", storage.StockOut},
	{"In stock", storage.StockInStock},
	{"Negative stock", storage.StockNegative},
}

// productsView is the catalogue: the screen this application is mostly used
// through.
type productsView struct {
	app *App

	table   *DataTable[core.ProductWithStock]
	content fyne.CanvasObject

	searchBox    *widget.Entry
	categorySel  *widget.Select
	supplierSel  *widget.Select
	stockSel     *widget.Select
	archivedChk  *widget.Check
	summaryLabel *widget.Label
	clearButton  *widget.Button

	detail *productDetailPanel
	split  *container.Split

	filter storage.ProductFilter
	page   service.ProductPage

	debounce *time.Timer
	// generation guards against an older, slower query overwriting the results
	// of a newer one — the classic search race where letting go of the key
	// shows results for a prefix you already finished typing.
	generation int
}

func newProductsView(a *App) *productsView {
	v := &productsView{
		app: a,
		filter: storage.ProductFilter{
			Sort:  storage.SortProductSKU,
			Limit: pageSize,
		},
	}
	v.build()
	return v
}

func (v *productsView) title() string { return "Products" }

func (v *productsView) object() fyne.CanvasObject { return v.content }

func (v *productsView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("Import", theme.UploadIcon(), widget.MediumImportance, v.app.openImportDialog),
		toolbarButton("Export", theme.DownloadIcon(), widget.MediumImportance, v.app.openExportDialog),
		toolbarButton("New product", theme.ContentAddIcon(), widget.HighImportance,
			func() { v.app.openProductForm(nil) }),
	}
}

func (v *productsView) build() {
	v.table = NewDataTable(productColumns(v.app.currency))
	v.table.SetSort(string(storage.SortProductSKU), false)
	v.table.OnSortChanged = func(key string, descending bool) {
		v.filter.Sort = storage.ProductSort(key)
		v.filter.Direction = sortDirection(descending)
		v.filter.Offset = 0
		v.reload()
	}
	v.table.OnSelect = func(row core.ProductWithStock, _ int) {
		v.detail.show(row)
		v.showDetail(true)
	}
	v.table.OnActivate = func(row core.ProductWithStock, _ int) { v.app.openProductForm(&row) }

	v.detail = newProductDetailPanel(v.app)

	// The detail panel sits beside the list rather than in a dialog, so that
	// clicking down a list of items to compare them does not mean opening and
	// closing a modal for each one. It collapses when nothing is selected,
	// because a quarter of the window explaining that nothing is selected is a
	// quarter of the window the table could have used.
	v.split = container.NewHSplit(v.table.Object(), v.detail.object())
	v.showDetail(false)

	v.content = container.NewBorder(v.buildFilterBar(), v.buildFooter(), nil, nil, v.split)
}

// detailSplitOffset is how much width the table keeps when the inspector is
// open.
const detailSplitOffset = 0.7

// showDetail opens or collapses the inspector.
func (v *productsView) showDetail(open bool) {
	if v.split == nil {
		return
	}
	if open {
		v.detail.object().Show()
		v.split.SetOffset(detailSplitOffset)
		return
	}
	v.detail.clear()
	v.detail.object().Hide()
	v.split.SetOffset(1)
}

func (v *productsView) buildFilterBar() fyne.CanvasObject {
	// Every widget is created before any callback is attached. A Fyne setter
	// such as SetSelectedIndex fires its change handler synchronously, so
	// wiring callbacks as the widgets are built means one of them runs against
	// a half-constructed view.
	v.searchBox = widget.NewEntry()
	v.searchBox.SetPlaceHolder("Filter by SKU, name or barcode")

	v.categorySel = widget.NewSelect(nil, nil)
	v.categorySel.PlaceHolder = "All categories"

	v.supplierSel = widget.NewSelect(nil, nil)
	v.supplierSel.PlaceHolder = "All suppliers"

	labels := make([]string, len(stockFilterOptions))
	for i, option := range stockFilterOptions {
		labels[i] = option.label
	}
	v.stockSel = widget.NewSelect(labels, nil)
	v.stockSel.SetSelectedIndex(0)

	v.archivedChk = widget.NewCheck("Show archived", nil)

	// Now that they all exist, they can safely talk to each other.
	v.searchBox.OnChanged = func(string) { v.reloadDebounced() }
	v.categorySel.OnChanged = func(string) { v.applyFacets() }
	v.supplierSel.OnChanged = func(string) { v.applyFacets() }
	v.stockSel.OnChanged = func(string) { v.applyFacets() }
	v.archivedChk.OnChanged = func(bool) { v.applyFacets() }

	v.clearButton = widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), v.clearFilters)
	v.clearButton.Importance = widget.LowImportance

	filters := container.NewHBox(
		container.NewGridWrap(fyne.NewSize(180, 36), v.categorySel),
		container.NewGridWrap(fyne.NewSize(180, 36), v.supplierSel),
		container.NewGridWrap(fyne.NewSize(180, 36), v.stockSel),
		v.archivedChk,
		v.clearButton,
	)

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, filters, v.searchBox),
		widget.NewSeparator(),
	)
}

func (v *productsView) buildFooter() fyne.CanvasObject {
	v.summaryLabel = widget.NewLabel("")
	v.summaryLabel.Importance = widget.LowImportance

	return container.NewVBox(
		widget.NewSeparator(),
		container.NewBorder(nil, nil, v.summaryLabel, nil),
	)
}

// setSearch drives the list from elsewhere, such as a scan that found no
// matching code.
func (v *productsView) setSearch(term string) {
	v.searchBox.SetText(term)
	v.reload()
}

func (v *productsView) applyFacets() {
	v.filter.Category = selectedOrEmpty(v.categorySel)
	v.filter.Supplier = selectedOrEmpty(v.supplierSel)
	v.filter.IncludeInactive = v.archivedChk.Checked

	v.filter.Stock = storage.StockAny
	if i := v.stockSel.SelectedIndex(); i >= 0 && i < len(stockFilterOptions) {
		v.filter.Stock = stockFilterOptions[i].state
	}

	v.filter.Offset = 0
	v.reload()
}

func (v *productsView) clearFilters() {
	v.searchBox.SetText("")
	v.categorySel.ClearSelected()
	v.supplierSel.ClearSelected()
	v.stockSel.SetSelectedIndex(0)
	v.archivedChk.SetChecked(false)
	v.applyFacets()
}

// reloadDebounced waits for typing to settle before querying.
func (v *productsView) reloadDebounced() {
	if v.debounce != nil {
		v.debounce.Stop()
	}
	v.debounce = time.AfterFunc(searchDebounce, func() {
		if v.app.ctx.Err() != nil {
			return
		}
		v.app.onMain(v.reload)
	})
}

func (v *productsView) reload() {
	v.filter.Search = v.searchBox.Text
	v.filter.LocationID = v.app.location
	v.filter.Limit = pageSize

	v.generation++
	generation := v.generation
	filter := v.filter

	v.app.background(func(ctx context.Context) {
		page, pageErr := v.app.svc.ListProducts(ctx, filter)
		categories, _ := v.app.svc.Categories(ctx)
		suppliers, _ := v.app.svc.Suppliers(ctx)

		v.app.onMain(func() {
			// A response from a query the user has already moved past must not
			// replace what is on screen now.
			if generation != v.generation {
				return
			}
			if pageErr != nil {
				v.app.showError(wrapf(pageErr, "the catalogue could not be loaded"))
				return
			}

			v.page = page
			v.table.SetSort(string(filter.Sort), filter.Direction == storage.Descending)
			v.table.SetRows(page.Items, v.emptyMessage())
			v.summaryLabel.SetText(v.summary(page))
			v.refreshFacetOptions(categories, suppliers)
			v.showDetail(false)
		})
	})
}

func (v *productsView) summary(page service.ProductPage) string {
	summary := describeCount(len(page.Items), page.Total)
	if page.Total > len(page.Items) {
		summary += " — narrow the filters to see the rest"
	}
	return summary
}

// emptyMessage explains why the list is empty, which is different depending on
// whether the catalogue is empty or the filters simply exclude everything.
func (v *productsView) emptyMessage() string {
	if v.filter.Search == "" && v.filter.Category == "" &&
		v.filter.Supplier == "" && v.filter.Stock == storage.StockAny {
		return "No products yet.\n\nAdd one with ⌘N, or import a CSV from the File menu."
	}
	return "No products match these filters.\n\nTry clearing them."
}

func (v *productsView) refreshFacetOptions(categories, suppliers []string) {
	setSelectOptions(v.categorySel, categories)
	setSelectOptions(v.supplierSel, suppliers)
}

// setSelectOptions replaces a dropdown's choices while keeping the current
// selection if it still exists.
func setSelectOptions(sel *widget.Select, options []string) {
	current := sel.Selected
	sel.Options = options
	sel.Refresh()

	if current == "" {
		return
	}
	for _, option := range options {
		if option == current {
			return
		}
	}
	sel.ClearSelected()
}

func selectedOrEmpty(sel *widget.Select) string {
	if sel == nil {
		return ""
	}
	return sel.Selected
}

// productColumns defines the catalogue grid.
func productColumns(currency core.Currency) []Column[core.ProductWithStock] {
	return []Column[core.ProductWithStock]{
		{
			Title: "SKU", Width: 140, SortKey: string(storage.SortProductSKU),
			Monospace: true,
			Value:     func(p core.ProductWithStock) string { return p.SKU },
			Bold:      func(core.ProductWithStock) bool { return true },
		},
		{
			Title: "Name", Width: 260, SortKey: string(storage.SortProductName),
			Value: func(p core.ProductWithStock) string { return p.Name },
		},
		{
			Title: "Category", Width: 130, SortKey: string(storage.SortProductCategory),
			Value: func(p core.ProductWithStock) string { return dash(p.Category) },
		},
		{
			// The unit belongs with the number it qualifies: "420 pack" is one
			// fact, and splitting it across two columns costs width without
			// making anything clearer.
			Title: "On hand", Width: 130, Align: fyne.TextAlignTrailing,
			SortKey: string(storage.SortProductStock), Monospace: true,
			Value: func(p core.ProductWithStock) string {
				if p.NonStock {
					return "—"
				}
				return formatQuantity(p.OnHand) + " " + string(p.Unit)
			},
			Importance: quantityImportance,
			Bold:       func(core.ProductWithStock) bool { return true },
		},
		{
			Title: "Status", Width: 120, SortKey: string(storage.SortProductStatus),
			Value:      func(p core.ProductWithStock) string { return p.Status().Label() },
			Importance: func(p core.ProductWithStock) widget.Importance { return statusImportance(p.Status()) },
		},
		{
			Title: "Cost", Width: 110, Align: fyne.TextAlignTrailing,
			SortKey: string(storage.SortProductCost), Monospace: true,
			Value: func(p core.ProductWithStock) string { return p.Cost.Display() },
		},
		{
			Title: "Price", Width: 110, Align: fyne.TextAlignTrailing,
			SortKey: string(storage.SortProductPrice), Monospace: true,
			Value: func(p core.ProductWithStock) string { return p.Price.Display() },
		},
		{
			Title: "Stock value", Width: 120, Align: fyne.TextAlignTrailing,
			SortKey: string(storage.SortProductValue), Monospace: true,
			Value: func(p core.ProductWithStock) string {
				if p.NonStock {
					return "—"
				}
				return p.StockValue().Display()
			},
		},
		{
			Title: "Supplier", Width: 170, SortKey: string(storage.SortProductSupplier),
			Value: func(p core.ProductWithStock) string { return dash(p.Supplier) },
		},
		{
			Title: "Reorder at", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(p core.ProductWithStock) string {
				if p.ReorderPoint == 0 {
					return "—"
				}
				return formatQuantity(p.ReorderPoint)
			},
		},
		{
			Title: "Last moved", Width: 120,
			Value: func(p core.ProductWithStock) string {
				return formatRelative(p.LastMovementAt, time.Now())
			},
			Importance: func(p core.ProductWithStock) widget.Importance { return widget.LowImportance },
		},
	}
}
