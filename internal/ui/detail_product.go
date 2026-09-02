package ui

import (
	"context"
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

// historyDepth is how many recent movements the detail panel shows.
const historyDepth = 8

// productDetailPanel is the inspector beside the catalogue.
//
// It exists so that answering "what is this thing and what has happened to it"
// costs one click rather than a modal, and so the stock actions someone
// performs dozens of times a day are always visible rather than buried in a
// menu.
type productDetailPanel struct {
	app *App

	root    *fyne.Container
	empty   *widget.Label
	body    *fyne.Container
	current *core.ProductWithStock

	nameLabel    *widget.Label
	skuLabel     *widget.Label
	statusLabel  *widget.Label
	onHandLabel  *widget.Label
	detailsGrid  *fyne.Container
	tagsBox      *fyne.Container
	historyBox   *fyne.Container
	actionRow    *fyne.Container
	archivedNote *widget.Label
}

func newProductDetailPanel(a *App) *productDetailPanel {
	p := &productDetailPanel{app: a}
	p.build()
	return p
}

func (p *productDetailPanel) object() fyne.CanvasObject { return p.root }

func (p *productDetailPanel) build() {
	p.empty = widget.NewLabel("Select a product to see its details,\nstock level and recent activity.")
	p.empty.Alignment = fyne.TextAlignCenter
	p.empty.Importance = widget.LowImportance
	p.empty.Wrapping = fyne.TextWrapWord

	p.nameLabel = widget.NewLabel("")
	p.nameLabel.TextStyle = fyne.TextStyle{Bold: true}
	p.nameLabel.SizeName = theme.SizeNameSubHeadingText
	p.nameLabel.Wrapping = fyne.TextWrapWord

	p.skuLabel = widget.NewLabel("")
	p.skuLabel.TextStyle = fyne.TextStyle{Monospace: true}
	p.skuLabel.Importance = widget.LowImportance

	p.onHandLabel = widget.NewLabel("")
	p.onHandLabel.TextStyle = fyne.TextStyle{Bold: true}
	p.onHandLabel.SizeName = theme.SizeNameHeadingText

	p.statusLabel = widget.NewLabel("")

	p.archivedNote = widget.NewLabel("This product is archived and hidden from the everyday catalogue.")
	p.archivedNote.Importance = widget.WarningImportance
	p.archivedNote.Wrapping = fyne.TextWrapWord
	p.archivedNote.Hide()

	p.detailsGrid = container.New(newFormGrid())
	p.tagsBox = container.NewVBox()
	p.historyBox = container.NewVBox()
	p.actionRow = container.NewVBox()

	p.body = container.NewVBox(
		p.nameLabel,
		p.skuLabel,
		p.archivedNote,
		widget.NewSeparator(),
		container.NewHBox(p.onHandLabel, p.statusLabel),
		p.actionRow,
		sectionHeading("Details"),
		p.detailsGrid,
		p.tagsBox,
		sectionHeading("Recent activity"),
		p.historyBox,
	)
	p.body.Hide()

	p.root = container.NewPadded(container.NewVScroll(container.NewStack(p.empty, p.body)))
}

func (p *productDetailPanel) clear() {
	p.current = nil
	p.body.Hide()
	p.empty.Show()
}

// show renders one product and loads its history.
func (p *productDetailPanel) show(product core.ProductWithStock) {
	p.current = &product
	p.empty.Hide()
	p.body.Show()

	p.nameLabel.SetText(product.Name)

	identity := product.SKU
	if product.Barcode != "" {
		identity += "     " + product.Barcode
	}
	p.skuLabel.SetText(identity)

	if product.NonStock {
		p.onHandLabel.SetText("Non-stock")
		p.onHandLabel.Importance = widget.LowImportance
		p.statusLabel.SetText("This item has no tracked quantity.")
		p.statusLabel.Importance = widget.LowImportance
	} else {
		p.onHandLabel.SetText(fmt.Sprintf("%s %s", formatQuantity(product.OnHand), product.Unit))
		p.onHandLabel.Importance = quantityImportance(product)
		p.statusLabel.SetText(p.stockCommentary(product))
		p.statusLabel.Importance = statusImportance(product.Status())
	}
	p.onHandLabel.Refresh()
	p.statusLabel.Refresh()

	if product.Active {
		p.archivedNote.Hide()
	} else {
		p.archivedNote.Show()
	}

	p.buildActions(product)
	p.buildDetails(product)
	p.buildTags(product)
	p.loadHistory(product)
}

// stockCommentary says what the number means, not just what it is.
func (p *productDetailPanel) stockCommentary(product core.ProductWithStock) string {
	switch product.Status() {
	case core.StatusOutOfStock:
		if suggested := product.SuggestedOrderQuantity(); suggested > 0 {
			return fmt.Sprintf("Out of stock — order %s", formatQuantity(suggested))
		}
		return "Out of stock"
	case core.StatusLow:
		return fmt.Sprintf("At or below the reorder point of %s — order %s",
			formatQuantity(product.ReorderPoint), formatQuantity(product.SuggestedOrderQuantity()))
	}
	if product.ReorderPoint > 0 {
		return fmt.Sprintf("%s above the reorder point",
			formatQuantity(product.OnHand-product.ReorderPoint))
	}
	return "In stock"
}

func (p *productDetailPanel) buildActions(product core.ProductWithStock) {
	p.actionRow.RemoveAll()

	edit := widget.NewButtonWithIcon("Edit", theme.DocumentCreateIcon(), func() {
		p.app.openProductForm(&product)
	})

	if product.NonStock {
		p.actionRow.Add(container.NewGridWithColumns(2, edit, p.archiveButton(product)))
		p.actionRow.Refresh()
		return
	}

	receive := widget.NewButtonWithIcon("Receive", theme.ContentAddIcon(), func() {
		p.app.openStockDialog(product, stockActionReceive)
	})
	receive.Importance = widget.HighImportance

	issue := widget.NewButtonWithIcon("Issue", theme.ContentRemoveIcon(), func() {
		p.app.openStockDialog(product, stockActionIssue)
	})

	count := widget.NewButtonWithIcon("Count", theme.ViewRefreshIcon(), func() {
		p.app.openStockDialog(product, stockActionCount)
	})

	p.actionRow.Add(container.NewGridWithColumns(3, receive, issue, count))
	p.actionRow.Add(container.NewGridWithColumns(2, edit, p.archiveButton(product)))
	p.actionRow.Refresh()
}

func (p *productDetailPanel) archiveButton(product core.ProductWithStock) *widget.Button {
	if !product.Active {
		return widget.NewButtonWithIcon("Restore", theme.ViewRestoreIcon(), func() {
			p.app.restoreProduct(product)
		})
	}
	// Deliberately not styled as a danger button. It is confirmed before it
	// does anything, and a wall of red beside the actions someone uses all day
	// trains them to ignore the colour.
	return widget.NewButtonWithIcon("Remove", theme.DeleteIcon(), func() {
		p.app.removeProduct(product)
	})
}

func (p *productDetailPanel) buildDetails(product core.ProductWithStock) {
	p.detailsGrid.RemoveAll()

	add := func(name, value string) {
		formRow(p.detailsGrid, name, wrappedLabel(value))
	}

	add("Category", dash(product.Category))
	add("Supplier", dash(product.Supplier))
	add("Unit", product.Unit.Label())
	add("Sale price", product.Price.Display())
	add("Unit cost", product.Cost.Display())

	if product.Price.Minor > 0 && product.Price.Currency == product.Cost.Currency {
		add("Margin", fmt.Sprintf("%s  (%.1f%%)",
			product.Margin().Display(), product.MarginPercent()))
	}
	if !product.NonStock {
		add("Stock value", product.StockValue().Display())
		if product.ReorderPoint > 0 {
			add("Reorder point", formatQuantity(product.ReorderPoint))
		}
		if product.ReorderQuantity > 0 {
			add("Reorder quantity", formatQuantity(product.ReorderQuantity))
		}
	}
	if product.TrackLots {
		add("Lot tracking", "Enabled")
	}
	if product.WeightGrams > 0 {
		add("Weight", fmt.Sprintf("%s g", formatQuantity(product.WeightGrams)))
	}
	add("Last moved", formatRelative(product.LastMovementAt, timeNow()))
	add("Updated", formatDateTime(product.UpdatedAt))

	if product.Description != "" {
		add("Description", product.Description)
	}
	if product.Notes != "" {
		add("Notes", product.Notes)
	}
	for _, key := range product.CustomFields.Keys() {
		add(key, product.CustomFields[key])
	}

	p.detailsGrid.Refresh()
}

func (p *productDetailPanel) buildTags(product core.ProductWithStock) {
	p.tagsBox.RemoveAll()
	if len(product.Tags) == 0 {
		p.tagsBox.Refresh()
		return
	}

	// Tags are rendered as buttons because their most useful behaviour is to
	// filter the catalogue down to everything else carrying the same label.
	chips := container.NewHBox()
	for _, tag := range product.Tags {
		tag := tag
		chip := widget.NewButton(tag, func() { p.app.filterByTag(tag) })
		chip.Importance = widget.LowImportance
		chips.Add(chip)
	}

	p.tagsBox.Add(sectionHeading("Tags"))
	p.tagsBox.Add(container.NewHScroll(chips))
	p.tagsBox.Refresh()
}

func (p *productDetailPanel) loadHistory(product core.ProductWithStock) {
	p.historyBox.RemoveAll()
	p.historyBox.Add(widget.NewLabel("Loading…"))
	p.historyBox.Refresh()

	p.app.background(func(ctx context.Context) {
		movements, err := p.app.svc.MovementHistory(ctx, storage.MovementFilter{
			ProductID: product.ID,
			Limit:     historyDepth,
		})

		p.app.onMain(func() {
			// The user may have clicked another row while this loaded.
			if p.current == nil || p.current.ID != product.ID {
				return
			}

			p.historyBox.RemoveAll()
			if err != nil {
				p.historyBox.Add(widget.NewLabel("Could not load the history."))
				p.historyBox.Refresh()
				return
			}
			if len(movements) == 0 {
				note := widget.NewLabel("No stock movements yet.")
				note.Importance = widget.LowImportance
				p.historyBox.Add(note)
				p.historyBox.Refresh()
				return
			}

			now := timeNow()
			for _, m := range movements {
				p.historyBox.Add(movementRow(m, "", now))
			}
			p.historyBox.Refresh()
		})
	})
}

// movementRow renders one ledger entry compactly: the signed quantity, what
// caused it, and when. Pass an empty subject on a screen that is already
// showing one product, so its own history does not repeat its name on every
// line.
func movementRow(m core.StockMovement, subject string, now time.Time) fyne.CanvasObject {
	delta := widget.NewLabel(formatDelta(m.QtyDelta))
	delta.TextStyle = fyne.TextStyle{Bold: true, Monospace: true}
	delta.Alignment = fyne.TextAlignTrailing
	delta.Importance = deltaImportance(m.QtyDelta)

	reason := m.Reason.Label()
	if subject != "" {
		reason = subject + " — " + reason
	}
	if m.Note != "" {
		reason += " — " + m.Note
	}
	if m.LotNumber != "" {
		reason += "  [lot " + m.LotNumber + "]"
	}
	label := widget.NewLabel(reason)
	label.Truncation = fyne.TextTruncateEllipsis

	when := widget.NewLabel(formatRelative(m.OccurredAt, now))
	when.Importance = widget.LowImportance
	when.Alignment = fyne.TextAlignTrailing

	return container.NewBorder(nil, nil,
		container.NewGridWrap(fyne.NewSize(70, 30), delta),
		container.NewGridWrap(fyne.NewSize(110, 30), when),
		label,
	)
}

// timeNow is the panel's clock, kept in one place so tests can reason about
// the relative timestamps this file renders.
func timeNow() time.Time { return time.Now() }
