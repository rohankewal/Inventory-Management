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

// orderPageSize bounds one screenful of orders.
const orderPageSize = 300

// orderScopeOption pairs a menu label with the filter it applies.
type orderScopeOption struct {
	label string
	apply func(*storage.OrderFilter)
}

// orderScopes are the questions somebody actually opens this screen to ask.
var orderScopes = []orderScopeOption{
	{"Open orders", func(f *storage.OrderFilter) { f.OpenOnly = true }},
	{"Late — past cancel date", func(f *storage.OrderFilter) { f.LateOnly = true }},
	{"Shipping in 14 days", func(f *storage.OrderFilter) {
		f.OpenOnly = true
		f.ShipBefore = time.Now().UTC().AddDate(0, 0, 14)
	}},
	{"Drafts", func(f *storage.OrderFilter) { f.Status = core.OrderDraft }},
	{"Shipped", func(f *storage.OrderFilter) { f.Status = core.OrderShipped }},
	{"Everything", func(*storage.OrderFilter) {}},
}

// ordersView is the screen the business runs on: every store PO, what is on
// it, when it has to leave, and whether it is late.
type ordersView struct {
	app *App

	content fyne.CanvasObject
	table   *DataTable[core.OrderSummary]
	detail  *orderDetailPanel
	split   *container.Split

	searchBox   *widget.Entry
	customerSel *widget.Select
	scopeSel    *widget.Select
	summary     *widget.Label

	customers []core.CustomerWithStores
	filter    storage.OrderFilter

	debounce   *time.Timer
	generation int
}

func newOrdersView(a *App) *ordersView {
	v := &ordersView{
		app:    a,
		filter: storage.OrderFilter{OpenOnly: true, Limit: orderPageSize},
	}
	v.build()
	return v
}

func (v *ordersView) title() string { return "Orders" }

func (v *ordersView) object() fyne.CanvasObject { return v.content }

func (v *ordersView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("Import orders", theme.UploadIcon(), widget.MediumImportance,
			v.app.openOrderImportDialog),
		toolbarButton("New order", theme.ContentAddIcon(), widget.HighImportance,
			func() { v.app.openOrderForm(nil) }),
	}
}

func (v *ordersView) build() {
	v.table = NewDataTable(orderColumns())
	v.table.SetSort(string(storage.SortOrderShipDate), false)
	v.table.OnSortChanged = func(key string, descending bool) {
		v.filter.Sort = storage.OrderSort(key)
		v.filter.Direction = sortDirection(descending)
		v.reload()
	}
	v.table.OnSelect = func(row core.OrderSummary, _ int) {
		v.detail.show(row.ID)
		v.showDetail(true)
	}
	v.table.OnActivate = func(row core.OrderSummary, _ int) { v.app.openOrderByID(row.ID) }

	v.detail = newOrderDetailPanel(v.app)

	v.split = container.NewHSplit(v.table.Object(), v.detail.object())
	v.showDetail(false)

	v.content = container.NewBorder(v.buildFilterBar(), v.buildFooter(), nil, nil, v.split)
}

// showDetail opens or collapses the inspector.
func (v *ordersView) showDetail(open bool) {
	if v.split == nil {
		return
	}
	if open {
		v.detail.object().Show()
		v.split.SetOffset(0.62)
		return
	}
	v.detail.clear()
	v.detail.object().Hide()
	v.split.SetOffset(1)
}

func (v *ordersView) buildFilterBar() fyne.CanvasObject {
	// Widgets first, callbacks after: a Fyne setter fires its handler
	// synchronously, so wiring as we go would run one against a half-built view.
	v.searchBox = widget.NewEntry()
	v.searchBox.SetPlaceHolder("Filter by PO number, store or customer")

	v.customerSel = widget.NewSelect(nil, nil)
	v.customerSel.PlaceHolder = "All customers"

	labels := make([]string, len(orderScopes))
	for i, scope := range orderScopes {
		labels[i] = scope.label
	}
	v.scopeSel = widget.NewSelect(labels, nil)
	v.scopeSel.SetSelectedIndex(0)

	clear := widget.NewButtonWithIcon("Clear", theme.ContentClearIcon(), v.clearFilters)
	clear.Importance = widget.LowImportance

	v.searchBox.OnChanged = func(string) { v.reloadDebounced() }
	v.customerSel.OnChanged = func(string) { v.applyFacets() }
	v.scopeSel.OnChanged = func(string) { v.applyFacets() }

	filters := container.NewHBox(
		container.NewGridWrap(fyne.NewSize(210, 36), v.customerSel),
		container.NewGridWrap(fyne.NewSize(210, 36), v.scopeSel),
		clear,
	)

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, filters, v.searchBox),
		widget.NewSeparator(),
	)
}

func (v *ordersView) buildFooter() fyne.CanvasObject {
	v.summary = widget.NewLabel("")
	v.summary.Importance = widget.LowImportance

	return container.NewVBox(widget.NewSeparator(), container.NewBorder(nil, nil, v.summary, nil))
}

func (v *ordersView) applyFacets() {
	sort, direction := v.filter.Sort, v.filter.Direction
	v.filter = storage.OrderFilter{
		Sort: sort, Direction: direction, Limit: orderPageSize,
	}

	if i := v.scopeSel.SelectedIndex(); i >= 0 && i < len(orderScopes) {
		orderScopes[i].apply(&v.filter)
	}
	v.filter.CustomerID = v.selectedCustomer()

	v.reload()
}

func (v *ordersView) selectedCustomer() core.ID {
	i := v.customerSel.SelectedIndex()
	if i < 0 || i >= len(v.customers) {
		return core.NilID
	}
	return v.customers[i].ID
}

func (v *ordersView) clearFilters() {
	v.searchBox.SetText("")
	v.customerSel.ClearSelected()
	v.scopeSel.SetSelectedIndex(0)
	v.applyFacets()
}

// setSearch drives the list from elsewhere, such as a scan that resolved a PO.
func (v *ordersView) setSearch(term string) {
	// Searching for a specific PO must not be hidden by the open-only default:
	// somebody looking up a number usually wants it whatever its status.
	v.scopeSel.SetSelectedIndex(len(orderScopes) - 1)
	v.searchBox.SetText(term)
	v.reload()
}

func (v *ordersView) reloadDebounced() {
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

func (v *ordersView) reload() {
	v.filter.Search = v.searchBox.Text
	v.filter.Limit = orderPageSize

	v.generation++
	generation := v.generation
	filter := v.filter

	v.app.background(func(ctx context.Context) {
		page, pageErr := v.app.svc.ListOrders(ctx, filter)
		customers, _ := v.app.svc.ListCustomers(ctx, storage.CustomerFilter{})

		v.app.onMain(func() {
			if generation != v.generation {
				return
			}
			if pageErr != nil {
				v.app.showError(wrapf(pageErr, "the orders could not be loaded"))
				return
			}

			v.customers = customers.Items
			names := make([]string, len(customers.Items))
			for i, c := range customers.Items {
				names[i] = c.Name
			}
			setSelectOptions(v.customerSel, names)

			v.table.SetSort(string(filter.Sort), filter.Direction == storage.Descending)
			v.table.SetRows(page.Items, v.emptyMessage())
			v.summary.SetText(v.summarise(page))
			v.showDetail(false)
		})
	})
}

func (v *ordersView) summarise(page service.OrderPage) string {
	if len(page.Items) == 0 {
		return "No orders"
	}

	now := time.Now()
	var units, late int64
	value := core.Zero(v.app.currency)
	for _, order := range page.Items {
		units += order.Totals.Outstanding
		if order.Late(now) {
			late++
		}
		if order.Totals.Value.Currency == value.Currency {
			value.Minor += order.Totals.Value.Minor
		}
	}

	summary := sprintf("%s · %s outstanding · %s",
		pluralize(len(page.Items), "order", "orders"),
		formatQuantity(units), value.Display())
	if late > 0 {
		summary += sprintf("  ·  %s past the cancel date", formatQuantity(late))
	}
	if page.Total > len(page.Items) {
		summary += sprintf("  ·  showing %d of %d", len(page.Items), page.Total)
	}
	return summary
}

func (v *ordersView) emptyMessage() string {
	if v.filter.LateOnly {
		return "Nothing is past its cancel date.\n\nThat is the report you want to be empty."
	}
	if v.filter.Search != "" {
		return "No orders match that.\n\nTry \"Everything\" in the scope filter — the default hides shipped orders."
	}
	return "No open orders.\n\nImport a client's order sheet, or create one with the button above."
}

func orderColumns() []Column[core.OrderSummary] {
	now := time.Now()

	// Ordered by what somebody needs when the inspector is open and the grid
	// is narrow: which PO, which door, is it late, how much is left to send.
	// Customer and value matter, but they are not what the screen is scanned
	// for.
	return []Column[core.OrderSummary]{
		{
			Title: "PO number", Width: 140, Monospace: true,
			SortKey: string(storage.SortOrderPONumber),
			Value:   func(o core.OrderSummary) string { return o.CustomerPONumber },
			Bold:    func(core.OrderSummary) bool { return true },
		},
		{
			Title: "Store", Width: 190, SortKey: string(storage.SortOrderStore),
			Value: func(o core.OrderSummary) string { return o.StoreCode + " — " + o.StoreName },
		},
		{
			Title: "Status", Width: 130, SortKey: string(storage.SortOrderStatus),
			Value: func(o core.OrderSummary) string { return o.Status.Label() },
			Importance: func(o core.OrderSummary) widget.Importance {
				return orderStatusImportance(o, now)
			},
		},
		{
			Title: "Outstanding", Width: 105, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(o core.OrderSummary) string { return formatQuantity(o.Totals.Outstanding) },
			Importance: func(o core.OrderSummary) widget.Importance {
				if o.Totals.Outstanding == 0 {
					return widget.LowImportance
				}
				return widget.MediumImportance
			},
			Bold: func(core.OrderSummary) bool { return true },
		},
		{
			Title: "Ship by", Width: 120, SortKey: string(storage.SortOrderShipDate),
			Value: func(o core.OrderSummary) string { return formatDate(o.RequestedShipDate) },
			Importance: func(o core.OrderSummary) widget.Importance {
				if !o.Status.Open() || o.RequestedShipDate.IsZero() {
					return widget.LowImportance
				}
				switch days := o.DaysToShip(now); {
				case days < 0:
					return widget.DangerImportance
				case days <= 7:
					return widget.WarningImportance
				}
				return widget.MediumImportance
			},
		},
		{
			Title: "Cancel after", Width: 120, SortKey: string(storage.SortOrderCancelDate),
			Value: func(o core.OrderSummary) string { return formatDate(o.CancelAfterDate) },
			Importance: func(o core.OrderSummary) widget.Importance {
				if o.Late(now) {
					return widget.DangerImportance
				}
				return widget.LowImportance
			},
			Bold: func(o core.OrderSummary) bool { return o.Late(now) },
		},
		{
			Title: "Customer", Width: 140, SortKey: string(storage.SortOrderCustomer),
			Value: func(o core.OrderSummary) string { return o.CustomerName },
		},
		{
			Title: "Value", Width: 125, Align: fyne.TextAlignTrailing, Monospace: true,
			SortKey: string(storage.SortOrderValue),
			Value:   func(o core.OrderSummary) string { return o.Totals.Value.Display() },
		},
		{
			Title: "Units", Width: 95, Align: fyne.TextAlignTrailing, Monospace: true,
			SortKey: string(storage.SortOrderUnits),
			Value:   func(o core.OrderSummary) string { return formatQuantity(o.Totals.Units) },
		},
		{
			Title: "Program", Width: 140,
			Value: func(o core.OrderSummary) string { return dash(o.ProgramCode) },
		},
	}
}

// orderStatusImportance colours an order by how much attention it needs. A late
// order is red whatever its status says, because the cancel date is the fact
// that costs money.
func orderStatusImportance(o core.OrderSummary, now time.Time) widget.Importance {
	if o.Late(now) {
		return widget.DangerImportance
	}
	switch o.Status {
	case core.OrderShipped:
		return widget.SuccessImportance
	case core.OrderCancelled, core.OrderClosed:
		return widget.LowImportance
	case core.OrderDraft:
		return widget.WarningImportance
	}
	return widget.MediumImportance
}
