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

// movementPageSize bounds one screenful of ledger history.
const movementPageSize = 400

// movementsView is the stock ledger: the append-only record of everything that
// has happened to stock, which is what makes a variance traceable.
type movementsView struct {
	app *App

	content   fyne.CanvasObject
	table     *DataTable[service.ActivityEntry]
	reasonSel *widget.Select
	summary   *widget.Label

	filter storage.MovementFilter
}

var movementReasonFilters = []struct {
	label  string
	reason core.MovementReason
}{
	{"All reasons", ""},
	{core.ReasonOpeningBalance.Label(), core.ReasonOpeningBalance},
	{core.ReasonReceipt.Label(), core.ReasonReceipt},
	{core.ReasonSale.Label(), core.ReasonSale},
	{core.ReasonAdjustment.Label(), core.ReasonAdjustment},
	{core.ReasonStockCount.Label(), core.ReasonStockCount},
	{core.ReasonWriteOff.Label(), core.ReasonWriteOff},
	{core.ReasonReturn.Label(), core.ReasonReturn},
}

func newMovementsView(a *App) *movementsView {
	v := &movementsView{
		app:    a,
		filter: storage.MovementFilter{Limit: movementPageSize},
	}
	v.build()
	return v
}

func (v *movementsView) title() string { return "Stock activity" }

func (v *movementsView) object() fyne.CanvasObject { return v.content }

func (v *movementsView) actions() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		toolbarButton("Refresh", theme.ViewRefreshIcon(), widget.MediumImportance, v.reload),
	}
}

func (v *movementsView) build() {
	v.table = NewDataTable(movementColumns())
	v.table.OnActivate = func(row service.ActivityEntry, _ int) {
		v.app.searchCatalogue(row.SKU)
	}

	labels := make([]string, len(movementReasonFilters))
	for i, r := range movementReasonFilters {
		labels[i] = r.label
	}
	v.reasonSel = widget.NewSelect(labels, nil)
	v.reasonSel.SetSelectedIndex(0)

	v.summary = widget.NewLabel("")
	v.summary.Importance = widget.LowImportance

	// Wired after the widgets it touches exist. See buildFilterBar.
	v.reasonSel.OnChanged = func(string) {
		i := v.reasonSel.SelectedIndex()
		if i >= 0 && i < len(movementReasonFilters) {
			v.filter.Reason = movementReasonFilters[i].reason
		}
		v.reload()
	}

	note := widget.NewLabel(
		"Every change to stock is recorded here and can never be edited or deleted. " +
			"A mistake is corrected by posting an offsetting entry.")
	note.Importance = widget.LowImportance
	note.Wrapping = fyne.TextWrapWord

	header := container.NewVBox(
		container.NewBorder(nil, nil, nil,
			container.NewGridWrap(fyne.NewSize(200, 36), v.reasonSel),
			note,
		),
		widget.NewSeparator(),
	)
	footer := container.NewVBox(widget.NewSeparator(), v.summary)

	v.content = container.NewBorder(header, footer, nil, nil, v.table.Object())
}

func (v *movementsView) reload() {
	v.filter.LocationID = v.app.location
	v.filter.Limit = movementPageSize
	filter := v.filter

	v.app.background(func(ctx context.Context) {
		movements, err := v.app.svc.MovementHistory(ctx, filter)
		if err != nil {
			v.app.onMain(func() { v.app.showError(wrapf(err, "the stock activity could not be loaded")) })
			return
		}

		// The ledger stores product ids, so the service resolves them — once
		// per distinct product rather than once per row.
		rows, err := v.app.svc.DescribeMovements(ctx, movements)
		if err != nil {
			v.app.onMain(func() { v.app.showError(wrapf(err, "the stock activity could not be loaded")) })
			return
		}

		v.app.onMain(func() {
			v.table.SetRows(rows, "No stock has moved yet.\n\nReceiving or counting an item will record it here.")
			v.summary.SetText(sprintf("%s — newest first", pluralize(len(rows), "movement", "movements")))
		})
	})
}

func movementColumns() []Column[service.ActivityEntry] {
	return []Column[service.ActivityEntry]{
		{
			Title: "When", Width: 160,
			Value: func(r service.ActivityEntry) string { return formatDateTime(r.Movement.OccurredAt) },
		},
		{
			Title: "SKU", Width: 140, Monospace: true,
			Value: func(r service.ActivityEntry) string { return dash(r.SKU) },
			Bold:  func(service.ActivityEntry) bool { return true },
		},
		{
			Title: "Product", Width: 250,
			Value: func(r service.ActivityEntry) string { return dash(r.Name) },
		},
		{
			Title: "Change", Width: 120, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(r service.ActivityEntry) string {
				return formatDelta(r.Movement.QtyDelta) + " " + string(r.Unit)
			},
			Importance: func(r service.ActivityEntry) widget.Importance {
				return deltaImportance(r.Movement.QtyDelta)
			},
			Bold: func(service.ActivityEntry) bool { return true },
		},
		{
			Title: "Reason", Width: 150,
			Value: func(r service.ActivityEntry) string { return r.Movement.Reason.Label() },
		},
		{
			Title: "Unit cost", Width: 110, Align: fyne.TextAlignTrailing, Monospace: true,
			Value: func(r service.ActivityEntry) string {
				if r.Movement.UnitCost.IsZero() {
					return "—"
				}
				return r.Movement.UnitCost.Display()
			},
		},
		{
			Title: "Lot", Width: 130,
			Value: func(r service.ActivityEntry) string { return dash(r.Movement.LotNumber) },
		},
		{
			Title: "Expires", Width: 120,
			Value: func(r service.ActivityEntry) string {
				if r.Movement.ExpiryDate.IsZero() {
					return "—"
				}
				return formatDate(r.Movement.ExpiryDate)
			},
			Importance: func(r service.ActivityEntry) widget.Importance {
				if !r.Movement.ExpiryDate.IsZero() && r.Movement.ExpiryDate.Before(time.Now()) {
					return widget.DangerImportance
				}
				return widget.MediumImportance
			},
		},
		{
			Title: "Note", Width: 280,
			Value: func(r service.ActivityEntry) string { return dash(r.Movement.Note) },
		},
	}
}
