package ui

import (
	"context"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/service"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// dashboardView is what the application opens on.
//
// It answers one question — is there anything I need to deal with today — and
// every card is a link to the screen that deals with it. A dashboard whose
// numbers cannot be clicked is decoration.
type dashboardView struct {
	app *App

	content fyne.CanvasObject

	openOrdersCard *statCard
	lateCard       *statCard
	coverageCard   *statCard
	valueCard      *statCard

	alertBox    *fyne.Container
	activityBox *fyne.Container
	topValueBox *fyne.Container
}

func newDashboardView(a *App) *dashboardView {
	v := &dashboardView{app: a}
	v.build()
	return v
}

func (v *dashboardView) title() string { return "Dashboard" }

func (v *dashboardView) object() fyne.CanvasObject { return v.content }

func (v *dashboardView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("New order", theme.ContentAddIcon(), widget.HighImportance,
			func() { v.app.openOrderForm(nil) }),
	}
}

func (v *dashboardView) build() {
	// The dashboard leads with the order book, because the business runs on
	// what has been promised to clients. Stock value matters, but nobody opens
	// the application in the morning to find out what the shelf is worth.
	v.openOrdersCard = newStatCard("Open orders")
	v.lateCard = newStatCard("Past cancel date")
	v.coverageCard = newStatCard("Cannot ship")
	v.valueCard = newStatCard("Stock value")

	cards := container.NewGridWithColumns(4,
		clickableCard(v.openOrdersCard, func() { v.app.show(viewOrders) }),
		clickableCard(v.lateCard, func() { v.app.showLateOrders() }),
		clickableCard(v.coverageCard, func() { v.app.showCoverageReport() }),
		clickableCard(v.valueCard, func() { v.app.showValuationReport() }),
	)

	v.alertBox = container.NewVBox()
	v.activityBox = container.NewVBox()
	v.topValueBox = container.NewVBox()

	columns := container.NewGridWithColumns(2,
		widget.NewCard("Recent stock activity", "", v.activityBox),
		widget.NewCard("Where the value sits", "", v.topValueBox),
	)

	v.content = container.NewVScroll(container.NewVBox(
		cards,
		v.alertBox,
		columns,
	))
}

// clickableCard wraps a stat card so the whole tile navigates.
func clickableCard(card *statCard, action func()) fyne.CanvasObject {
	button := widget.NewButton("", action)
	button.Importance = widget.LowImportance
	return container.NewStack(button, card.object())
}

func (v *dashboardView) reload() {
	v.app.background(func(ctx context.Context) {
		summary, err := v.app.svc.Summary(ctx, v.app.location)
		if err != nil {
			v.app.onMain(func() { v.app.showError(wrapf(err, "the dashboard could not be loaded")) })
			return
		}
		book, bookErr := v.app.svc.OrderBookSummary(ctx)
		if bookErr != nil {
			v.app.onMain(func() { v.app.showError(wrapf(bookErr, "the order book could not be loaded")) })
			return
		}
		coverage, coverErr := v.app.svc.Coverage(ctx, v.app.location)
		if coverErr != nil {
			v.app.onMain(func() { v.app.showError(wrapf(coverErr, "coverage could not be calculated")) })
			return
		}

		v.app.onMain(func() { v.render(summary, book, coverage) })
	})
}

func (v *dashboardView) render(d service.Dashboard, book service.OrderBook, coverage service.DemandCoverage) {
	v.openOrdersCard.set(formatQuantity(int64(book.OpenOrders)),
		sprintf("%s units outstanding for %s, worth %s",
			formatQuantity(book.OpenUnits),
			pluralize(book.Customers, "customer", "customers"),
			book.OpenValue.Display()),
		widget.MediumImportance)

	lateImportance := widget.SuccessImportance
	lateDetail := sprintf("%s ship within 14 days", formatQuantity(int64(book.ShippingSoon)))
	if book.Late > 0 {
		lateImportance = widget.DangerImportance
		lateDetail = "The client can refuse these — click to see them"
	}
	v.lateCard.set(formatQuantity(int64(book.Late)), lateDetail, lateImportance)

	coverImportance := widget.SuccessImportance
	coverDetail := "Every open order is covered by stock"
	if coverage.ShortLines > 0 {
		coverImportance = widget.DangerImportance
		coverDetail = sprintf("%s short across %s, %s to cover",
			pluralize(coverage.ShortLines, "product", "products"),
			pluralize(coverage.BlockedOrders, "order", "orders"),
			coverage.ShortValue.Display())
	}
	v.coverageCard.set(formatQuantity(int64(coverage.BlockedOrders)), coverDetail, coverImportance)

	v.valueCard.set(d.StockValue.Display(),
		sprintf("%s units across %s, by %s",
			formatQuantity(d.TotalUnits),
			pluralize(d.ActiveProducts, "product", "products"),
			d.ValuationMethod.Label()),
		widget.MediumImportance)

	v.renderAlerts(d, book, coverage)
	v.renderActivity(d)
	v.renderTopValue(d)
}

// renderAlerts surfaces only conditions that need a decision. An always-present
// banner is one people stop reading.
func (v *dashboardView) renderAlerts(d service.Dashboard, book service.OrderBook, coverage service.DemandCoverage) {
	v.alertBox.RemoveAll()

	// Client-facing problems come first: a late order costs money today, a
	// negative stock figure costs an afternoon of investigation.
	if book.Late > 0 {
		v.alertBox.Add(alertRow(
			sprintf("%s past the cancel date", pluralize(book.Late, "order is", "orders are")),
			"The client is entitled to refuse these deliveries. Ship, re-date or cancel them.",
			widget.DangerImportance,
			"Show them", v.app.showLateOrders))
	}
	if coverage.ShortLines > 0 {
		v.alertBox.Add(alertRow(
			sprintf("%s cannot be shipped in full",
				pluralize(coverage.BlockedOrders, "order", "orders")),
			sprintf("%s short of what has been promised. Covering the gap costs about %s.",
				pluralize(coverage.ShortLines, "product is", "products are"),
				coverage.ShortValue.Display()),
			widget.WarningImportance,
			"View coverage", v.app.showCoverageReport))
	}

	if d.NegativeStock > 0 {
		v.alertBox.Add(alertRow(
			sprintf("%s below zero", pluralize(d.NegativeStock, "product is", "products are")),
			"Negative stock means the ledger and the shelf disagree. Count the items to correct it.",
			widget.DangerImportance,
			"Show them", func() { v.app.showStockState(storage.StockNegative) }))
	}
	if d.ExpiringSoon > 0 {
		v.alertBox.Add(alertRow(
			sprintf("%s expiring within 30 days", pluralize(d.ExpiringSoon, "lot is", "lots are")),
			"Sell, move or write these off before they expire.",
			widget.WarningImportance,
			"View report", func() { v.app.showExpiryReport() }))
	}
	v.alertBox.Refresh()
}

func alertRow(title, detail string, importance widget.Importance, actionLabel string, action func()) fyne.CanvasObject {
	heading := widget.NewLabel(title)
	heading.TextStyle = fyne.TextStyle{Bold: true}
	heading.Importance = importance

	body := widget.NewLabel(detail)
	body.Importance = widget.LowImportance
	body.Wrapping = fyne.TextWrapWord

	button := widget.NewButton(actionLabel, action)

	return widget.NewCard("", "", container.NewBorder(
		nil, nil, nil, container.NewVBox(button),
		container.NewVBox(heading, body),
	))
}

func (v *dashboardView) renderActivity(d service.Dashboard) {
	v.activityBox.RemoveAll()

	if len(d.RecentActivity) == 0 {
		note := widget.NewLabel("Nothing has moved yet.")
		note.Importance = widget.LowImportance
		v.activityBox.Add(note)
		v.activityBox.Refresh()
		return
	}

	now := time.Now()
	for _, entry := range d.RecentActivity {
		v.activityBox.Add(movementRow(entry.Movement, entry.SKU, now))
	}

	more := widget.NewButton("See all activity", func() { v.app.show(viewMovements) })
	more.Importance = widget.LowImportance
	v.activityBox.Add(more)
	v.activityBox.Refresh()
}

func (v *dashboardView) renderTopValue(d service.Dashboard) {
	v.topValueBox.RemoveAll()

	if len(d.TopValue) == 0 {
		note := widget.NewLabel("No stock is on hand yet.")
		note.Importance = widget.LowImportance
		v.topValueBox.Add(note)
		v.topValueBox.Refresh()
		return
	}

	for _, line := range d.TopValue {
		name := widget.NewLabel(line.SKU + " — " + line.Name)
		name.Truncation = fyne.TextTruncateEllipsis

		value := widget.NewLabel(line.Value.Display())
		value.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
		value.Alignment = fyne.TextAlignTrailing

		quantity := widget.NewLabel(formatQuantity(line.OnHand))
		quantity.Importance = widget.LowImportance
		quantity.Alignment = fyne.TextAlignTrailing

		v.topValueBox.Add(container.NewBorder(nil, nil, nil,
			container.NewHBox(
				container.NewGridWrap(fyne.NewSize(70, 30), quantity),
				container.NewGridWrap(fyne.NewSize(110, 30), value),
			),
			name,
		))
	}

	more := widget.NewButton("Open the valuation report", func() { v.app.showValuationReport() })
	more.Importance = widget.LowImportance
	v.topValueBox.Add(more)
	v.topValueBox.Refresh()
}
