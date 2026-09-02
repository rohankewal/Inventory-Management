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
)

// reportID names a report.
type reportID string

const (
	reportCoverage  reportID = "coverage"
	reportValuation reportID = "valuation"
	reportReorder   reportID = "reorder"
	reportABC       reportID = "abc"
	reportAging     reportID = "aging"
	reportExpiry    reportID = "expiry"
)

// reportsView holds the analysis screens.
//
// Each answers a question a manager actually asks: what is the stock worth,
// what should I buy, what deserves my attention, what is not selling, and what
// is about to go out of date.
type reportsView struct {
	app *App

	content fyne.CanvasObject
	tabs    *container.AppTabs

	coverageSummary *widget.Label
	coverageTable   *DataTable[service.CoverageLine]

	valuationSummary *widget.Label
	valuationTable   *DataTable[core.ProductValuation]

	reorderSummary *widget.Label
	reorderTable   *DataTable[service.ReorderLine]

	abcSummary *widget.Label
	abcTable   *DataTable[core.ABCLine]

	agingSummary *widget.Label
	agingTable   *DataTable[service.AgingLine]

	expirySummary *widget.Label
	expiryTable   *DataTable[service.ExpiringLot]
}

func newReportsView(a *App) *reportsView {
	v := &reportsView{app: a}
	v.build()
	return v
}

func (v *reportsView) title() string { return "Reports" }

func (v *reportsView) object() fyne.CanvasObject { return v.content }

func (v *reportsView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("Refresh", theme.ViewRefreshIcon(), widget.MediumImportance, v.reload),
	}
}

func (v *reportsView) build() {
	v.coverageSummary, v.coverageTable = newReportPane(coverageColumns())
	v.valuationSummary, v.valuationTable = newReportPane(valuationColumns())
	v.reorderSummary, v.reorderTable = newReportPane(reorderColumns())
	v.abcSummary, v.abcTable = newReportPane(abcColumns())
	v.agingSummary, v.agingTable = newReportPane(agingColumns())
	v.expirySummary, v.expiryTable = newReportPane(expiryColumns())

	v.reorderTable.OnActivate = func(row service.ReorderLine, _ int) {
		v.app.openStockDialog(row.Product, stockActionReceive)
	}

	v.tabs = container.NewAppTabs(
		container.NewTabItem("Order coverage", reportPane(v.coverageSummary, v.coverageTable.Object())),
		container.NewTabItem("Valuation", reportPane(v.valuationSummary, v.valuationTable.Object())),
		container.NewTabItem("What to reorder", reportPane(v.reorderSummary, v.reorderTable.Object())),
		container.NewTabItem("ABC analysis", reportPane(v.abcSummary, v.abcTable.Object())),
		container.NewTabItem("Stock aging", reportPane(v.agingSummary, v.agingTable.Object())),
		container.NewTabItem("Expiring lots", reportPane(v.expirySummary, v.expiryTable.Object())),
	)
	v.tabs.OnSelected = func(*container.TabItem) { v.reload() }

	v.content = v.tabs
}

// newReportPane builds the summary line and grid shared by every report.
func newReportPane[T any](columns []Column[T]) (*widget.Label, *DataTable[T]) {
	summary := widget.NewLabel("")
	summary.Wrapping = fyne.TextWrapWord
	return summary, NewDataTable(columns)
}

func reportPane(summary *widget.Label, table fyne.CanvasObject) fyne.CanvasObject {
	return container.NewBorder(
		container.NewVBox(container.NewPadded(summary), widget.NewSeparator()),
		nil, nil, nil, table,
	)
}

// showTab opens one report from elsewhere in the application.
func (v *reportsView) showTab(id reportID) {
	index := map[reportID]int{
		reportCoverage: 0, reportValuation: 1, reportReorder: 2,
		reportABC: 3, reportAging: 4, reportExpiry: 5,
	}[id]
	v.tabs.SelectIndex(index)
	v.reload()
}

func (v *reportsView) reload() {
	scope := service.ReportScope{LocationID: v.app.location}

	switch v.tabs.SelectedIndex() {
	case 0:
		v.loadCoverage()
	case 1:
		v.loadValuation(scope)
	case 2:
		v.loadReorder(scope)
	case 3:
		v.loadABC(scope)
	case 4:
		v.loadAging(scope)
	case 5:
		v.loadExpiry()
	}
}

// loadCoverage answers "can we ship what we have promised", which is the
// report this business opens first.
func (v *reportsView) loadCoverage() {
	v.app.background(func(ctx context.Context) {
		coverage, err := v.app.svc.Coverage(ctx, v.app.location)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(wrapf(err, "coverage could not be calculated"))
				return
			}

			if coverage.ShortLines == 0 {
				v.coverageSummary.SetText(sprintf(
					"Every open order is covered. %s committed across the book.",
					pluralize(len(coverage.Lines), "product", "products")))
			} else {
				v.coverageSummary.SetText(sprintf(
					"%s short, blocking %s. Covering the gap costs about %s.",
					pluralize(coverage.ShortLines, "product is", "products are"),
					pluralize(coverage.BlockedOrders, "order", "orders"),
					coverage.ShortValue.Display()))
			}
			v.coverageTable.SetRows(coverage.Lines,
				"No open orders.\n\nCoverage compares what clients have ordered against what is on the shelf.")
		})
	})
}

func (v *reportsView) loadValuation(scope service.ReportScope) {
	v.app.background(func(ctx context.Context) {
		valuation, err := v.app.svc.Valuation(ctx, scope)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(wrapf(err, "the valuation could not be calculated"))
				return
			}
			v.valuationSummary.SetText(sprintf(
				"Stock on hand is worth %s, calculated by %s. Cost of goods issued to date is %s.",
				valuation.Total.Display(), valuation.Method.Label(), valuation.COGS.Display()))
			v.valuationTable.SetRows(valuation.Lines, "There is no stock to value yet.")
		})
	})
}

func (v *reportsView) loadReorder(scope service.ReportScope) {
	v.app.background(func(ctx context.Context) {
		report, err := v.app.svc.ReorderSuggestions(ctx, scope)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(wrapf(err, "the reorder report could not be built"))
				return
			}
			v.reorderSummary.SetText(sprintf(
				"%s to reorder from %s, costing about %s. Press Enter on a row to receive it.",
				pluralize(len(report.Lines), "item", "items"),
				pluralize(len(report.BySupplier), "supplier", "suppliers"),
				report.Total.Display()))
			v.reorderTable.SetRows(report.Lines,
				"Nothing needs reordering.\n\nSet a reorder point on a product to have it appear here.")
		})
	})
}

func (v *reportsView) loadABC(scope service.ReportScope) {
	v.app.background(func(ctx context.Context) {
		lines, err := v.app.svc.ABCAnalysis(ctx, scope)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(wrapf(err, "the ABC analysis could not be built"))
				return
			}

			var a, b, c int
			for _, line := range lines {
				switch line.Class {
				case core.ClassA:
					a++
				case core.ClassB:
					b++
				default:
					c++
				}
			}
			v.abcSummary.SetText(sprintf(
				"%d class A items hold 80%% of the value, %d class B hold the next 15%%, and %d class C hold the rest. "+
					"Count and watch the A items most closely.", a, b, c))
			v.abcTable.SetRows(lines, "There is no stock to classify yet.")
		})
	})
}

func (v *reportsView) loadAging(scope service.ReportScope) {
	v.app.background(func(ctx context.Context) {
		report, err := v.app.svc.StockAging(ctx, scope)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(wrapf(err, "the aging report could not be built"))
				return
			}

			dead := report.Totals[core.AgingDead]
			v.agingSummary.SetText(sprintf(
				"%s holding stock. %s sitting in %s has not moved in over a year.",
				pluralize(len(report.Lines), "product is", "products are"),
				dead.Display(),
				pluralize(report.Counts[core.AgingDead], "product", "products")))
			v.agingTable.SetRows(report.Lines, "No stock is on hand yet.")
		})
	})
}

func (v *reportsView) loadExpiry() {
	// Ninety days is far enough ahead to act on and near enough to be real.
	const window = 90 * 24 * time.Hour

	v.app.background(func(ctx context.Context) {
		lots, err := v.app.svc.ExpiringLots(ctx, window)
		v.app.onMain(func() {
			if err != nil {
				v.app.showError(wrapf(err, "the expiry report could not be built"))
				return
			}

			var expired int
			for _, lot := range lots {
				if lot.DaysRemaining < 0 {
					expired++
				}
			}
			v.expirySummary.SetText(sprintf(
				"%s expiring within 90 days, of which %d %s already past their date.",
				pluralize(len(lots), "lot is", "lots are"), expired, isAre(expired)))
			v.expiryTable.SetRows(lots,
				"No lots are expiring.\n\nTurn on lot tracking for a product to record batch numbers and expiry dates.")
		})
	})
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// --- report columns ---------------------------------------------------------

func coverageColumns() []Column[service.CoverageLine] {
	return []Column[service.CoverageLine]{
		{Title: "SKU", Width: 140, Monospace: true,
			Value: func(l service.CoverageLine) string { return l.SKU },
			Bold:  func(service.CoverageLine) bool { return true }},
		{Title: "Product", Width: 260, Value: func(l service.CoverageLine) string { return l.Name }},
		{Title: "Committed", Width: 110, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.CoverageLine) string { return formatQuantity(l.Committed) }},
		{Title: "On hand", Width: 110, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.CoverageLine) string { return formatQuantity(l.OnHand) }},
		{Title: "Short", Width: 110, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.CoverageLine) string {
				if l.Short == 0 {
					return "—"
				}
				return formatQuantity(l.Short)
			},
			Bold: func(l service.CoverageLine) bool { return l.Short > 0 },
			Importance: func(l service.CoverageLine) widget.Importance {
				if l.Short > 0 {
					return widget.DangerImportance
				}
				return widget.SuccessImportance
			}},
		{Title: "Cost to cover", Width: 140, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.CoverageLine) string {
				if l.Short == 0 {
					return "—"
				}
				return l.ShortValue.Display()
			}},
		{Title: "Orders", Width: 90, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.CoverageLine) string { return formatQuantity(int64(l.Orders)) }},
		{Title: "Needed by", Width: 130,
			Value: func(l service.CoverageLine) string { return formatDate(l.EarliestShipDate) },
			Importance: func(l service.CoverageLine) widget.Importance {
				if l.Short > 0 {
					return widget.WarningImportance
				}
				return widget.LowImportance
			}},
		{Title: "Supplier", Width: 180,
			Value: func(l service.CoverageLine) string { return dash(l.Supplier) }},
	}
}

func valuationColumns() []Column[core.ProductValuation] {
	return []Column[core.ProductValuation]{
		{Title: "SKU", Width: 140, Monospace: true,
			Value: func(l core.ProductValuation) string { return l.SKU },
			Bold:  func(core.ProductValuation) bool { return true }},
		{Title: "Product", Width: 280, Value: func(l core.ProductValuation) string { return l.Name }},
		{Title: "Category", Width: 140, Value: func(l core.ProductValuation) string { return dash(l.Category) }},
		{Title: "On hand", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ProductValuation) string { return formatQuantity(l.OnHand) }},
		{Title: "Unit cost", Width: 120, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ProductValuation) string { return l.UnitCost.Display() }},
		{Title: "Stock value", Width: 140, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ProductValuation) string { return l.Value.Display() },
			Bold:  func(core.ProductValuation) bool { return true }},
		{Title: "Cost of goods issued", Width: 180, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ProductValuation) string { return l.COGS.Display() }},
	}
}

func reorderColumns() []Column[service.ReorderLine] {
	return []Column[service.ReorderLine]{
		{Title: "Supplier", Width: 190,
			Value: func(l service.ReorderLine) string { return dash(l.Product.Supplier) }},
		{Title: "SKU", Width: 140, Monospace: true,
			Value: func(l service.ReorderLine) string { return l.Product.SKU },
			Bold:  func(service.ReorderLine) bool { return true }},
		{Title: "Product", Width: 260, Value: func(l service.ReorderLine) string { return l.Product.Name }},
		{Title: "On hand", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value:      func(l service.ReorderLine) string { return formatQuantity(l.Product.OnHand) },
			Importance: func(l service.ReorderLine) widget.Importance { return quantityImportance(l.Product) }},
		{Title: "Reorder at", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.ReorderLine) string { return formatQuantity(l.Product.ReorderPoint) }},
		{Title: "Order", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.ReorderLine) string { return formatQuantity(l.Suggested) },
			Bold:  func(service.ReorderLine) bool { return true }},
		{Title: "Estimated cost", Width: 150, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.ReorderLine) string { return l.EstimatedCost.Display() }},
	}
}

func abcColumns() []Column[core.ABCLine] {
	return []Column[core.ABCLine]{
		{Title: "Class", Width: 80, Align: fyne.TextAlignCenter,
			Value: func(l core.ABCLine) string { return string(l.Class) },
			Bold:  func(core.ABCLine) bool { return true },
			Importance: func(l core.ABCLine) widget.Importance {
				switch l.Class {
				case core.ClassA:
					return widget.DangerImportance
				case core.ClassB:
					return widget.WarningImportance
				}
				return widget.LowImportance
			}},
		{Title: "SKU", Width: 140, Monospace: true,
			Value: func(l core.ABCLine) string { return l.SKU }},
		{Title: "Product", Width: 280, Value: func(l core.ABCLine) string { return l.Name }},
		{Title: "Stock value", Width: 140, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ABCLine) string { return l.Value.Display() }},
		{Title: "Share", Width: 90, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ABCLine) string { return sprintf("%.1f%%", l.ShareOfValue) }},
		{Title: "Cumulative", Width: 110, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l core.ABCLine) string { return sprintf("%.1f%%", l.CumulativeShare) }},
	}
}

func agingColumns() []Column[service.AgingLine] {
	return []Column[service.AgingLine]{
		{Title: "SKU", Width: 140, Monospace: true,
			Value: func(l service.AgingLine) string { return l.Product.SKU },
			Bold:  func(service.AgingLine) bool { return true }},
		{Title: "Product", Width: 270, Value: func(l service.AgingLine) string { return l.Product.Name }},
		{Title: "On hand", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.AgingLine) string { return formatQuantity(l.Product.OnHand) }},
		{Title: "Value", Width: 130, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.AgingLine) string { return l.Value.Display() }},
		{Title: "Last moved", Width: 140,
			Value: func(l service.AgingLine) string {
				return formatRelative(l.Product.LastMovementAt, time.Now())
			}},
		{Title: "Age", Width: 200,
			Value: func(l service.AgingLine) string { return l.Bucket.Label() },
			Importance: func(l service.AgingLine) widget.Importance {
				switch l.Bucket {
				case core.AgingDead:
					return widget.DangerImportance
				case core.Aging180to365:
					return widget.WarningImportance
				}
				return widget.MediumImportance
			}},
	}
}

func expiryColumns() []Column[service.ExpiringLot] {
	return []Column[service.ExpiringLot]{
		{Title: "Expires", Width: 130,
			Value: func(l service.ExpiringLot) string { return formatDate(l.Movement.ExpiryDate) },
			Bold:  func(service.ExpiringLot) bool { return true },
			Importance: func(l service.ExpiringLot) widget.Importance {
				if l.DaysRemaining < 0 {
					return widget.DangerImportance
				}
				if l.DaysRemaining < 30 {
					return widget.WarningImportance
				}
				return widget.MediumImportance
			}},
		{Title: "Days left", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.ExpiringLot) string {
				if l.DaysRemaining < 0 {
					return sprintf("%d overdue", -l.DaysRemaining)
				}
				return formatQuantity(int64(l.DaysRemaining))
			}},
		{Title: "Lot", Width: 150, Monospace: true,
			Value: func(l service.ExpiringLot) string { return l.Movement.LotNumber }},
		{Title: "SKU", Width: 140, Monospace: true,
			Value: func(l service.ExpiringLot) string { return l.Product.SKU }},
		{Title: "Product", Width: 260, Value: func(l service.ExpiringLot) string { return l.Product.Name }},
		{Title: "Received", Width: 100, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(l service.ExpiringLot) string { return formatQuantity(l.Movement.QtyDelta) }},
	}
}
