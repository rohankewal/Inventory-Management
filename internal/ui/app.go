// Package ui renders the desktop client.
//
// Two rules hold everywhere in this package:
//
//  1. No package here talks to storage directly. Every mutation goes through
//     the service layer, which owns transactions and, from Phase 2, permissions
//     and the audit trail.
//  2. No database call runs on the UI goroutine. Fyne redraws on that
//     goroutine, so a query that takes a second freezes the window for a
//     second. Work happens in a goroutine and results are handed back with
//     fyne.Do.
package ui

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/config"
	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
)

// operationTimeout bounds any single database call made from the UI, so a
// wedged connection surfaces as an error message rather than a frozen window.
const operationTimeout = 30 * time.Second

// viewID names a screen.
type viewID string

const (
	viewDashboard viewID = "dashboard"
	viewOrders    viewID = "orders"
	viewCustomers viewID = "customers"
	viewProducts  viewID = "products"
	viewMovements viewID = "movements"
	viewReports   viewID = "reports"
	viewSettings  viewID = "settings"
)

// view is one screen in the shell.
type view interface {
	// object returns the screen's content, built once and reused.
	object() fyne.CanvasObject
	// title is shown in the header.
	title() string
	// actions are the buttons placed on the right of the header.
	actions() []fyne.CanvasObject
	// reload refreshes the screen's data. It is called when the screen is
	// shown and whenever something elsewhere changes the data underneath it.
	reload()
}

// App is the desktop application.
type App struct {
	svc *service.Inventory
	log *slog.Logger
	cfg config.Config

	fyneApp fyne.App
	window  fyne.Window
	theme   *appTheme

	// ctx is cancelled when the window closes, which unblocks any query still
	// running in the background.
	ctx    context.Context
	cancel context.CancelFunc

	// location scopes every screen. Multi-location arrives in Phase 3; until
	// then this is the seeded default and the picker has one entry.
	location core.ID
	currency core.Currency

	views  map[viewID]view
	navs   map[viewID]*widget.Button
	active viewID

	titleLabel *widget.Label
	actionBar  *fyne.Container
	content    *fyne.Container
	scanEntry  *widget.Entry
	status     *statusBar

	// pending counts in-flight background work so the header can show that
	// something is happening rather than appearing to have ignored a click.
	pending int
	spinner *widget.ProgressBarInfinite

	// run and onMain are the two threading seams. In production run spawns a
	// goroutine and onMain marshals the result back through fyne.Do. Tests
	// replace both with inline calls, because Fyne's headless driver executes
	// a marshalled closure on whichever goroutine submitted it — which means
	// a background query and the test's own assertions end up inside Fyne's
	// widget code at the same time.
	run    func(fn func(ctx context.Context))
	onMain func(fn func())
}

// New builds the application window.
func New(svc *service.Inventory, cfg config.Config, log *slog.Logger) *App {
	return newApp(app.NewWithID("com.inventorysys.desktop"), svc, cfg, log)
}

// newApp builds the application over a given Fyne app, so tests can construct
// the whole shell against a headless driver.
func newApp(fyneApp fyne.App, svc *service.Inventory, cfg config.Config, log *slog.Logger) *App {
	ctx, cancel := context.WithCancel(context.Background())

	a := &App{
		svc:      svc,
		cfg:      cfg,
		log:      log,
		ctx:      ctx,
		cancel:   cancel,
		location: core.DefaultLocationID,
		currency: core.Currency(cfg.Currency),
		views:    map[viewID]view{},
		navs:     map[viewID]*widget.Button{},
	}
	if !a.currency.Valid() {
		a.currency = core.DefaultCurrency
	}

	a.run = a.runInBackground
	a.onMain = fyne.Do

	a.fyneApp = fyneApp
	a.applyAppearance(a.savedAppearance())

	a.window = a.fyneApp.NewWindow(a.windowTitle())
	a.window.Resize(fyne.NewSize(1280, 800))
	a.window.CenterOnScreen()
	a.window.SetOnClosed(cancel)

	a.buildViews()
	a.window.SetContent(a.buildShell())
	a.window.SetMainMenu(a.buildMenu())
	a.installShortcuts()

	return a
}

// Run shows the window and blocks until it closes.
func (a *App) Run() {
	a.show(viewDashboard)
	a.window.ShowAndRun()
}

func (a *App) windowTitle() string { return config.AppName + " — Inventory" }

// --- shell ------------------------------------------------------------------

func (a *App) buildViews() {
	a.views[viewDashboard] = newDashboardView(a)
	a.views[viewOrders] = newOrdersView(a)
	a.views[viewCustomers] = newCustomersView(a)
	a.views[viewProducts] = newProductsView(a)
	a.views[viewMovements] = newMovementsView(a)
	a.views[viewReports] = newReportsView(a)
	a.views[viewSettings] = newSettingsView(a)
}

func (a *App) buildShell() fyne.CanvasObject {
	a.content = container.NewStack()
	a.status = newStatusBar(a.onMain)

	main := container.NewBorder(a.buildHeader(), a.status.object(), nil, nil,
		container.NewPadded(a.content))

	split := container.NewHSplit(a.buildSidebar(), main)
	// The sidebar is a fixed navigation rail, so it gets a small fraction and
	// the content takes the rest as the window grows.
	split.SetOffset(0.16)
	return split
}

func (a *App) buildSidebar() fyne.CanvasObject {
	brand := widget.NewLabel(config.AppName)
	brand.TextStyle = fyne.TextStyle{Bold: true}
	brand.SizeName = theme.SizeNameSubHeadingText

	items := []struct {
		id    viewID
		label string
		icon  fyne.Resource
	}{
		{viewDashboard, "Dashboard", theme.HomeIcon()},
		{viewOrders, "Orders", theme.ListIcon()},
		{viewCustomers, "Customers", theme.AccountIcon()},
		{viewProducts, "Products", theme.StorageIcon()},
		{viewMovements, "Stock activity", theme.HistoryIcon()},
		{viewReports, "Reports", theme.DocumentIcon()},
		{viewSettings, "Settings", theme.SettingsIcon()},
	}

	nav := container.NewVBox()
	for _, item := range items {
		id := item.id
		button := widget.NewButtonWithIcon(item.label, item.icon, func() { a.show(id) })
		button.Alignment = widget.ButtonAlignLeading
		button.Importance = widget.LowImportance
		a.navs[id] = button
		nav.Add(button)
	}

	footer := widget.NewLabel(a.storageSummary())
	footer.Importance = widget.LowImportance
	footer.SizeName = theme.SizeNameCaptionText
	footer.Wrapping = fyne.TextWrapWord

	return container.NewBorder(
		container.NewPadded(brand),
		container.NewPadded(footer),
		nil, nil,
		container.NewVScroll(container.NewPadded(nav)),
	)
}

func (a *App) storageSummary() string {
	if a.cfg.Driver == config.DriverSQLite {
		return fmt.Sprintf("Local database\n%s", a.currency)
	}
	return fmt.Sprintf("%s database\n%s", a.cfg.Driver, a.currency)
}

func (a *App) buildHeader() fyne.CanvasObject {
	a.titleLabel = widget.NewLabel("")
	a.titleLabel.TextStyle = fyne.TextStyle{Bold: true}
	a.titleLabel.SizeName = theme.SizeNameHeadingText

	// One scan box, always available. A handheld scanner is just a keyboard
	// that types a code and presses Enter, so the fastest possible path from
	// "item in hand" to "its record on screen" is a field that is never more
	// than one keystroke away, whatever screen you are on.
	a.scanEntry = widget.NewEntry()
	a.scanEntry.SetPlaceHolder("Scan a barcode, or type a PO number   (⌘F)")
	a.scanEntry.ActionItem = widget.NewIcon(theme.SearchIcon())
	a.scanEntry.OnSubmitted = a.onScan

	a.spinner = widget.NewProgressBarInfinite()
	a.spinner.Hide()

	a.actionBar = container.NewHBox()

	right := container.NewHBox(a.actionBar)
	search := container.NewGridWrap(fyne.NewSize(330, 38), a.scanEntry)

	bar := container.NewBorder(nil, nil,
		container.NewPadded(a.titleLabel),
		container.NewPadded(container.NewHBox(search, right)),
		nil,
	)
	return container.NewVBox(bar, a.spinner, widget.NewSeparator())
}

// show switches to a screen, refreshing its data.
func (a *App) show(id viewID) {
	target, ok := a.views[id]
	if !ok {
		return
	}
	a.active = id

	for navID, button := range a.navs {
		if navID == id {
			button.Importance = widget.HighImportance
		} else {
			button.Importance = widget.LowImportance
		}
		button.Refresh()
	}

	a.titleLabel.SetText(target.title())

	a.actionBar.RemoveAll()
	for _, action := range target.actions() {
		a.actionBar.Add(action)
	}
	a.actionBar.Refresh()

	a.content.RemoveAll()
	a.content.Add(target.object())
	a.content.Refresh()

	target.reload()
}

// reloadAll refreshes every screen's data, used after an import or any change
// broad enough that a single screen's view of the world is no longer right.
func (a *App) reloadAll() {
	if current, ok := a.views[a.active]; ok {
		current.reload()
	}
}

// --- menus and shortcuts ----------------------------------------------------

func (a *App) buildMenu() *fyne.MainMenu {
	file := fyne.NewMenu("File",
		fyne.NewMenuItem("New order…", func() { a.openOrderForm(nil) }),
		fyne.NewMenuItem("New product…", func() { a.openProductForm(nil) }),
		fyne.NewMenuItem("New customer…", func() { a.openCustomerForm(nil) }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Import orders from CSV…", a.openOrderImportDialog),
		fyne.NewMenuItem("Import products from CSV…", a.openImportDialog),
		fyne.NewMenuItem("Export products to CSV…", a.openExportDialog),
		fyne.NewMenuItem("Download CSV templates…", a.saveCSVTemplates),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Back up the database…", a.openBackupDialog),
	)

	appearance := fyne.NewMenuItem("Appearance", nil)
	appearance.ChildMenu = fyne.NewMenu("",
		fyne.NewMenuItem(AppearanceSystem.Label(), func() { a.setAppearance(AppearanceSystem) }),
		fyne.NewMenuItem(AppearanceLight.Label(), func() { a.setAppearance(AppearanceLight) }),
		fyne.NewMenuItem(AppearanceDark.Label(), func() { a.setAppearance(AppearanceDark) }),
	)

	view := fyne.NewMenu("View",
		fyne.NewMenuItem("Refresh", a.reloadAll),
		fyne.NewMenuItemSeparator(),
		appearance,
	)

	help := fyne.NewMenu("Help",
		fyne.NewMenuItem("Keyboard shortcuts", a.showShortcuts),
		fyne.NewMenuItem("About "+config.AppName, a.showAbout),
	)

	return fyne.NewMainMenu(file, view, help)
}

func (a *App) installShortcuts() {
	shortcut := func(key fyne.KeyName, handler func()) {
		a.window.Canvas().AddShortcut(
			&desktop.CustomShortcut{KeyName: key, Modifier: fyne.KeyModifierShortcutDefault},
			func(fyne.Shortcut) { handler() },
		)
	}

	shortcut(fyne.KeyF, func() { a.window.Canvas().Focus(a.scanEntry) })
	shortcut(fyne.KeyN, func() { a.openOrderForm(nil) })
	shortcut(fyne.KeyR, a.reloadAll)
	shortcut(fyne.Key1, func() { a.show(viewDashboard) })
	shortcut(fyne.Key2, func() { a.show(viewOrders) })
	shortcut(fyne.Key3, func() { a.show(viewCustomers) })
	shortcut(fyne.Key4, func() { a.show(viewProducts) })
	shortcut(fyne.Key5, func() { a.show(viewMovements) })
	shortcut(fyne.Key6, func() { a.show(viewReports) })
	shortcut(fyne.Key7, func() { a.show(viewSettings) })
}

func (a *App) showShortcuts() {
	shortcuts := [][2]string{
		{"⌘F", "Focus the scan and search box"},
		{"⌘N", "New order"},
		{"⌘R", "Refresh the current screen"},
		{"⌘1 – ⌘7", "Switch between screens"},
		{"Enter in the scan box", "Open the PO, product or barcode you typed"},
	}

	rows := container.New(newFormGrid())
	for _, s := range shortcuts {
		key := widget.NewLabel(s[0])
		key.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
		rows.Add(key)
		rows.Add(widget.NewLabel(s[1]))
	}
	a.showInfoDialog("Keyboard shortcuts", rows)
}

func (a *App) showAbout() {
	body := container.NewVBox(
		widget.NewLabelWithStyle(config.AppName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewLabel("Inventory management for small businesses and teams."),
		widget.NewSeparator(),
		labelledValue("Storage", a.cfg.Driver),
		labelledValue("Database", a.cfg.DatabasePath()),
		labelledValue("Data folder", a.cfg.Dir()),
		labelledValue("Currency", string(a.currency)),
	)
	a.showInfoDialog("About "+config.AppName, body)
}

// --- appearance -------------------------------------------------------------

func (a *App) savedAppearance() Appearance {
	saved := Appearance(a.fyneApp.Preferences().StringWithFallback("appearance", string(AppearanceSystem)))
	switch saved {
	case AppearanceLight, AppearanceDark, AppearanceSystem:
		return saved
	}
	return AppearanceSystem
}

func (a *App) applyAppearance(appearance Appearance) {
	a.theme = newTheme(appearance)
	a.fyneApp.Settings().SetTheme(a.theme)
}

func (a *App) setAppearance(appearance Appearance) {
	a.fyneApp.Preferences().SetString("appearance", string(appearance))
	a.applyAppearance(appearance)
	a.status.info("Appearance set to %s.", appearance.Label())
}

// --- scanning ---------------------------------------------------------------

// onScan resolves whatever was typed or scanned.
//
// It tries a PO number, then a barcode, then a SKU, because those are the three
// identifiers anybody actually holds — a client says a PO number on the phone,
// a warehouse scans a barcode, and a buyer knows a SKU. Anything else falls
// through to a catalogue search, since a half-remembered product name is a
// search rather than a failed scan.
func (a *App) onScan(code string) {
	if code == "" {
		return
	}

	a.background(func(ctx context.Context) {
		found, err := a.svc.Lookup(ctx, code, a.location)
		a.onMain(func() {
			if err != nil {
				a.searchCatalogue(code)
				return
			}
			a.scanEntry.SetText("")

			switch {
			case len(found.Orders) == 1:
				order := found.Orders[0]
				a.status.success("Found %s — %s, %s.",
					order.CustomerPONumber, order.CustomerName, order.StoreName)
				a.showOrder(order)

			case len(found.Orders) > 1:
				// The number is only unique within a customer, so two clients
				// can legitimately share one.
				a.status.info("%s matches %d orders across customers.",
					code, len(found.Orders))
				a.showOrder(found.Orders[0])

			case found.Product != nil:
				a.status.success("Found %s — %s.", found.Product.SKU, found.Product.Name)
				a.openProductDetail(*found.Product)
			}
		})
	})
}

// searchCatalogue switches to the product list filtered by a term.
func (a *App) searchCatalogue(term string) {
	products, ok := a.views[viewProducts].(*productsView)
	if !ok {
		return
	}
	a.show(viewProducts)
	products.setSearch(term)
}

// --- background work --------------------------------------------------------

// background runs work off the UI goroutine with a bounded timeout. Callers
// hand results back with a.onMain.
func (a *App) background(fn func(ctx context.Context)) {
	// A debounced search or a delayed refresh can fire after the window has
	// closed. Starting a query against a database that is being shut down
	// gains nothing and races the teardown.
	if a.ctx.Err() != nil {
		return
	}
	a.beginWork()
	a.run(fn)
}

func (a *App) runInBackground(fn func(ctx context.Context)) {
	go func() {
		defer a.recoverPanic()
		defer func() { a.onMain(a.endWork) }()

		ctx, cancel := context.WithTimeout(a.ctx, operationTimeout)
		defer cancel()
		fn(ctx)
	}()
}

func (a *App) beginWork() {
	a.pending++
	if a.spinner != nil {
		a.spinner.Show()
	}
}

func (a *App) endWork() {
	a.pending--
	if a.pending <= 0 {
		a.pending = 0
		if a.spinner != nil {
			a.spinner.Hide()
		}
	}
}

// recoverPanic keeps one failed operation from taking the whole application
// down, and makes sure the user hears about it rather than watching nothing
// happen.
func (a *App) recoverPanic() {
	v := recover()
	if v == nil {
		return
	}
	a.log.Error("recovered from panic in a background operation", "panic", fmt.Sprint(v))
	a.onMain(func() {
		a.endWork()
		a.status.failure("Something went wrong. The details have been written to the log file.")
	})
}

// showError logs the full error and shows the user the readable part.
func (a *App) showError(err error) {
	if err == nil {
		return
	}
	a.log.Error("operation failed", "error", err)
	a.status.failure("%s", humanError(err))
}
