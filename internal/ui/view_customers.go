package ui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// customersView lists client businesses and, for the selected one, its stores.
//
// The stores are the point: a client with two hundred doors is unmanageable as
// a flat list, and every order screen depends on the store list being right.
type customersView struct {
	app *App

	content fyne.CanvasObject
	table   *DataTable[core.CustomerWithStores]
	stores  *storePanel
	split   *container.Split

	searchBox   *widget.Entry
	archivedChk *widget.Check
	summary     *widget.Label

	debounce   *time.Timer
	generation int
	filter     storage.CustomerFilter
}

func newCustomersView(a *App) *customersView {
	v := &customersView{app: a}
	v.build()
	return v
}

func (v *customersView) title() string { return "Customers" }

func (v *customersView) object() fyne.CanvasObject { return v.content }

func (v *customersView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("New customer", theme.ContentAddIcon(), widget.HighImportance,
			func() { v.app.openCustomerForm(nil) }),
	}
}

func (v *customersView) build() {
	v.table = NewDataTable(customerColumns())
	v.table.OnSelect = func(row core.CustomerWithStores, _ int) {
		v.stores.show(row.Customer)
		v.showDetail(true)
	}
	v.table.OnActivate = func(row core.CustomerWithStores, _ int) {
		v.app.openCustomerForm(&row.Customer)
	}

	v.stores = newStorePanel(v.app)

	v.split = container.NewHSplit(v.table.Object(), v.stores.object())
	v.showDetail(false)

	v.content = container.NewBorder(v.buildFilterBar(), v.buildFooter(), nil, nil, v.split)
}

func (v *customersView) showDetail(open bool) {
	if v.split == nil {
		return
	}
	if open {
		v.stores.object().Show()
		v.split.SetOffset(0.44)
		return
	}
	v.stores.clear()
	v.stores.object().Hide()
	v.split.SetOffset(1)
}

func (v *customersView) buildFilterBar() fyne.CanvasObject {
	v.searchBox = widget.NewEntry()
	v.searchBox.SetPlaceHolder("Filter by code or name")
	v.archivedChk = widget.NewCheck("Show archived", nil)

	v.searchBox.OnChanged = func(string) { v.reloadDebounced() }
	v.archivedChk.OnChanged = func(bool) { v.reload() }

	return container.NewVBox(
		container.NewBorder(nil, nil, nil, v.archivedChk, v.searchBox),
		widget.NewSeparator(),
	)
}

func (v *customersView) buildFooter() fyne.CanvasObject {
	v.summary = widget.NewLabel("")
	v.summary.Importance = widget.LowImportance
	return container.NewVBox(widget.NewSeparator(), container.NewBorder(nil, nil, v.summary, nil))
}

func (v *customersView) reloadDebounced() {
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

func (v *customersView) reload() {
	v.filter = storage.CustomerFilter{
		Search:          v.searchBox.Text,
		IncludeInactive: v.archivedChk.Checked,
	}

	v.generation++
	generation := v.generation
	filter := v.filter

	v.app.background(func(ctx context.Context) {
		page, err := v.app.svc.ListCustomers(ctx, filter)
		v.app.onMain(func() {
			if generation != v.generation {
				return
			}
			if err != nil {
				v.app.showError(wrapf(err, "the customers could not be loaded"))
				return
			}

			v.table.SetRows(page.Items,
				"No customers yet.\n\nAdd one, then import their store list from a spreadsheet.")

			var stores, open int
			for _, c := range page.Items {
				stores += c.ActiveStores
				open += c.OpenOrders
			}
			v.summary.SetText(sprintf("%s · %s · %s open",
				pluralize(page.Total, "customer", "customers"),
				pluralize(stores, "store", "stores"),
				pluralize(open, "order", "orders")))
			v.showDetail(false)
		})
	})
}

func customerColumns() []Column[core.CustomerWithStores] {
	return []Column[core.CustomerWithStores]{
		{
			Title: "Code", Width: 110, Monospace: true,
			Value: func(c core.CustomerWithStores) string { return c.Code },
			Bold:  func(core.CustomerWithStores) bool { return true },
		},
		{
			Title: "Customer", Width: 240,
			Value: func(c core.CustomerWithStores) string { return c.Name },
		},
		{
			Title: "Stores", Width: 90, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(c core.CustomerWithStores) string { return formatQuantity(int64(c.ActiveStores)) },
			Importance: func(c core.CustomerWithStores) widget.Importance {
				// A client with no stores cannot be ordered for, which is
				// worth flagging before somebody tries.
				if c.ActiveStores == 0 {
					return widget.WarningImportance
				}
				return widget.MediumImportance
			},
		},
		{
			Title: "Open orders", Width: 110, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(c core.CustomerWithStores) string { return formatQuantity(int64(c.OpenOrders)) },
			Bold:  func(c core.CustomerWithStores) bool { return c.OpenOrders > 0 },
		},
		{
			Title: "Currency", Width: 90,
			Value: func(c core.CustomerWithStores) string { return string(c.Currency) },
		},
		{
			Title: "Terms", Width: 130,
			Value: func(c core.CustomerWithStores) string { return dash(c.Terms) },
		},
		{
			Title: "Contact", Width: 220,
			Value: func(c core.CustomerWithStores) string { return dash(describeContact(c.Contact)) },
		},
		{
			Title: "Status", Width: 100,
			Value: func(c core.CustomerWithStores) string {
				if c.Active {
					return "Active"
				}
				return "Archived"
			},
			Importance: func(c core.CustomerWithStores) widget.Importance {
				if c.Active {
					return widget.MediumImportance
				}
				return widget.LowImportance
			},
		},
	}
}

// storePanel lists one customer's ship-to destinations.
type storePanel struct {
	app *App

	root    *fyne.Container
	empty   *widget.Label
	body    *fyne.Container
	current core.Customer

	nameLabel *widget.Label
	actionRow *fyne.Container
	listBox   *fyne.Container
}

func newStorePanel(a *App) *storePanel {
	p := &storePanel{app: a}
	p.build()
	return p
}

func (p *storePanel) object() fyne.CanvasObject { return p.root }

func (p *storePanel) build() {
	p.empty = widget.NewLabel("Select a customer to see and manage its stores.")
	p.empty.Alignment = fyne.TextAlignCenter
	p.empty.Importance = widget.LowImportance
	p.empty.Wrapping = fyne.TextWrapWord

	p.nameLabel = widget.NewLabel("")
	p.nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	p.nameLabel.SizeName = theme.SizeNameSubHeadingText

	p.actionRow = container.NewVBox()
	p.listBox = container.NewVBox()

	p.body = container.NewVBox(
		p.nameLabel,
		p.actionRow,
		widget.NewSeparator(),
		p.listBox,
	)
	p.body.Hide()

	p.root = container.NewPadded(container.NewVScroll(container.NewStack(p.empty, p.body)))
}

func (p *storePanel) clear() {
	p.current = core.Customer{}
	p.body.Hide()
	p.empty.Show()
}

func (p *storePanel) show(customer core.Customer) {
	p.current = customer
	p.empty.Hide()
	p.body.Show()

	p.nameLabel.SetText(customer.Name + " — stores")

	p.actionRow.RemoveAll()
	add := widget.NewButtonWithIcon("Add store", theme.ContentAddIcon(), func() {
		p.app.openStoreForm(customer, nil)
	})
	add.Importance = widget.HighImportance

	importStores := widget.NewButtonWithIcon("Import list", theme.UploadIcon(), func() {
		p.app.openStoreImportDialog(customer)
	})
	editCustomer := widget.NewButtonWithIcon("Edit customer", theme.DocumentCreateIcon(), func() {
		p.app.openCustomerForm(&customer)
	})

	p.actionRow.Add(container.NewGridWithColumns(3, add, importStores, editCustomer))
	p.actionRow.Refresh()

	p.load(customer)
}

func (p *storePanel) load(customer core.Customer) {
	p.listBox.RemoveAll()
	p.listBox.Add(widget.NewLabel("Loading…"))
	p.listBox.Refresh()

	p.app.background(func(ctx context.Context) {
		stores, err := p.app.svc.ListStores(ctx, storage.StoreFilter{
			CustomerID: customer.ID, IncludeInactive: true,
		})
		p.app.onMain(func() {
			if p.current.ID != customer.ID {
				return
			}

			p.listBox.RemoveAll()
			if err != nil {
				p.listBox.Add(widget.NewLabel("Could not load the stores."))
				p.listBox.Refresh()
				return
			}
			if len(stores) == 0 {
				note := widget.NewLabel(
					"No stores yet.\n\nImport the client's store list from a spreadsheet, " +
						"or add them one at a time.")
				note.Wrapping = fyne.TextWrapWord
				note.Importance = widget.WarningImportance
				p.listBox.Add(note)
				p.listBox.Refresh()
				return
			}

			for _, store := range stores {
				p.listBox.Add(p.storeRow(customer, store))
			}
			p.listBox.Refresh()
		})
	})
}

func (p *storePanel) storeRow(customer core.Customer, store core.CustomerStore) fyne.CanvasObject {
	title := widget.NewLabel(store.Label())
	title.TextStyle = fyne.TextStyle{Bold: store.Active}
	if !store.Active {
		title.Importance = widget.LowImportance
	}

	address := widget.NewLabel(dash(store.ShipTo.SingleLine()))
	address.Importance = widget.LowImportance
	address.SizeName = theme.SizeNameCaptionText
	address.Truncation = fyne.TextTruncateEllipsis

	edit := widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		p.app.openStoreForm(customer, &store)
	})
	edit.Importance = widget.LowImportance

	orders := widget.NewButtonWithIcon("", theme.ListIcon(), func() {
		p.app.showOrdersForStore(store)
	})
	orders.Importance = widget.LowImportance

	// A store with no address cannot produce usable delivery paperwork, so it
	// is flagged where somebody can fix it.
	body := container.NewVBox(title, address)
	if store.ShipTo.IsEmpty() && store.Active {
		warning := widget.NewLabel("No address on file")
		warning.Importance = widget.WarningImportance
		warning.SizeName = theme.SizeNameCaptionText
		body.Add(warning)
	}

	return container.NewBorder(nil, widget.NewSeparator(), nil,
		container.NewHBox(orders, edit), body)
}
