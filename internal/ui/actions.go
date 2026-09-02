package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/service"
)

// sprintf is a local alias so format helpers read cleanly in this package.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// --- product mutations ------------------------------------------------------

func (a *App) createProduct(product core.Product, opening service.OpeningStock) {
	a.background(func(ctx context.Context) {
		created, err := a.svc.CreateProduct(ctx, product, opening)
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			a.status.success("Created %s — %s.", created.SKU, created.Name)
			a.reloadAll()
		})
	})
}

func (a *App) saveProduct(product core.Product) {
	a.background(func(ctx context.Context) {
		saved, err := a.svc.UpdateProduct(ctx, product)
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			a.status.success("Saved %s.", saved.SKU)
			a.reloadAll()
		})
	})
}

// removeProduct confirms first, and says plainly what will happen: a product
// with history is archived rather than deleted, and being told that afterwards
// is much less useful than being told before.
func (a *App) removeProduct(product core.ProductWithStock) {
	message := sprintf(
		"Remove %s — %s?\n\nIf it has any stock history it will be archived rather than "+
			"deleted, so its records and reports stay intact. You can restore it later.",
		product.SKU, product.Name)

	a.confirm("Remove product", message, "Remove", func() {
		a.background(func(ctx context.Context) {
			outcome, err := a.svc.DeleteProduct(ctx, product.ID, core.NilID)
			a.onMain(func() {
				if err != nil {
					a.showError(err)
					return
				}
				if outcome == service.OutcomeArchived {
					a.status.warn("Archived %s. Its stock history was kept.", product.SKU)
				} else {
					a.status.success("Removed %s.", product.SKU)
				}
				a.reloadAll()
			})
		})
	})
}

func (a *App) restoreProduct(product core.ProductWithStock) {
	a.background(func(ctx context.Context) {
		err := a.svc.RestoreProduct(ctx, product.ID)
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			a.status.success("Restored %s.", product.SKU)
			a.reloadAll()
		})
	})
}

// openProductDetail brings a product on screen, used after a successful scan.
func (a *App) openProductDetail(product core.ProductWithStock) {
	products, ok := a.views[viewProducts].(*productsView)
	if !ok {
		return
	}
	a.show(viewProducts)
	products.setSearch(product.SKU)
}

// filterByTag narrows the catalogue to one tag.
func (a *App) filterByTag(tag string) {
	products, ok := a.views[viewProducts].(*productsView)
	if !ok {
		return
	}
	a.show(viewProducts)
	products.filter.Tag = tag
	products.reload()
	a.status.info("Filtered to items tagged %q.", tag)
}

// --- stock movements --------------------------------------------------------

// stockAction is which of the three stock operations a dialog performs.
type stockAction int

const (
	stockActionReceive stockAction = iota
	stockActionIssue
	stockActionCount
)

func (s stockAction) title() string {
	switch s {
	case stockActionIssue:
		return "Issue stock"
	case stockActionCount:
		return "Record a stock count"
	}
	return "Receive stock"
}

func (s stockAction) confirmLabel() string {
	switch s {
	case stockActionIssue:
		return "Issue"
	case stockActionCount:
		return "Record count"
	}
	return "Receive"
}

// openStockDialog performs one stock operation on one product.
//
// Receiving, issuing and counting are separate dialogs rather than one
// "adjust" box with a sign, because they ask different questions: a receipt
// needs a cost, a count needs the number on the shelf rather than a delta, and
// conflating them is how ledgers end up full of unexplained adjustments.
func (a *App) openStockDialog(product core.ProductWithStock, action stockAction) {
	quantity := newEntry("0")
	note := newEntry("Optional — why")

	cost := newEntry(product.Cost.String())
	lot := newEntry("Lot or batch number")
	expiry := newEntry("YYYY-MM-DD")

	reasonLabels := make([]string, len(core.AdjustmentReasons))
	for i, r := range core.AdjustmentReasons {
		reasonLabels[i] = r.Label()
	}
	reason := widget.NewSelect(reasonLabels, nil)
	reason.SetSelectedIndex(0)

	current := widget.NewLabel(sprintf("%s %s on hand", formatQuantity(product.OnHand), product.Unit))
	current.TextStyle = fyne.TextStyle{Bold: true}

	preview := widget.NewLabel("")
	preview.Importance = widget.LowImportance

	// The resulting level is shown as the number is typed, so an operator sees
	// what they are about to do before they commit to it.
	updatePreview := func(string) {
		n, err := parseQuantity(quantity.Text)
		if err != nil {
			preview.SetText("Enter a whole number.")
			preview.Importance = widget.DangerImportance
			preview.Refresh()
			return
		}

		var after int64
		switch action {
		case stockActionReceive:
			after = product.OnHand + n
		case stockActionIssue:
			after = product.OnHand - n
		case stockActionCount:
			after = n
		}

		preview.Importance = widget.LowImportance
		text := sprintf("After this: %s %s", formatQuantity(after), product.Unit)
		if after < 0 {
			preview.Importance = widget.DangerImportance
			text += "  — more than is available"
		} else if action == stockActionCount && n != product.OnHand {
			text += sprintf("   (variance %s)", formatDelta(n-product.OnHand))
		}
		preview.SetText(text)
		preview.Refresh()
	}
	quantity.OnChanged = updatePreview
	updatePreview("")

	grid := container.New(newFormGrid())
	formRow(grid, "Product", widget.NewLabel(product.SKU+" — "+product.Name))
	formRow(grid, "Current", current)

	switch action {
	case stockActionReceive:
		formRow(grid, "Quantity received", quantity)
		formRow(grid, "Unit cost", cost)
		if product.TrackLots {
			formRow(grid, "Lot number", lot)
			formRow(grid, "Expiry date", expiry)
		}
	case stockActionIssue:
		formRow(grid, "Quantity issued", quantity)
	case stockActionCount:
		formRow(grid, "Counted on shelf", quantity)
		formRow(grid, "Reason", reason)
	}
	formRow(grid, "Note", note)
	formRow(grid, "", preview)

	submit := func(confirmed bool) {
		if !confirmed {
			return
		}

		n, err := parseQuantity(quantity.Text)
		if err != nil {
			a.showError(err)
			return
		}

		in := service.AdjustStockInput{
			ProductID:  product.ID,
			LocationID: a.location,
			Delta:      n,
			Note:       strings.TrimSpace(note.Text),
		}

		switch action {
		case stockActionReceive:
			unitCost, err := core.ParseMoney(orZero(cost.Text), a.currency)
			if err != nil {
				a.showError(wrapf(err, "the unit cost is not valid"))
				return
			}
			in.UnitCost = unitCost
			in.LotNumber = strings.TrimSpace(lot.Text)
			if raw := strings.TrimSpace(expiry.Text); raw != "" {
				when, err := time.Parse("2006-01-02", raw)
				if err != nil {
					a.showError(fmt.Errorf("%w: the expiry date must look like 2026-12-31", core.ErrInvalid))
					return
				}
				in.ExpiryDate = when
			}
			a.postStock(product, func(ctx context.Context) (int64, error) {
				return a.svc.ReceiveStock(ctx, in)
			})

		case stockActionIssue:
			a.postStock(product, func(ctx context.Context) (int64, error) {
				return a.svc.IssueStock(ctx, in)
			})

		case stockActionCount:
			count := service.SetStockInput{
				ProductID:  product.ID,
				LocationID: a.location,
				Counted:    n,
				Note:       in.Note,
			}
			a.postStock(product, func(ctx context.Context) (int64, error) {
				return a.svc.SetStock(ctx, count)
			})
		}
	}

	d := dialog.NewCustomConfirm(action.title(), action.confirmLabel(), "Cancel",
		container.NewPadded(grid), submit, a.window)
	d.Resize(fyne.NewSize(560, 420))
	d.Show()

	a.window.Canvas().Focus(quantity)
}

func (a *App) postStock(product core.ProductWithStock, post func(context.Context) (int64, error)) {
	a.background(func(ctx context.Context) {
		onHand, err := post(ctx)
		a.onMain(func() {
			if err != nil {
				a.showError(err)
				return
			}
			a.status.success("%s is now at %s %s.",
				product.SKU, formatQuantity(onHand), product.Unit)
			a.reloadAll()
		})
	})
}
